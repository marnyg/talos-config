package siweoidc

// The OIDC wire surface. Handlers own HTTP shape only; grants and keys
// live in siweoidc.go. The sign-in page reuses the wallet UX of the
// hub's /status login (window.ethereum personal_sign with a manual
// cast-wallet-sign fallback) so the operator sees one signing flow
// everywhere.

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/marnyg/talos-config/config-server/ethsig"
)

// Handler returns the provider's HTTP mux.
func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("GET /jwks", p.handleJWKS)
	mux.HandleFunc("GET /authorize", p.handleAuthorizeGet)
	mux.HandleFunc("POST /authorize", p.handleAuthorizePost)
	mux.HandleFunc("POST /token", p.handleToken)
	mux.HandleFunc("GET /userinfo", p.handleUserinfo)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Provider) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.issuer,
		"authorization_endpoint":                p.issuer + "/authorize",
		"token_endpoint":                        p.issuer + "/token",
		"userinfo_endpoint":                     p.issuer + "/userinfo",
		"jwks_uri":                              p.issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "groups"},
		"claims_supported": []string{
			"sub", "iss", "aud", "exp", "iat", "nonce",
			"name", "preferred_username", "email", "email_verified", "groups",
		},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (p *Provider) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, p.signer.jwks())
}

// redirectError reports a protocol error to the client's registered
// redirect URI (RFC 6749 §4.1.2.1). Only called after the client and
// redirect URI validated — never redirect anywhere else.
func redirectError(w http.ResponseWriter, r *http.Request, req authRequest, code string) {
	u, err := url.Parse(req.redirectURI)
	if err != nil {
		http.Error(w, code, http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", code)
	if req.state != "" {
		q.Set("state", req.state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (p *Provider) handleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	q := map[string]string{}
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			q[k] = vs[0]
		}
	}
	req, errCode, err := p.validateAuthRequest(q)
	if err != nil {
		log.Printf("authorize: rejected: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errCode != "" {
		log.Printf("authorize: client %s: %s", req.clientID, errCode)
		redirectError(w, r, req, errCode)
		return
	}
	p.renderLogin(w, req, "")
}

func (p *Provider) handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	q := map[string]string{}
	for _, k := range []string{
		"client_id", "redirect_uri", "response_type", "state", "nonce",
		"code_challenge", "code_challenge_method",
	} {
		q[k] = r.FormValue(k)
	}
	// Re-validate from scratch: the form round-trip is untrusted input,
	// same as the query string was.
	req, errCode, err := p.validateAuthRequest(q)
	if err != nil {
		log.Printf("authorize: rejected on submit: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errCode != "" {
		redirectError(w, r, req, errCode)
		return
	}

	nonce, sig := r.FormValue("login_nonce"), r.FormValue("signature")
	if nonce == "" || sig == "" {
		http.Error(w, "missing nonce or signature", http.StatusBadRequest)
		return
	}
	if !p.redeemNonce(nonce) {
		p.renderLogin(w, req, "sign-in challenge expired or already used — try again")
		return
	}
	addr, err := ethsig.RecoverPersonalSign(loginMessage(req.clientID, nonce), sig)
	if err != nil {
		log.Printf("authorize: signature verification failed: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, ok := p.identityFor(addr)
	if !ok {
		log.Printf("authorize: wallet %s not in allowlist", addr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	code := p.mintCode(req, id)
	log.Printf("authorize: wallet %s (%s) signed in to %s", addr, id.Username, req.clientID)

	u, err := url.Parse(req.redirectURI)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	qs := u.Query()
	qs.Set("code", code)
	if req.state != "" {
		qs.Set("state", req.state)
	}
	u.RawQuery = qs.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func (p *Provider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if r.FormValue("grant_type") != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	// client_secret, if a relying party insists on sending one (e.g.
	// oauth2-proxy requires the field to be set), is ignored: public
	// clients, PKCE is the proof.
	//
	// client_id may arrive as HTTP Basic auth instead of a form param
	// (RFC 6749 §2.3.1, form-urlencoded username, empty password) —
	// golang.org/x/oauth2 (ArgoCD, oauth2-proxy) tries that style
	// first, and rejecting it burns the code before the library's
	// in-params retry arrives.
	clientID := r.FormValue("client_id")
	if u, _, ok := r.BasicAuth(); ok && clientID == "" {
		if dec, err := url.QueryUnescape(u); err == nil {
			clientID = dec
		}
	}
	resp, err := p.redeemCode(
		r.FormValue("code"),
		clientID,
		r.FormValue("redirect_uri"),
		r.FormValue("code_verifier"),
	)
	if err != nil {
		log.Printf("token: exchange failed: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func (p *Provider) handleUserinfo(w http.ResponseWriter, r *http.Request) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	claims, ok := p.userinfoFor(h[len(prefix):])
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	writeJSON(w, http.StatusOK, claims)
}

// The sign-in page: same visual language and wallet helper as the
// hub's /status login, minus the break-glass token (an SSO bypass
// credential would defeat the point).
var loginTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html>
<head><title>Sign in</title><style>
 :root {
   --bg: #f5f6f8; --panel: #ffffff; --border: #d8dde4;
   --text: #1d2530; --muted: #67717e; --accent: #2563eb;
   --code-bg: #edf0f4;
 }
 @media (prefers-color-scheme: dark) {
   :root {
     --bg: #0f141b; --panel: #161d26; --border: #2b3542;
     --text: #d6dde7; --muted: #8593a3; --accent: #60a5fa;
     --code-bg: #0b1017;
   }
 }
 * { box-sizing: border-box; }
 body {
   font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
   font-size: 14px; line-height: 1.55;
   background: var(--bg); color: var(--text);
   max-width: 40rem; margin: 0 auto; padding: 3rem 1.25rem;
 }
 h1 {
   font-size: 1.35rem; margin: 0 0 1.25rem;
   padding-bottom: .6rem; border-bottom: 2px solid var(--border);
 }
 .msg {
   padding: .7rem .9rem; margin-bottom: 1rem;
   background: var(--panel); border: 1px solid var(--border);
   border-left: 4px solid var(--accent); border-radius: 4px;
 }
 pre {
   background: var(--code-bg); border: 1px solid var(--border);
   border-radius: 4px; padding: .6rem .8rem; overflow-x: auto;
 }
 code { background: var(--code-bg); padding: .1rem .35rem; border-radius: 3px; }
 button {
   font: inherit; cursor: pointer;
   background: var(--accent); color: #fff;
   border: 1px solid var(--accent); border-radius: 4px;
   padding: .35rem .9rem; margin: .15rem .15rem .15rem 0;
 }
 button:hover { filter: brightness(1.1); }
 details {
   margin: .5rem 0; padding: .4rem .7rem;
   border: 1px dashed var(--border); border-radius: 4px;
 }
 details button { background: var(--panel); color: var(--text); border-color: var(--border); }
 summary { cursor: pointer; color: var(--muted); }
 input[type=text] {
   font: inherit; background: var(--bg); color: var(--text);
   border: 1px solid var(--border); border-radius: 4px;
   padding: .35rem .6rem; margin: .15rem .15rem .15rem 0; max-width: 100%;
 }
</style></head>
<body>
<h1>Sign in to {{.ClientID}}</h1>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
<p>Sign with an admin wallet to continue to <code>{{.ClientID}}</code>.</p>
<form method="POST" action="/authorize" id="login-form">
 <input type="hidden" name="client_id" value="{{.ClientID}}">
 <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
 <input type="hidden" name="response_type" value="code">
 <input type="hidden" name="state" value="{{.State}}">
 <input type="hidden" name="nonce" value="{{.OIDCNonce}}">
 <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
 <input type="hidden" name="code_challenge_method" value="S256">
 <input type="hidden" name="login_nonce" value="{{.LoginNonce}}">
 <button type="button" id="login-wallet">Sign in with wallet</button>
 <details>
  <summary>Sign manually (e.g. cast wallet sign)</summary>
  <p>Sign exactly:</p><pre>{{.Msg}}</pre>
  <input type="text" name="signature" placeholder="0x signature" size="60">
  <button>Submit</button>
 </details>
</form>
<script>
(function () {
  async function walletSign(msg) {
    if (!window.ethereum) { alert('no wallet found'); return null; }
    var accounts = await ethereum.request({ method: 'eth_requestAccounts' });
    return await ethereum.request({ method: 'personal_sign', params: [msg, accounts[0]] });
  }
  document.getElementById('login-wallet').addEventListener('click', async function () {
    try {
      var sig = await walletSign({{.Msg}});
      if (!sig) return;
      var form = document.getElementById('login-form');
      form.querySelector('input[name=signature]').value = sig;
      form.submit();
    } catch (e) { alert('signing failed: ' + (e.message || e)); }
  });
})();
</script>
</body></html>`))

func (p *Provider) renderLogin(w http.ResponseWriter, req authRequest, msg string) {
	nonce := p.issueNonce()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := loginTemplate.Execute(w, map[string]any{
		"ClientID":      req.clientID,
		"RedirectURI":   req.redirectURI,
		"State":         req.state,
		"OIDCNonce":     req.oidcNonce,
		"CodeChallenge": req.codeChallenge,
		"LoginNonce":    nonce,
		"Msg":           loginMessage(req.clientID, nonce),
		"Message":       msg,
	})
	if err != nil {
		log.Printf("rendering sign-in page: %v", err)
	}
}
