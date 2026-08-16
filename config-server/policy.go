package main

// The mesh policy page at /policy (task 6462fed4, phase 2): view the
// git-derived access policy, install an ephemeral overlay for
// experiments, see the effective-vs-git diff, and export the overlay
// text for a commit. Follows the /status pattern exactly: a SIWE
// session gates *viewing*; installing or clearing the overlay requires
// a per-action wallet signature — a stolen session cookie cannot
// rewrite the mesh firewall. No break-glass token path on mutations:
// the break-glass for policy is a git commit, which is also the only
// durable one (invariant 2).
//
// The signed set-message binds the sha256 of the exact document being
// installed plus a single-use nonce, so a signature can neither be
// replayed nor spliced onto different content.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/ethsig"
)

// policySetMessageV1 is the canonical text a wallet signs to install a
// policy overlay. Distinct prefix from every other signable message
// (enrollment, approval, login, master key): a signature for one must
// never be replayable as another.
func policySetMessageV1(sha256Hex, nonce string) string {
	return fmt.Sprintf(
		"talos config-server mesh policy overlay v1\nsha256: %s\nnonce: %s",
		sha256Hex, nonce,
	)
}

// policyClearMessageV1 is the canonical text a wallet signs to clear
// the overlay. Clearing is signed too: an overlay may be *narrower*
// than git, so reverting can widen access.
func policyClearMessageV1(nonce string) string {
	return fmt.Sprintf(
		"talos config-server mesh policy overlay clear v1\nnonce: %s",
		nonce,
	)
}

// normalizePolicyText undoes the CRLF the form post added: the browser
// hashes textarea.value (LF per spec), then form encoding submits CRLF.
// Hash and store the LF form so the signature matches what was signed.
func normalizePolicyText(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func policySHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// policyEnabled mirrors statusEnabled plus a live mesh manager: the
// page is about the mesh policy, so without a mesh there is nothing to
// show (404, same as any unknown path).
func (s *server) policyEnabled() bool {
	return s.statusEnabled() && s.mesh() != nil
}

func (s *server) handlePolicyPage(w http.ResponseWriter, r *http.Request) {
	if !s.policyEnabled() {
		http.NotFound(w, r)
		return
	}
	addr, ok := s.sessionAddr(r)
	if !ok {
		// No anonymous rendering, same stance as /status; the login
		// form lives there.
		http.Redirect(w, r, "/status", http.StatusSeeOther)
		return
	}
	s.renderPolicy(w, addr, r.URL.Query().Get("msg"))
}

func (s *server) handlePolicySet(w http.ResponseWriter, r *http.Request) {
	if !s.policyEnabled() {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.sessionAddr(r); !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	text := normalizePolicyText(r.FormValue("policy"))
	nonce, sig := r.FormValue("nonce"), r.FormValue("signature")
	if text == "" || nonce == "" || sig == "" {
		http.Error(w, "policy, nonce and signature required", http.StatusBadRequest)
		return
	}
	if !s.sessions.redeemNonce(nonce) {
		s.respondPolicy(w, r, "challenge expired or already used — try again")
		return
	}
	addr, err := ethsig.RecoverPersonalSign(policySetMessageV1(policySHA256(text), nonce), sig)
	if err != nil {
		log.Printf("policy overlay: signature verification failed: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !slices.Contains(s.adminAddrs, addr) {
		log.Printf("policy overlay: wallet %s not in allowlist", addr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.mesh().SetPolicyOverlay([]byte(text), addr); err != nil {
		s.respondPolicy(w, r, fmt.Sprintf("rejected: %v", err))
		return
	}
	log.Printf("policy overlay installed by %s (sha256 %s)", addr, policySHA256(text))
	s.respondPolicy(w, r, "overlay installed — new enrollments and node config serves now use it")
}

func (s *server) handlePolicyClear(w http.ResponseWriter, r *http.Request) {
	if !s.policyEnabled() {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.sessionAddr(r); !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	nonce, sig := r.FormValue("nonce"), r.FormValue("signature")
	if nonce == "" || sig == "" {
		http.Error(w, "nonce and signature required", http.StatusBadRequest)
		return
	}
	if !s.sessions.redeemNonce(nonce) {
		s.respondPolicy(w, r, "challenge expired or already used — try again")
		return
	}
	addr, err := ethsig.RecoverPersonalSign(policyClearMessageV1(nonce), sig)
	if err != nil {
		log.Printf("policy clear: signature verification failed: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !slices.Contains(s.adminAddrs, addr) {
		log.Printf("policy clear: wallet %s not in allowlist", addr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.mesh().ClearPolicyOverlay()
	log.Printf("policy overlay cleared by %s", addr)
	s.respondPolicy(w, r, "overlay cleared — effective policy is the git file again")
}

func (s *server) respondPolicy(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/policy?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

// --- diff ---

// diffLine is one row of the effective-vs-git diff. Op is " ", "-"
// (git only) or "+" (overlay only).
type diffLine struct {
	Op   string
	Text string
}

// diffPolicyLines is a plain LCS line diff. Policy documents are tens
// of lines, so the quadratic table is nothing; no need for a
// dependency.
func diffPolicyLines(a, b string) []diffLine {
	al := strings.Split(strings.TrimSuffix(a, "\n"), "\n")
	bl := strings.Split(strings.TrimSuffix(b, "\n"), "\n")
	// lcs[i][j] = length of the LCS of al[i:] and bl[j:].
	lcs := make([][]int, len(al)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(bl)+1)
	}
	for i := len(al) - 1; i >= 0; i-- {
		for j := len(bl) - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	for i < len(al) && j < len(bl) {
		switch {
		case al[i] == bl[j]:
			out = append(out, diffLine{" ", al[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, diffLine{"-", al[i]})
			i++
		default:
			out = append(out, diffLine{"+", bl[j]})
			j++
		}
	}
	for ; i < len(al); i++ {
		out = append(out, diffLine{"-", al[i]})
	}
	for ; j < len(bl); j++ {
		out = append(out, diffLine{"+", bl[j]})
	}
	return out
}

// --- page ---

var policyTemplate = template.Must(template.New("policy").Parse(statusPageHead("Mesh policy") + `
<h1>Mesh policy</h1>
<p class="session-line">signed in as {{.Addr}} — <a href="/status">status</a></p>
{{if .Message}}<div class="msg">{{.Message}}</div>
<script>history.replaceState(null, '', '/policy');</script>
{{end}}

<h2>Effective policy</h2>
{{if .Overlay}}
<div class="msg warn">
 <strong>⚠ Ephemeral overlay active</strong> — installed by {{.OverlayBy}} at
 {{.OverlaySince}}. It lives in hub memory only: a redeploy or restart reverts
 to git. To keep it, commit the overlay text below as
 <code>talos/mesh-policy.yaml</code>.
</div>
{{else}}
<p>The git file is in effect (no overlay).</p>
{{end}}
<p class="session-line">Propagation: device rules apply on the next enrollment,
node rules on the next <code>apply</code>, the hub's own firewall on the next
unseal — an overlay never outlives this process, so the hub scope effectively
rides git until live sync lands.</p>

{{if .Overlay}}
<h2>Diff — git → overlay</h2>
<pre>{{range .Diff}}{{if eq .Op "+"}}<span style="color:#15803d">+ {{.Text}}</span>{{else if eq .Op "-"}}<span style="color:#b91c1c">- {{.Text}}</span>{{else}}  {{.Text}}{{end}}
{{end}}</pre>
<h2>Export</h2>
<p>To make the overlay durable, commit exactly this as
<code>talos/mesh-policy.yaml</code>:</p>
<pre id="export-text">{{.OverlayRaw}}</pre>
<form method="POST" action="/policy/clear" id="clear-form">
 <input type="hidden" name="nonce" value="{{.ClearNonce}}">
 <button type="button" id="clear-wallet">Clear overlay (revert to git)</button>
 <details>
  <summary>Sign manually (e.g. cast wallet sign)</summary>
  <p>Sign exactly:</p><pre>{{.ClearMsg}}</pre>
  <input type="text" name="signature" placeholder="0x signature" size="60">
  <button>Submit clear</button>
 </details>
</form>
{{end}}

<h2>Git file{{if .Overlay}} (base){{end}}</h2>
<pre>{{.GitRaw}}</pre>

<h2>Install overlay</h2>
<p>Edit the candidate policy and sign it in. Validation is the same as the git
file's — unknown groups, malformed rules and empty scopes are rejected before
anything changes.</p>
<form method="POST" action="/policy/overlay" id="set-form">
 <input type="hidden" name="nonce" value="{{.SetNonce}}">
 <textarea name="policy" id="policy-text" rows="24" cols="90"
  style="width:100%;font-family:inherit">{{.EditText}}</textarea>
 <p><button type="button" id="set-wallet">Sign &amp; install overlay</button></p>
 <details>
  <summary>Sign manually (e.g. cast wallet sign)</summary>
  <p>The message binds the sha256 of the exact text above; it changes as you
  type. Sign exactly:</p>
  <pre id="set-msg"></pre>
  <input type="text" name="signature" placeholder="0x signature" size="60">
  <button>Submit overlay</button>
 </details>
</form>

<script>
(function () {
` + walletSignJS + `
  async function sha256Hex(text) {
    var buf = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text));
    return Array.from(new Uint8Array(buf)).map(function (b) {
      return b.toString(16).padStart(2, '0');
    }).join('');
  }
  var setNonce = {{.SetNonce}};
  function setMessage(sum) {
    return 'talos config-server mesh policy overlay v1\nsha256: ' + sum + '\nnonce: ' + setNonce;
  }
  var ta = document.getElementById('policy-text');
  var msgPre = document.getElementById('set-msg');
  async function refreshMsg() { msgPre.textContent = setMessage(await sha256Hex(ta.value)); }
  ta.addEventListener('input', refreshMsg);
  refreshMsg();
  document.getElementById('set-wallet').addEventListener('click', async function () {
    try {
      var sig = await walletSign(setMessage(await sha256Hex(ta.value)));
      if (!sig) return;
      var form = document.getElementById('set-form');
      form.querySelector('input[name=signature]').value = sig;
      form.submit();
    } catch (e) { alert('signing failed: ' + (e.message || e)); }
  });
  var clearBtn = document.getElementById('clear-wallet');
  if (clearBtn) clearBtn.addEventListener('click', async function () {
    try {
      var sig = await walletSign({{.ClearMsg}});
      if (!sig) return;
      var form = document.getElementById('clear-form');
      form.querySelector('input[name=signature]').value = sig;
      form.submit();
    } catch (e) { alert('signing failed: ' + (e.message || e)); }
  });
})();
</script>
</body></html>`))

func (s *server) renderPolicy(w http.ResponseWriter, addr, msg string) {
	nm := s.mesh()
	gitRaw, err := nm.PolicyGitRaw()
	if err != nil {
		log.Printf("policy page: reading git policy: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	overlayRaw, by, since, hasOverlay := nm.PolicyOverlay()

	// The edit box starts from what is currently effective, so
	// iterating on an installed overlay does not restart from git.
	editText := string(gitRaw)
	if hasOverlay {
		editText = string(overlayRaw)
	}

	clearNonce := s.sessions.issueNonce()
	data := map[string]any{
		"Addr":         addr,
		"Message":      msg,
		"GitRaw":       string(gitRaw),
		"Overlay":      hasOverlay,
		"OverlayRaw":   string(overlayRaw),
		"OverlayBy":    by,
		"OverlaySince": since.UTC().Format(time.RFC3339),
		"EditText":     editText,
		"SetNonce":     s.sessions.issueNonce(),
		"ClearNonce":   clearNonce,
		"ClearMsg":     policyClearMessageV1(clearNonce),
	}
	if hasOverlay {
		data["Diff"] = diffPolicyLines(string(gitRaw), string(overlayRaw))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := policyTemplate.Execute(w, data); err != nil {
		log.Printf("rendering policy page: %v", err)
	}
}
