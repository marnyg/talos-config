package main

// The operator dashboard at /status, behind a SIWE session login (or
// the break-glass admin token). Zero information is rendered before
// the operator authenticates: the logged-out page is only the sign-in
// prompt. Shows seal state, mesh membership with live tunnel state,
// per-machine last config fetch, and auto-bootstrap state, and hosts
// the unseal form and pending machine approvals. The session only
// gates *viewing* — approve/deny/unseal remain per-action wallet
// signatures (POST /verify, POST /unseal); a stolen session cookie
// cannot approve a machine or unseal the hub.

import (
	"cmp"
	"fmt"
	"html/template"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/mesh"
)

const sessionCookieName = "talos_status_session"

// tokenSessionAddr labels sessions opened with the break-glass admin
// token instead of a wallet signature.
const tokenSessionAddr = "admin (break-glass token)"

// statusEnabled: the page only exists when some admin credential is
// configured (wallet allowlist or break-glass token) — there is no
// other way in, and without one the page must leak nothing (404, same
// as any unknown path).
func (s *server) statusEnabled() bool {
	return (len(s.adminAddrs) > 0 || s.adminToken != "") && s.sessions != nil
}

// sessionAddr resolves the request's session cookie to a wallet address.
func (s *server) sessionAddr(r *http.Request) (string, bool) {
	if s.sessions == nil {
		return "", false
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return s.sessions.addrFor(c.Value)
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.statusEnabled() {
		http.NotFound(w, r)
		return
	}
	if addr, ok := s.sessionAddr(r); ok {
		s.renderStatus(w, addr, r.URL.Query().Get("msg"))
		return
	}
	s.renderLogin(w, "")
}

func (s *server) handleStatusLogin(w http.ResponseWriter, r *http.Request) {
	if !s.statusEnabled() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	// Break-glass: the admin token opens a session without a wallet.
	if tok := r.FormValue("admin_token"); tok != "" {
		if s.adminToken == "" || !constantTimeEqual(tok, s.adminToken) {
			log.Print("status login: bad admin token")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		log.Print("status login: admin token session opened")
		s.openSession(w, r, tokenSessionAddr)
		return
	}

	nonce, sig := r.FormValue("nonce"), r.FormValue("signature")
	if nonce == "" || sig == "" {
		http.Error(w, "missing nonce or signature", http.StatusBadRequest)
		return
	}
	if !s.sessions.redeemNonce(nonce) {
		s.renderLogin(w, "login challenge expired or already used — try again")
		return
	}
	addr, err := ethsig.RecoverPersonalSign(loginMessage(nonce), sig)
	if err != nil {
		log.Printf("status login: signature verification failed: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !slices.Contains(s.adminAddrs, addr) {
		log.Printf("status login: wallet %s not in allowlist", addr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	log.Printf("status login: wallet %s signed in", addr)
	s.openSession(w, r, addr)
}

// openSession sets the session cookie and redirects to the dashboard.
// Path is "/" so post-action renders on POST /verify and POST /unseal
// see the session too.
func (s *server) openSession(w http.ResponseWriter, r *http.Request, addr string) {
	token := s.sessions.create(addr)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(externalBase(r), "https://"),
	})
	http.Redirect(w, r, "/status", http.StatusSeeOther)
}

func (s *server) handleStatusLogout(w http.ResponseWriter, r *http.Request) {
	if !s.statusEnabled() {
		http.NotFound(w, r)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
	})
	http.Redirect(w, r, "/status", http.StatusSeeOther)
}

// respondAction reports the outcome of a signature-authorized action
// (approve/deny/unseal). Session holders get a POST-redirect-GET back
// to the dashboard (rendering in place would leave the browser parked
// on /unseal or /verify, where the auto-refresh GET then 405s); the
// outcome rides along as a query param. Headless callers
// (cast-wallet-sign + curl) get plain text.
func (s *server) respondAction(w http.ResponseWriter, r *http.Request, msg string) {
	if s.statusEnabled() {
		if _, ok := s.sessionAddr(r); ok {
			http.Redirect(w, r, "/status?msg="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
	}
	fmt.Fprintln(w, msg)
}

const statusStyle = `
 :root {
   --bg: #f5f6f8; --panel: #ffffff; --border: #d8dde4;
   --text: #1d2530; --muted: #67717e; --accent: #2563eb;
   --warn: #b91c1c; --warn-bg: #fdf2f2; --warn-border: #e8b4b4;
   --code-bg: #edf0f4; --thead: #eef1f5; --zebra: #f8fafc;
 }
 @media (prefers-color-scheme: dark) {
   :root {
     --bg: #0f141b; --panel: #161d26; --border: #2b3542;
     --text: #d6dde7; --muted: #8593a3; --accent: #60a5fa;
     --warn: #f08c8c; --warn-bg: #2a1717; --warn-border: #5c2b2b;
     --code-bg: #0b1017; --thead: #1b2430; --zebra: #131a23;
   }
 }
 * { box-sizing: border-box; }
 body {
   font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
   font-size: 14px; line-height: 1.55;
   background: var(--bg); color: var(--text);
   max-width: 64rem; margin: 0 auto; padding: 2rem 1.25rem 4rem;
 }
 h1 {
   font-size: 1.35rem; margin: 0 0 1.25rem;
   padding-bottom: .6rem; border-bottom: 2px solid var(--border);
 }
 h2 {
   font-size: .8rem; text-transform: uppercase; letter-spacing: .08em;
   color: var(--muted); margin: 2rem 0 .6rem;
 }
 a { color: var(--accent); }
 table {
   border-collapse: collapse; width: 100%; margin-bottom: 1rem;
   background: var(--panel);
 }
 td, th {
   border: 1px solid var(--border); padding: .45rem .7rem;
   text-align: left; vertical-align: top;
 }
 th { background: var(--thead); font-weight: 600; white-space: nowrap; }
 tbody tr:nth-child(even) td, table tr:nth-child(even) td { background: var(--zebra); }
 .msg {
   padding: .7rem .9rem; margin-bottom: 1rem;
   background: var(--panel); border: 1px solid var(--border);
   border-left: 4px solid var(--accent); border-radius: 4px;
 }
 .msg.warn, td.warn {
   background: var(--warn-bg); border-color: var(--warn-border);
   border-left-color: var(--warn); color: var(--warn);
 }
 .msg.warn code, .msg.warn pre { color: var(--text); }
 pre {
   background: var(--code-bg); border: 1px solid var(--border);
   border-radius: 4px; padding: .6rem .8rem; overflow-x: auto;
 }
 code { background: var(--code-bg); padding: .1rem .35rem; border-radius: 3px; }
 button {
   font: inherit; cursor: pointer;
   background: var(--panel); color: var(--text);
   border: 1px solid var(--border); border-radius: 4px;
   padding: .35rem .9rem; margin: .15rem .15rem .15rem 0;
 }
 button:hover { border-color: var(--accent); color: var(--accent); }
 button.wallet, #login-wallet, #unseal-wallet {
   background: var(--accent); border-color: var(--accent); color: #fff;
 }
 button.wallet:hover, #login-wallet:hover, #unseal-wallet:hover {
   filter: brightness(1.1); color: #fff;
 }
 input[type=text], input[type=password] {
   font: inherit; background: var(--bg); color: var(--text);
   border: 1px solid var(--border); border-radius: 4px;
   padding: .35rem .6rem; margin: .15rem .15rem .15rem 0; max-width: 100%;
 }
 input[type=text]:focus, input[type=password]:focus {
   outline: none; border-color: var(--accent);
 }
 details {
   margin: .5rem 0; padding: .4rem .7rem;
   border: 1px dashed var(--border); border-radius: 4px;
 }
 summary { cursor: pointer; color: var(--muted); }
 details[open] summary { margin-bottom: .4rem; }
 form.inline { display: inline; margin-left: .5rem; }
 form.inline button { padding: .15rem .6rem; font-size: .85em; }
 .session-line { color: var(--muted); margin: 0 0 1.25rem; }
`

// statusPageHead is the shared HTML shell opener for the admin pages
// (login and dashboard), so the style block and head boilerplate cannot
// drift between them. Closed by the templates' own </body></html>.
func statusPageHead(title string) string {
	return `<!DOCTYPE html>
<html>
<head><title>` + title + `</title><style>` + statusStyle + `</style></head>
<body>`
}

// walletSignJS is the one place the in-page wallet API is touched: both
// admin pages sign EIP-191 personal_sign messages through this helper,
// so the request shape cannot drift between the login and dashboard
// flows. Returns null when no wallet is present (already alerted).
const walletSignJS = `
  async function walletSign(msg) {
    if (!window.ethereum) { alert('no wallet found'); return null; }
    var accounts = await ethereum.request({ method: 'eth_requestAccounts' });
    return await ethereum.request({ method: 'personal_sign', params: [msg, accounts[0]] });
  }
`

var loginTemplate = template.Must(template.New("login").Parse(statusPageHead("Status") + `
<h1>Status</h1>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
<p>Sign in with an admin wallet to view cluster status.</p>
{{if .WalletEnabled}}
<form method="POST" action="/status/login" id="login-form">
 <input type="hidden" name="nonce" value="{{.Nonce}}">
 <button type="button" id="login-wallet">Sign in with wallet</button>
 <details>
  <summary>Sign manually (e.g. cast wallet sign)</summary>
  <p>Sign exactly:</p><pre>{{.Msg}}</pre>
  <input type="text" name="signature" placeholder="0x signature" size="60">
  <button>Submit</button>
 </details>
</form>
{{end}}
{{if .TokenEnabled}}
<details>
 <summary>Admin token (break-glass)</summary>
 <form method="POST" action="/status/login">
  <input type="password" name="admin_token" placeholder="admin token">
  <button>Sign in</button>
 </form>
</details>
{{end}}
{{if .WalletEnabled}}
<script>
(function () {
` + walletSignJS + `
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
{{end}}
</body></html>`))

func (s *server) renderLogin(w http.ResponseWriter, msg string) {
	nonce := s.sessions.issueNonce()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := loginTemplate.Execute(w, map[string]any{
		"Nonce":         nonce,
		"Msg":           loginMessage(nonce),
		"Message":       msg,
		"WalletEnabled": len(s.adminAddrs) > 0,
		"TokenEnabled":  s.adminToken != "",
	})
	if err != nil {
		log.Printf("rendering login page: %v", err)
	}
}

var statusTemplate = template.Must(template.New("status").Parse(statusPageHead("Cluster status") + `
<h1>Cluster status</h1>
<p class="session-line">signed in as {{.Addr}} — <a href="/policy">mesh policy</a>
 <form class="inline" method="POST" action="/status/logout"><button>sign out</button></form>
</p>
{{if .Message}}<div class="msg">{{.Message}}</div>
<script>history.replaceState(null, '', '/status');</script>
{{end}}
<div id="live">
{{if .Sealed}}
<div class="msg warn">
 <strong>⚠ Hub is SEALED</strong> — config serving is paused.
 <p>Unsealing signs the master-key derivation message. <strong>This signature IS the
 fleet master key.</strong> Only ever sign it on this page or offline via
 <code>cast wallet sign</code> — never on any other site.</p>
 <form method="POST" action="/unseal" id="unseal-form" data-msg="{{.MasterMessage}}">
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
<table>
 <tr><th>server</th><td>{{.Version}}{{if .Started}} — up since {{.Started}}{{end}}</td></tr>
 <tr><th>hub</th><td{{if .Sealed}} class="warn"{{end}}>{{.Seal}}</td></tr>
 {{if .Mesh}}<tr><th>mesh</th><td{{if .MeshWarn}} class="warn"{{end}}>{{.Mesh}}</td></tr>{{end}}
 {{with .Boot}}
 <tr><th>auto-bootstrap</th><td>{{.State}}{{if .Target}} — target {{.Target}} ({{.MeshIP}}){{end}}{{if .Done}} — cluster bootstrapped, idle{{else if .Attempted}} — Bootstrap called, watching etcd{{end}}{{if .LastErr}} — last error: {{.LastErr}}{{end}}</td></tr>
 {{else}}
 <tr><th>auto-bootstrap</th><td>disabled</td></tr>
 {{end}}
</table>
{{range .UndeclaredKMS}}
<div class="msg warn">machine sealed disk keys under UNDECLARED uuid <code>{{.}}</code> —
add <code>uuid: {{.}}</code> to its machines/&lt;mac&gt;/meta.yaml before the next
server restart or it will not be able to unlock its disks.</div>
{{end}}
<h2>Pending approvals</h2>
{{if not .Pending}}<p>No pending requests.</p>{{end}}
{{range .Pending}}
{{if .Mesh}}
<table>
 <tr><th>User code</th><td>{{.Auth.UserCode}}</td></tr>
 <tr><th>kind</th><td>{{.Auth.Kind}}</td></tr>
 <tr><th>pubkey fingerprint</th><td><code>{{index .Auth.Identity "pubkey_fp"}}</code></td></tr>
 <tr><th>proposed</th><td>{{index .Auth.Identity "proposed_name"}} ({{index .Auth.Identity "proposed_group"}})</td></tr>
 <tr><th>Requested</th><td>{{.Auth.CreatedAt.Format "15:04:05"}}</td></tr>
</table>
<p>Verify the fingerprint against what the device shows before approving.
You decide the final name and group — the device only proposed them.</p>
<form method="POST" action="/mesh/enroll/approve" class="mesh-enroll"
      data-fp="{{index .Auth.Identity "pubkey_fp"}}" data-nonce="{{.Auth.Nonce}}">
 <input type="hidden" name="user_code" value="{{.Auth.UserCode}}">
 <p>
  <label>name <input type="text" name="name" value="{{index .Auth.Identity "proposed_name"}}"></label>
  <label><input type="radio" name="group" value="media"{{if ne (index .Auth.Identity "proposed_group") "admins"}} checked{{end}}> media</label>
  <label><input type="radio" name="group" value="admins"{{if eq (index .Auth.Identity "proposed_group") "admins"}} checked{{end}}> admins</label>
  <label>retype name to confirm admins <input type="text" name="admin_retype" placeholder="required for admins"></label>
 </p>
 <p>The wallet signs exactly this (rebuilt live from the fields above):</p>
 <pre class="mesh-msg"></pre>
{{if $.WalletEnabled}}
 <button type="button" class="wallet mesh-approve">Approve with wallet</button>
{{end}}
 <details>
  <summary>Sign manually (e.g. cast wallet sign)</summary>
  <input type="text" name="signature" placeholder="0x signature" size="60">
  <button>Submit approve</button>
 </details>
</form>
<form method="POST" action="/verify" data-msg-deny="{{.MsgDeny}}">
 <input type="hidden" name="user_code" value="{{.Auth.UserCode}}">
 <input type="hidden" name="signature" value="">
{{if $.WalletEnabled}}
 <button type="button" class="wallet" data-action="deny">Deny with wallet</button>
{{end}}
{{if $.TokenEnabled}}
 <details>
  <summary>Admin token (break-glass)</summary>
  <input type="password" name="admin_token" placeholder="admin token">
  <button name="action" value="deny">Deny</button>
 </details>
{{end}}
</form>
<br>
{{else}}
<table>
 <tr><th>User code</th><td>{{.Auth.UserCode}}</td></tr>
 <tr><th>kind</th><td>{{.Auth.Kind}}</td></tr>
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
{{end}}
<h2>Machines</h2>
<table>
 <tr><th>mac</th><th>dns</th><th>role</th><th>lan ip</th><th>last config fetch</th></tr>
{{range .Rows}} <tr><td>{{.MAC}}</td><td>{{.DNS}}</td><td>{{.Role}}</td><td>{{.IP}}</td><td>{{.LastFetch}}</td></tr>
{{end}}</table>
{{if .Mesh}}
<h2>Mesh</h2>
{{if .MeshRows}}
<table>
 <tr><th>name</th><th>group</th><th>mesh ip</th><th>tunnel</th><th>wan endpoint</th><th>relaying via hub to</th></tr>
{{range .MeshRows}} <tr><td>{{.Name}}</td><td>{{.Group}}</td><td>{{.Addr}}</td><td>{{.Tunnel}}</td><td>{{.Endpoint}}</td><td>{{.Relays}}</td></tr>
{{end}}</table>
{{else}}<p>Membership appears after unseal.</p>{{end}}
{{end}}
</div>
<script>
(function () {
` + walletSignJS + `
  // Delegated, not per-node: the live region below is re-rendered by
  // the poller, and per-node listeners would die with their nodes.
  document.addEventListener('click', async function (ev) {
    var btn = ev.target.closest('button');
    if (!btn) return;
    var form = btn.closest('form');
    var msg, action = null;
    if (btn.id === 'unseal-wallet') {
      msg = form.dataset.msg;
    } else if (btn.classList.contains('mesh-approve')) {
      msg = meshEnrollMsg(form);
    } else if (btn.classList.contains('wallet') && btn.dataset.action) {
      action = btn.dataset.action;
      msg = action === 'approve' ? form.dataset.msgApprove : form.dataset.msgDeny;
    } else {
      return;
    }
    ev.preventDefault();
    try {
      var sig = await walletSign(msg);
      if (!sig) return;
      form.querySelector('input[name=signature]').value = sig;
      if (action) {
        var act = document.createElement('input');
        act.type = 'hidden'; act.name = 'action'; act.value = action;
        form.appendChild(act);
      }
      form.submit();
    } catch (e) { alert('signing failed: ' + (e.message || e)); }
  });

  // Mesh-enroll cards (ADR-0012): the wallet signs the v1 enrollment
  // message with the FINAL name/group the operator picks, so the
  // message is rebuilt from the form fields on every edit. The server
  // rebuilds the same message from the submitted values
  // (handleMeshEnrollApprove) — the two must agree byte for byte,
  // including the name normalization (trim + lowercase).
  function meshEnrollMsg(form) {
    var name = form.querySelector('input[name=name]').value.trim().toLowerCase();
    var checked = form.querySelector('input[name=group]:checked');
    var group = checked ? checked.value : '';
    return 'talos config-server mesh device enrollment v1\nname: ' + name +
           '\ngroup: ' + group + '\npubkey: ' + form.dataset.fp +
           '\nnonce: ' + form.dataset.nonce;
  }
  function updateEnroll() {
    document.querySelectorAll('form.mesh-enroll').forEach(function (f) {
      var pre = f.querySelector('pre.mesh-msg');
      if (pre) pre.textContent = meshEnrollMsg(f);
    });
  }
  ['input', 'change'].forEach(function (t) {
    document.addEventListener(t, function (ev) {
      if (ev.target.closest && ev.target.closest('form.mesh-enroll')) updateEnroll();
    });
  });
  updateEnroll();

  // Soft refresh: re-fetch the page and swap only #live, so an update
  // never wipes a half-typed signature. Backs off while the operator
  // is interacting (focus in the region, text in any input, an open
  // details) and falls back to a full reload once the session expires.
  function busy() {
    var live = document.getElementById('live');
    if (document.activeElement !== document.body && live.contains(document.activeElement)) return true;
    if (live.querySelector('details[open]')) return true;
    var inputs = live.querySelectorAll('input[type=text], input[type=password]');
    // Compare against defaultValue, not '': mesh-enroll cards prefill
    // the name field, and an untouched prefill is not interaction.
    for (var i = 0; i < inputs.length; i++) if (inputs[i].value !== inputs[i].defaultValue) return true;
    return false;
  }
  setInterval(async function () {
    if (busy()) return;
    try {
      var resp = await fetch('/status');
      if (!resp.ok) return;
      var doc = new DOMParser().parseFromString(await resp.text(), 'text/html');
      var next = doc.getElementById('live');
      if (!next) { location.reload(); return; }
      if (busy()) return;
      var cur = document.getElementById('live');
      if (cur.innerHTML !== next.innerHTML) { cur.innerHTML = next.innerHTML; updateEnroll(); }
    } catch (e) { /* transient; try again next tick */ }
  }, 10000);
})();
</script>
</body></html>`))

// statusRow is one machines-table line. Live overlay state (tunnel
// up/down, endpoint, relays) lives in the mesh table — the hostmap
// join in nebseal.go — so this row only carries what is knowable from
// the repo plus the fetch log.
type statusRow struct {
	MAC, DNS, Role, IP string
	LastFetch          string
}

type statusData struct {
	Addr          string
	Message       string
	Version       string
	Started       string
	Seal          string
	Sealed        bool
	Mesh          string // mesh seal-state line ("" = mesh disabled)
	MeshWarn      bool
	MeshRows      []mesh.MemberRow
	Boot          *bootSnapshot
	Pending       []verifyEntry
	UndeclaredKMS []string
	Rows          []statusRow
	WalletEnabled bool
	TokenEnabled  bool
	MasterMessage string
}

func (s *server) renderStatus(w http.ResponseWriter, addr, msg string) {
	byMAC, err := machines.Load(filepath.Join(s.root, "machines"))
	if err != nil {
		log.Printf("status: loading machines: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	sealed := false
	seal := "hub disabled"
	if s.hub != nil {
		if s.hub.sealed() {
			sealed = true
			seal = "SEALED — config serving paused; unseal above"
		} else {
			seal = "unsealed"
		}
	}

	now := time.Now()
	var rows []statusRow
	for _, mac := range slices.Sorted(maps.Keys(byMAC)) {
		m := byMAC[mac]
		row := statusRow{
			MAC:       mac,
			DNS:       "—",
			Role:      strings.TrimSuffix(filepath.Base(m.Config), filepath.Ext(m.Config)),
			IP:        m.IP,
			LastFetch: "never",
		}
		// DNS names are derived from meta.yaml + the configured zone,
		// so they are known even while the hub is sealed.
		if nm := s.mesh(); nm != nil && nm.DNSZone() != "" {
			row.DNS = mesh.MachineDNSName(mac, m) + "." + nm.DNSZone()
		}
		if t, ok := s.lastFetch(mac); ok {
			row.LastFetch = ago(now, t)
		}
		rows = append(rows, row)
	}

	data := statusData{
		Addr:          addr,
		Message:       msg,
		Version:       cmp.Or(os.Getenv("FLY_IMAGE_REF"), "dev"),
		Seal:          seal,
		Sealed:        sealed,
		Rows:          rows,
		WalletEnabled: len(s.adminAddrs) > 0,
		TokenEnabled:  s.adminToken != "",
		MasterMessage: masterderive.MasterMessage,
	}
	if nm := s.mesh(); nm != nil {
		svc, _, meshErr := nm.State()
		switch {
		case svc != nil:
			data.Mesh = "up — lighthouse+relay, endpoint " + nm.Endpoint()
		case meshErr != nil:
			data.Mesh, data.MeshWarn = "DOWN — "+meshErr.Error(), true
		case data.Sealed:
			data.Mesh = "sealed"
		default:
			data.Mesh = "down"
		}
		data.MeshRows = nm.Members()
	}
	if !s.started.IsZero() {
		data.Started = s.started.UTC().Format("2006-01-02 15:04 MST")
	}
	if s.boot != nil {
		snap := s.boot.status()
		data.Boot = &snap
	}
	for _, da := range s.store.Pending() {
		if da.Kind == deviceflow.KindMeshEnroll {
			// No MsgApprove: approval IS the wallet signature over the v1
			// enrollment message, rebuilt live on the card. Deny still
			// goes through /verify with the generic deny message.
			data.Pending = append(data.Pending, verifyEntry{
				Auth:    da,
				MsgDeny: approvalMessage("deny", da.UserCode, da.Nonce),
				Mesh:    true,
			})
			continue
		}
		data.Pending = append(data.Pending, verifyEntry{
			Auth:       da,
			MsgApprove: approvalMessage("approve", da.UserCode, da.Nonce),
			MsgDeny:    approvalMessage("deny", da.UserCode, da.Nonce),
		})
	}
	if s.kms != nil {
		data.UndeclaredKMS = s.kms.undeclaredSealed()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statusTemplate.Execute(w, data); err != nil {
		log.Printf("rendering status page: %v", err)
	}
}

// ago renders a compact relative time.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm ago", int(d.Hours()), int(d.Minutes())%60)
	default:
		return t.Format("2006-01-02 15:04")
	}
}
