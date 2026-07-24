package main

// HTTP handlers for the OAuth2 device flow (RFC 8628) and the human
// verification page. Endpoint paths match the Talos defaults derived
// from the talos.config URL: /device/code and /token.

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

const deviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// externalBase reconstructs the externally visible base URL for
// verification_uri, honoring a TLS-terminating proxy.
func externalBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

// handleDeviceCode implements the device authorization endpoint.
// Talos sends client_id plus any extra_variable values (uuid, mac, ...).
func (s *server) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request")
		return
	}

	clientID := r.FormValue("client_id")
	if s.clientID != "" && clientID != s.clientID {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}

	// Everything that isn't a standard OAuth parameter is machine identity.
	identity := make(map[string]string)
	for k, vs := range r.Form {
		switch k {
		case "client_id", "client_secret", "scope", "audience":
			continue
		}
		if len(vs) > 0 && vs[0] != "" {
			identity[k] = vs[0]
		}
	}

	da := s.store.begin(clientID, identity)
	base := externalBase(r)

	log.Printf("device auth started: user_code=%s identity=%v", da.UserCode, identity)
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               da.DeviceCode,
		"user_code":                 da.UserCode,
		"verification_uri":          base + "/verify",
		"verification_uri_complete": base + "/verify?user_code=" + da.UserCode,
		"expires_in":                int(deviceAuthTTL.Seconds()),
		"interval":                  int(pollInterval.Seconds()),
	})
}

// handleToken implements the token endpoint (device_code grant only).
func (s *server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request")
		return
	}
	if r.FormValue("grant_type") != deviceCodeGrantType {
		oauthError(w, "unsupported_grant_type")
		return
	}

	token, errCode := s.store.poll(r.FormValue("device_code"))
	if errCode != "" {
		oauthError(w, errCode)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(tokenTTL.Seconds()),
	})
}

var verifyTemplate = template.Must(template.New("verify").Parse(`<!DOCTYPE html>
<html>
<head><title>Machine approval</title>
<style>
 body { font-family: monospace; max-width: 44rem; margin: 2rem auto; }
 table { border-collapse: collapse; width: 100%; }
 td, th { border: 1px solid #999; padding: .4rem .6rem; text-align: left; }
 .msg { padding: .5rem; border: 1px solid #999; margin-bottom: 1rem; }
 pre { background: #f4f4f4; padding: .5rem; overflow-x: auto; }
 details { margin: .5rem 0; }
</style></head>
<body>
<h1>Pending machine approvals</h1>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
{{if .WGSealed}}
<div class="msg" style="border-color:#c00">
 <strong>⚠ WireGuard control channel is SEALED</strong> — config serving is paused.
 <p>Unsealing signs the master-key derivation message. <strong>This signature IS the
 fleet master key.</strong> Only ever sign it on this page or offline via
 <code>cast wallet sign</code> — never on any other site.</p>
 <form method="POST" action="/unseal" id="unseal-form">
 {{if .WalletEnabled}}<button type="button" id="unseal-wallet">Unseal with wallet</button>{{end}}
  <details>
   <summary>Sign manually (e.g. cast wallet sign)</summary>
   <p>Sign exactly:</p><pre>{{.MasterMessage}}</pre>
   <input type="text" name="signature" placeholder="0x signature" size="60">
   <button>Submit unseal</button>
  </details>
 </form>
</div>
{{end}}
{{if not .Pending}}<p>No pending requests.</p>{{end}}
{{range .Pending}}
<table>
 <tr><th>User code</th><td>{{.Auth.UserCode}}</td></tr>
 {{range $k, $v := .Auth.Identity}}<tr><th>{{$k}}</th><td>{{$v}}</td></tr>{{end}}
 <tr><th>Requested</th><td>{{.Auth.CreatedAt.Format "15:04:05"}}</td></tr>
</table>
<form method="POST" action="/verify" data-msg-approve="{{.MsgApprove}}" data-msg-deny="{{.MsgDeny}}">
 <input type="hidden" name="user_code" value="{{.Auth.UserCode}}">
{{if $.WalletEnabled}}
 <button type="button" class="wallet" data-action="approve">Approve with wallet</button>
 <button type="button" class="wallet" data-action="deny">Deny with wallet</button>
 <details>
  <summary>Sign manually (e.g. cast wallet sign)</summary>
  <p>To approve, sign:</p><pre>{{.MsgApprove}}</pre>
  <p>To deny, sign:</p><pre>{{.MsgDeny}}</pre>
  <input type="text" name="signature" placeholder="0x signature" size="60">
  <button name="action" value="approve">Submit approve</button>
  <button name="action" value="deny">Submit deny</button>
 </details>
{{end}}
{{if $.TokenEnabled}}
 <details>
  <summary>Admin token (break-glass)</summary>
  <input type="password" name="admin_token" placeholder="admin token">
  <button name="action" value="approve">Approve</button>
  <button name="action" value="deny">Deny</button>
 </details>
{{end}}
</form>
<br>
{{end}}
{{if .WalletEnabled}}
<script>
(function () {
  var btn = document.getElementById('unseal-wallet');
  if (!btn) return;
  btn.addEventListener('click', async function () {
    if (!window.ethereum) { alert('no wallet found'); return; }
    try {
      var accounts = await ethereum.request({ method: 'eth_requestAccounts' });
      var sig = await ethereum.request({ method: 'personal_sign', params: [{{.MasterMessage}}, accounts[0]] });
      var form = document.getElementById('unseal-form');
      form.querySelector('input[name=signature]').value = sig;
      form.submit();
    } catch (e) { alert('signing failed: ' + (e.message || e)); }
  });
})();
document.querySelectorAll('button.wallet').forEach(function (btn) {
  btn.addEventListener('click', async function () {
    var form = btn.closest('form');
    var action = btn.dataset.action;
    var msg = action === 'approve' ? form.dataset.msgApprove : form.dataset.msgDeny;
    if (!window.ethereum) { alert('no wallet found'); return; }
    try {
      var accounts = await ethereum.request({ method: 'eth_requestAccounts' });
      var sig = await ethereum.request({ method: 'personal_sign', params: [msg, accounts[0]] });
      form.querySelector('input[name=signature]').value = sig;
      var act = document.createElement('input');
      act.type = 'hidden'; act.name = 'action'; act.value = action;
      form.appendChild(act);
      form.submit();
    } catch (e) { alert('signing failed: ' + (e.message || e)); }
  });
});
</script>
{{end}}
</body></html>`))

type verifyEntry struct {
	Auth       *deviceAuth
	MsgApprove string
	MsgDeny    string
}

type verifyPageData struct {
	Pending       []verifyEntry
	Message       string
	WalletEnabled bool
	TokenEnabled  bool
	WGSealed      bool
	MasterMessage string
}

func (s *server) handleVerifyPage(w http.ResponseWriter, r *http.Request) {
	s.renderVerify(w, "")
}

func (s *server) handleVerifyPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	userCode := strings.ToUpper(strings.TrimSpace(r.FormValue("user_code")))
	action := r.FormValue("action")
	if action != "approve" && action != "deny" {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	if !s.authorizeAdmin(r, userCode, action) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var err error
	if action == "approve" {
		err = s.store.approve(userCode)
	} else {
		err = s.store.deny(userCode)
	}
	if err != nil {
		s.renderVerify(w, fmt.Sprintf("%s: %v", userCode, err))
		return
	}

	log.Printf("device auth %sd: user_code=%s", action, userCode)
	s.renderVerify(w, fmt.Sprintf("%s %sd", userCode, action))
}

// authorizeAdmin accepts either a wallet signature over the canonical
// approval message (checked against the address allowlist) or the
// break-glass admin token.
func (s *server) authorizeAdmin(r *http.Request, userCode, action string) bool {
	if sig := r.FormValue("signature"); sig != "" && len(s.adminAddrs) > 0 {
		nonce, err := s.store.nonceFor(userCode)
		if err != nil {
			return false
		}
		addr, err := recoverPersonalSign(approvalMessage(action, userCode, nonce), sig)
		if err != nil {
			log.Printf("signature verification failed for %s: %v", userCode, err)
			return false
		}
		for _, allowed := range s.adminAddrs {
			if addr == allowed {
				log.Printf("wallet %s authorized %s of %s", addr, action, userCode)
				return true
			}
		}
		log.Printf("wallet %s not in allowlist (user_code=%s)", addr, userCode)
		return false
	}

	if s.adminToken != "" && constantTimeEqual(r.FormValue("admin_token"), s.adminToken) {
		return true
	}
	return false
}

func (s *server) renderVerify(w http.ResponseWriter, msg string) {
	var entries []verifyEntry
	for _, da := range s.store.pending() {
		entries = append(entries, verifyEntry{
			Auth:       da,
			MsgApprove: approvalMessage("approve", da.UserCode, da.Nonce),
			MsgDeny:    approvalMessage("deny", da.UserCode, da.Nonce),
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := verifyTemplate.Execute(w, verifyPageData{
		Pending:       entries,
		Message:       msg,
		WalletEnabled: len(s.adminAddrs) > 0,
		TokenEnabled:  s.adminToken != "",
		WGSealed:      s.wgm != nil && s.wgm.sealed(),
		MasterMessage: wgderive.MasterMessage,
	})
	if err != nil {
		log.Printf("rendering verify page: %v", err)
	}
}

// bearerToken extracts the token from the Authorization header, or "".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}
