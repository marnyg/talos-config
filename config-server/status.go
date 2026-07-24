package main

// Read-only /status page behind a SIWE session login. Zero information
// is rendered before an allowlisted wallet signs the login nonce: the
// logged-out page is only the sign-in prompt. Shows per-machine tunnel
// liveness (WG handshake/traffic), last config fetch, auto-bootstrap
// state, and seal state.

import (
	"cmp"
	"fmt"
	"html/template"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

const sessionCookieName = "talos_status_session"

// statusEnabled: the page only exists when a wallet allowlist is
// configured — there is no other way in, and without one the page
// must leak nothing (404, same as any unknown path).
func (s *server) statusEnabled() bool {
	return len(s.adminAddrs) > 0 && s.sessions != nil
}

// sessionAddr resolves the request's session cookie to a wallet address.
func (s *server) sessionAddr(r *http.Request) (string, bool) {
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
		s.renderStatus(w, addr)
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
	nonce, sig := r.FormValue("nonce"), r.FormValue("signature")
	if nonce == "" || sig == "" {
		http.Error(w, "missing nonce or signature", http.StatusBadRequest)
		return
	}
	if !s.sessions.redeemNonce(nonce) {
		s.renderLogin(w, "login challenge expired or already used — try again")
		return
	}
	addr, err := recoverPersonalSign(loginMessage(nonce), sig)
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

	token := s.sessions.create(addr)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/status",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(externalBase(r), "https://"),
	})
	log.Printf("status login: wallet %s signed in", addr)
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
		Name: sessionCookieName, Value: "", Path: "/status", MaxAge: -1, HttpOnly: true,
	})
	http.Redirect(w, r, "/status", http.StatusSeeOther)
}

const statusStyle = `
 body { font-family: monospace; max-width: 60rem; margin: 2rem auto; }
 table { border-collapse: collapse; width: 100%; margin-bottom: 1rem; }
 td, th { border: 1px solid #999; padding: .4rem .6rem; text-align: left; }
 .msg { padding: .5rem; border: 1px solid #999; margin-bottom: 1rem; }
 .warn { border-color: #c00; }
 pre { background: #f4f4f4; padding: .5rem; overflow-x: auto; }
 details { margin: .5rem 0; }
 form.inline { display: inline; }
`

var loginTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html>
<head><title>Status</title><style>` + statusStyle + `</style></head>
<body>
<h1>Status</h1>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
<p>Sign in with an admin wallet to view cluster status.</p>
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
<script>
(function () {
  var btn = document.getElementById('login-wallet');
  btn.addEventListener('click', async function () {
    if (!window.ethereum) { alert('no wallet found'); return; }
    try {
      var accounts = await ethereum.request({ method: 'eth_requestAccounts' });
      var sig = await ethereum.request({ method: 'personal_sign', params: [{{.Msg}}, accounts[0]] });
      var form = document.getElementById('login-form');
      form.querySelector('input[name=signature]').value = sig;
      form.submit();
    } catch (e) { alert('signing failed: ' + (e.message || e)); }
  });
})();
</script>
</body></html>`))

func (s *server) renderLogin(w http.ResponseWriter, msg string) {
	nonce := s.sessions.issueNonce()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := loginTemplate.Execute(w, map[string]string{
		"Nonce":   nonce,
		"Msg":     loginMessage(nonce),
		"Message": msg,
	})
	if err != nil {
		log.Printf("rendering login page: %v", err)
	}
}

var statusTemplate = template.Must(template.New("status").Parse(`<!DOCTYPE html>
<html>
<head><title>Cluster status</title>
<meta http-equiv="refresh" content="30">
<style>` + statusStyle + `</style></head>
<body>
<h1>Cluster status</h1>
<p>signed in as {{.Addr}}
 <form class="inline" method="POST" action="/status/logout"><button>sign out</button></form>
</p>
<table>
 <tr><th>server</th><td>{{.Version}}{{if .Started}} — up since {{.Started}}{{end}}</td></tr>
 <tr><th>control channel</th><td{{if .Sealed}} class="warn"{{end}}>{{.Seal}}</td></tr>
 {{with .Boot}}
 <tr><th>auto-bootstrap</th><td>{{.State}}{{if .Target}} — target {{.Target}} ({{.TunnelIP}}){{end}}{{if .Done}} — cluster bootstrapped, idle{{else if .Attempted}} — Bootstrap called, watching etcd{{end}}{{if .LastErr}} — last error: {{.LastErr}}{{end}}</td></tr>
 {{else}}
 <tr><th>auto-bootstrap</th><td>disabled</td></tr>
 {{end}}
 <tr><th>pending approvals</th><td>{{if .Pending}}{{range .Pending}}{{.}} {{end}}— <a href="/verify">review</a>{{else}}none{{end}}</td></tr>
</table>
{{range .UndeclaredKMS}}
<div class="msg warn">machine sealed disk keys under UNDECLARED uuid <code>{{.}}</code> —
add <code>uuid: {{.}}</code> to its machines/&lt;mac&gt;/meta.yaml before the next
server restart or it will not be able to unlock its disks.</div>
{{end}}
<h2>Machines</h2>
<table>
 <tr><th>mac</th><th>role</th><th>lan ip</th><th>tunnel ip</th><th>handshake</th><th>rx</th><th>tx</th><th>wan endpoint</th><th>last config fetch</th></tr>
{{range .Rows}} <tr><td>{{.MAC}}</td><td>{{.Role}}</td><td>{{.IP}}</td><td>{{.TunnelIP}}</td><td>{{.Handshake}}</td><td>{{.Rx}}</td><td>{{.Tx}}</td><td>{{.Endpoint}}</td><td>{{.LastFetch}}</td></tr>
{{end}}</table>
</body></html>`))

type statusRow struct {
	MAC, Role, IP, TunnelIP     string
	Handshake, Rx, Tx, Endpoint string
	LastFetch                   string
}

type statusData struct {
	Addr          string
	Version       string
	Started       string
	Seal          string
	Sealed        bool
	Boot          *bootSnapshot
	Pending       []string
	UndeclaredKMS []string
	Rows          []statusRow
}

func (s *server) renderStatus(w http.ResponseWriter, addr string) {
	machines, err := loadMachines(filepath.Join(s.root, "machines"))
	if err != nil {
		log.Printf("status: loading machines: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var wg *wgSettings
	seal := "wireguard disabled"
	if s.wgm != nil {
		if wg = s.wgm.current(); wg == nil {
			seal = "SEALED — config serving paused; unseal at /verify"
		} else {
			seal = "unsealed — endpoint " + wg.endpoint
		}
	}

	var stats map[string]wgPeerStat
	if wg != nil {
		if stats, err = wg.peerStats(); err != nil {
			log.Printf("status: reading peer stats: %v", err)
		}
	}

	now := time.Now()
	var rows []statusRow
	for _, mac := range slices.Sorted(maps.Keys(machines)) {
		m := machines[mac]
		row := statusRow{
			MAC:       mac,
			Role:      strings.TrimSuffix(filepath.Base(m.Config), filepath.Ext(m.Config)),
			IP:        m.IP,
			TunnelIP:  "—",
			Handshake: "—",
			Rx:        "—",
			Tx:        "—",
			Endpoint:  "—",
			LastFetch: "never",
		}
		if wg != nil {
			if ip, err := wg.machineTunnelIP(mac, m); err == nil {
				row.TunnelIP = ip.String()
				pub := wgderive.KeyHex(wgderive.PublicKey(wgderive.MachineKey(wg.master, mac)))
				if st, ok := stats[pub]; ok {
					if st.lastHandshake.IsZero() {
						row.Handshake = "never"
					} else {
						row.Handshake = ago(now, st.lastHandshake)
					}
					row.Rx, row.Tx = fmtBytes(st.rxBytes), fmtBytes(st.txBytes)
					if st.endpoint != "" {
						row.Endpoint = st.endpoint
					}
				}
			}
		}
		if t, ok := s.lastFetch(mac); ok {
			row.LastFetch = ago(now, t)
		}
		rows = append(rows, row)
	}

	data := statusData{
		Addr:    addr,
		Version: cmp.Or(os.Getenv("FLY_IMAGE_REF"), "dev"),
		Seal:    seal,
		Sealed:  s.wgm != nil && wg == nil,
		Rows:    rows,
	}
	if !s.started.IsZero() {
		data.Started = s.started.UTC().Format("2006-01-02 15:04 MST")
	}
	if s.boot != nil {
		snap := s.boot.status()
		data.Boot = &snap
	}
	for _, da := range s.store.pending() {
		data.Pending = append(data.Pending, da.UserCode)
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

// fmtBytes renders a byte count with a binary unit.
func fmtBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
