package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/slackhq/nebula/cert"
)

// tvStart POSTs the name form and extracts the device code the ticket
// page embeds for its poller.
func tvStart(t *testing.T, base, name string) (*http.Response, string) {
	t.Helper()
	resp, err := http.PostForm(base+"/mesh/tv", url.Values{"name": {name}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// tvExtract pulls a JS string literal assigned near a marker out of the
// ticket page. Templates render Go strings as quoted JS, so the value
// sits between quotes after the marker.
func tvExtract(t *testing.T, page, marker string) string {
	t.Helper()
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("ticket page does not contain %q", marker)
	}
	rest := page[i+len(marker):]
	start := strings.IndexByte(rest, '"')
	if start < 0 {
		t.Fatalf("no quoted value after %q", marker)
	}
	end := strings.IndexByte(rest[start+1:], '"')
	if end < 0 {
		t.Fatalf("unterminated value after %q", marker)
	}
	return rest[start+1 : start+1+end]
}

// pollToken exercises POST /token like the ticket page's JS does.
func pollToken(t *testing.T, base, deviceCode string) (token, errCode string) {
	t.Helper()
	resp, err := http.PostForm(base+"/token", url.Values{
		"grant_type":  {deviceCodeGrantType},
		"device_code": {deviceCode},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.AccessToken, out.Error
}

// fetchTVConfig redeems a bearer token at /mesh/tv/config.
func fetchTVConfig(t *testing.T, base, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/mesh/tv/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// TestMeshTVFlow is the happy path: start a flow for the media device,
// approve it store-side (the wallet-signature path is covered by the
// /verify tests), poll a token, redeem it for a config whose cert is
// the device's derived media-group identity.
func TestMeshTVFlow(t *testing.T) {
	s, ts := newMeshEnrollServer(t)

	resp, page := tvStart(t, ts.URL, "androidtv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start = %d: %s", resp.StatusCode, page)
	}
	deviceCode := tvExtract(t, page, "device_code:")
	if !strings.Contains(page, "/status?user_code=") {
		t.Fatal("ticket page does not point the QR at the /status approval page")
	}
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Fatal("ticket page has no inline QR image")
	}

	// Only one pending flow, and it is ours, kind tv.
	pending := s.store.pending()
	if len(pending) != 1 || pending[0].Kind != authKindTV {
		t.Fatalf("pending = %+v, want one tv flow", pending)
	}
	userCode := pending[0].UserCode
	if !strings.Contains(page, userCode) {
		t.Fatal("ticket page does not show the user code")
	}

	if _, errCode := pollToken(t, ts.URL, deviceCode); errCode != errAuthorizationPending {
		t.Fatalf("pre-approval poll error = %q, want %q", errCode, errAuthorizationPending)
	}
	if err := s.store.approve(userCode); err != nil {
		t.Fatal(err)
	}
	s.store.byDeviceCode[deviceCode].lastPoll = s.store.now().Add(-2 * pollInterval)
	token, errCode := pollToken(t, ts.URL, deviceCode)
	if token == "" {
		t.Fatalf("post-approval poll error = %q, want a token", errCode)
	}

	code, body := fetchTVConfig(t, ts.URL, token)
	if code != http.StatusOK {
		t.Fatalf("config fetch = %d: %s", code, body)
	}
	var cfg nebConfigYAML
	if err := yaml.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatal(err)
	}
	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(cfg.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if crt.Name() != "androidtv" {
		t.Fatalf("cert name = %q, want androidtv", crt.Name())
	}
	if g := crt.Groups(); len(g) != 1 || g[0] != nebGroupMedia {
		t.Fatalf("cert groups = %v, want [%s]", g, nebGroupMedia)
	}

	// Single-use: the same token must not serve twice.
	if code, _ := fetchTVConfig(t, ts.URL, token); code != http.StatusForbidden {
		t.Fatalf("second redemption = %d, want 403", code)
	}
}

// TestMeshTVRefusesNonMedia: an admins-group device must not get a
// ticket — the QR flow may never be a path to an admins cert — and
// undeclared names 404.
func TestMeshTVRefusesNonMedia(t *testing.T) {
	_, ts := newMeshEnrollServer(t)

	resp, body := tvStart(t, ts.URL, "laptop")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admins device start = %d (%s), want 403", resp.StatusCode, body)
	}
	resp, _ = tvStart(t, ts.URL, "toaster")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("undeclared device start = %d, want 404", resp.StatusCode)
	}
}

// TestMeshTVKindSeparation: a machine token must not redeem a TV
// config, and a TV token must not fetch a machine config — the grants
// carry different kinds and each endpoint requires its own.
func TestMeshTVKindSeparation(t *testing.T) {
	s, ts := newMeshEnrollServer(t)

	// Machine-kind token (as minted for a Talos config fetch), even one
	// that smuggles a mesh_device identity key from the untrusted form.
	da := s.store.begin(authKindMachine, "talos-pxe", map[string]string{"mesh_device": "androidtv"})
	if err := s.store.approve(da.UserCode); err != nil {
		t.Fatal(err)
	}
	machineToken, errCode := s.store.poll(da.DeviceCode)
	if machineToken == "" {
		t.Fatalf("poll error = %q", errCode)
	}
	if code, _ := fetchTVConfig(t, ts.URL, machineToken); code != http.StatusForbidden {
		t.Fatalf("machine token on /mesh/tv/config = %d, want 403", code)
	}

	// TV-kind token against the machine config validator.
	da = s.store.begin(authKindTV, nebTVClientID, map[string]string{"mesh_device": "androidtv"})
	if err := s.store.approve(da.UserCode); err != nil {
		t.Fatal(err)
	}
	tvToken, errCode := s.store.poll(da.DeviceCode)
	if tvToken == "" {
		t.Fatalf("poll error = %q", errCode)
	}
	if err := s.store.validate(tvToken, "b0-41-6f-15-3b-8f"); err == nil {
		t.Fatal("tv token validated for a machine config fetch")
	}
}

// TestMeshBlocklistPropagates: a fingerprint in mesh-blocklist.txt must
// appear in pki.blocklist of the node config, the device config, and
// the hub config; a malformed entry must refuse to load.
func TestMeshBlocklistPropagates(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	fp := strings.Repeat("ab", 32)
	blockfile := filepath.Join(s.root, nebBlocklistFile)
	if err := os.WriteFile(blockfile, []byte("# revoked\n"+fp+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Device config (via the enrollment exchange).
	code, body := meshEnroll(t, ts.URL, "androidtv")
	if code != http.StatusOK {
		t.Fatalf("enroll = %d: %s", code, body)
	}
	var cfg nebConfigYAML
	if err := yaml.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.PKI.Blocklist) != 1 || cfg.PKI.Blocklist[0] != fp {
		t.Fatalf("device blocklist = %v, want [%s]", cfg.PKI.Blocklist, fp)
	}

	// Node config (via the compose-time patch).
	mesh := s.hub.mesh
	master := s.hub.current()
	machines, err := loadMachines(mesh.machinesDir())
	if err != nil {
		t.Fatal(err)
	}
	for mac, m := range machines {
		patch, err := mesh.nebMachinePatch(master, mac, m, machines)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(patch, fp) {
			t.Fatalf("node patch for %s lacks the blocklisted fingerprint", mac)
		}
	}

	// Hub config.
	blocklist, err := loadMeshBlocklist(s.root)
	if err != nil {
		t.Fatal(err)
	}
	hubCfg, err := hubNebulaConfig(nebHubParams{
		master: master, subnet: mesh.subnet,
		listenHost: "0.0.0.0", listenPort: 4242, blocklist: blocklist,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hubCfg), fp) {
		t.Fatal("hub config lacks the blocklisted fingerprint")
	}

	// Malformed entry: fail loudly, not silently skip.
	if err := os.WriteFile(blockfile, []byte("not-a-fingerprint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMeshBlocklist(s.root); err == nil {
		t.Fatal("malformed blocklist entry loaded without error")
	}
}
