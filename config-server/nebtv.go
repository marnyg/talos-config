package main

// TV mesh onboarding: the RFC 8628 device flow pointed at a shared-space
// appliance instead of a Talos machine (thread uuid dba0c63d). The TV's
// browser starts a flow and shows a QR of the /status approval URL; the
// owner scans it with the wallet's in-app browser, signs in, and signs
// the same approval message a machine approval uses; the TV's page polls
// the token endpoint and swaps the single-use token for its nebula
// config over its own TLS session.
//
// Reuses the deviceflow package wholesale, which is the security
// argument in one line: the QR is a *pointer to the approval page*, not
// a bearer credential — worthless without an allowlisted wallet — and
// the token it eventually mints is single-use, ten-minute, and bound
// server-side to one declared media device (deviceflow.KindTV).
//
// Media group only, by construction: the flow refuses to start for an
// admins-group name, and refuses again at redemption in case the lists
// changed in between. An admin device enrolls with nebup, where the
// wallet signs for that specific device name — this page must never
// become a lower-friction path to an admins cert.

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net/http"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/mesh"
)

// nebTVClientID labels TV enrollment flows in logs and on the approval
// dashboard. Not a secret (RFC 8628 public client, same as Talos).
const nebTVClientID = "mesh-tv"

// handleMeshTVPage (GET /mesh/tv) renders the name form. It reveals
// nothing: no device list, no pending flows — the visitor types the
// name the owner told them, and unknown names 404 on POST.
func (s *server) handleMeshTVPage(w http.ResponseWriter, r *http.Request) {
	if s.mesh() == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tvFormTemplate.Execute(w, nil); err != nil {
		log.Printf("rendering tv form: %v", err)
	}
}

// handleMeshTVStart (POST /mesh/tv: name) begins a device flow for a
// declared media device and renders the ticket page (user code, QR,
// poller). POST rather than GET so crawlers cannot litter the approval
// dashboard with pending flows.
func (s *server) handleMeshTVStart(w http.ResponseWriter, r *http.Request) {
	nm := s.mesh()
	if nm == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d, ok := nm.Device(r.FormValue("name"))
	if !ok {
		http.Error(w, "unknown device name (declare it in MESH_MEDIA_DEVICES)", http.StatusNotFound)
		return
	}
	if d.Group != mesh.GroupMedia {
		// Admin devices enroll via nebup, where the wallet signs for the
		// device name itself. Refusing here keeps this page from ever
		// minting an admins cert off a scanned QR.
		http.Error(w, "not a media device — admin devices enroll with nebup", http.StatusForbidden)
		return
	}
	if s.hub.sealed() {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}

	da := s.store.Begin(deviceflow.KindTV, nebTVClientID, map[string]string{"mesh_device": d.Name})
	approveURL := externalBase(r) + "/status?user_code=" + da.UserCode

	png, err := qrcode.Encode(approveURL, qrcode.Medium, 256)
	if err != nil {
		log.Printf("tv enroll %q: qr encode: %v", d.Name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("tv enroll started: device=%q user_code=%s", d.Name, da.UserCode)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = tvTicketTemplate.Execute(w, map[string]any{
		"Name":       d.Name,
		"UserCode":   da.UserCode,
		"DeviceCode": da.DeviceCode,
		"ApproveURL": approveURL,
		"QRDataURI":  template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png)),
		"Interval":   int(deviceflow.PollInterval.Seconds()),
		"ExpiresIn":  int(deviceflow.AuthTTL.Seconds()),
	})
	if err != nil {
		log.Printf("rendering tv ticket: %v", err)
	}
}

// handleMeshTVConfig (GET /mesh/tv/config, Bearer token) redeems an
// approved TV token for the device's self-contained nebula config. The
// token is consumed only after a successful serve, mirroring /config.
func (s *server) handleMeshTVConfig(w http.ResponseWriter, r *http.Request) {
	nm := s.mesh()
	if nm == nil {
		http.NotFound(w, r)
		return
	}
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	name, err := s.store.MeshDeviceFor(token)
	if err != nil {
		log.Printf("tv config: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	d, ok := nm.Device(name)
	if !ok || d.Group != mesh.GroupMedia {
		// The declared lists changed between approval and redemption.
		log.Printf("tv config: %q is no longer a declared media device", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	master := s.hub.current()
	if master == nil {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}
	cfg, err := nm.DeviceConfig(master, d)
	if err != nil {
		log.Printf("tv config %q: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.store.Consume(token)
	log.Printf("tv enroll: served mesh config for %q (group %s)", d.Name, d.Group)
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.Name+".yml"))
	_, _ = w.Write(cfg)
}

var tvFormTemplate = template.Must(template.New("tvform").Parse(`<!DOCTYPE html>
<html>
<head><title>Join the mesh</title><style>` + statusStyle + tvStyle + `</style></head>
<body>
<h1>Join the mesh</h1>
<p>Enter this device's name. The owner declared it, and the owner's
wallet approves the request — nothing is granted from this page alone.</p>
<form method="POST" action="/mesh/tv">
 <input type="text" name="name" placeholder="device name" autofocus>
 <button>Request to join</button>
</form>
</body></html>`))

var tvTicketTemplate = template.Must(template.New("tvticket").Parse(`<!DOCTYPE html>
<html>
<head><title>Approve {{.Name}}</title><style>` + statusStyle + tvStyle + `</style></head>
<body>
<h1>Approve “{{.Name}}”</h1>
<p>Scan with the wallet app’s browser, sign in, and approve code</p>
<div class="usercode">{{.UserCode}}</div>
<p><img class="qr" src="{{.QRDataURI}}" alt="QR to approval page" width="256" height="256"></p>
<p class="mono">{{.ApproveURL}}</p>
<div class="msg" id="state">Waiting for approval… this page checks every {{.Interval}} seconds
(request expires in {{.ExpiresIn}} seconds).</div>
<script>
(function () {
  var state = document.getElementById('state');
  var timer = setInterval(poll, {{.Interval}} * 1000);
  async function poll() {
    var body = new URLSearchParams({
      grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
      device_code: {{.DeviceCode}}
    });
    var resp, data;
    try {
      resp = await fetch('/token', { method: 'POST', body: body });
      data = await resp.json();
    } catch (e) { return; /* transient; keep polling */ }
    if (data.access_token) { clearInterval(timer); fetchConfig(data.access_token); return; }
    if (data.error === 'authorization_pending' || data.error === 'slow_down') return;
    clearInterval(timer);
    state.className = 'msg warn';
    state.textContent = data.error === 'access_denied' ? 'Denied by the owner.'
      : data.error === 'expired_token' ? 'Request expired — reload to try again.'
      : 'Failed: ' + data.error + ' — reload to try again.';
  }
  async function fetchConfig(token) {
    var resp = await fetch('/mesh/tv/config', { headers: { 'Authorization': 'Bearer ' + token } });
    if (!resp.ok) {
      state.className = 'msg warn';
      state.textContent = 'Approved, but fetching the config failed (' + resp.status + ') — reload to try again.';
      return;
    }
    var blob = await resp.blob();
    var a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = {{.Name}} + '.yml';
    document.body.appendChild(a);
    a.click();
    state.textContent = 'Approved — config downloaded. Import it in the Nebula app.';
  }
})();
</script>
</body></html>`))

// tvStyle sizes the user code and QR for a screen read across a room.
const tvStyle = `
 .usercode {
   font-size: 3rem; letter-spacing: .18em; font-weight: 700;
   margin: .5rem 0 1rem;
 }
 .qr { border: 8px solid #fff; border-radius: 4px; }
 .mono { color: var(--muted); word-break: break-all; }
`
