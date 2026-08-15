package mobile

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/config-server/devkey"
	"github.com/marnyg/talos-config/config-server/mesh"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// TestEnrollFlowAgainstMintedConfig walks the app's whole enrollment
// path — keygen, flow start, pending→ok polling, config redemption,
// key splice, VpnService parse — against a fake hub that serves a
// config minted by the real mesh.EnrollDevice. The HTTP handlers are
// stand-ins (the real ones live in package main and are e2e-tested
// there); what this pins is the client protocol and that the app can
// complete and parse a genuine hub artifact.
func TestEnrollFlowAgainstMintedConfig(t *testing.T) {
	master := []byte("mobile-enroll-test-master-32byte")
	subnet := netip.MustParsePrefix("10.42.0.0/16")

	// The device identity, as the app would create it.
	kpRaw, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var kp struct{ PrivHex, PubHex string }
	if err := json.Unmarshal([]byte(kpRaw), &kp); err != nil {
		t.Fatal(err)
	}
	priv, pub, err := devkey.ParsePrivHex(kp.PrivHex)
	if err != nil {
		t.Fatal(err)
	}
	_ = priv
	if got, _ := PubkeyFromPriv(kp.PrivHex); got != kp.PubHex {
		t.Fatalf("PubkeyFromPriv = %s, want %s", got, kp.PubHex)
	}

	// Mint the config the hub would stash on approval.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := mesh.NewManager(4242, subnet, "127.0.0.1", "hub.example:4242", "mesh.internal", root)
	minted, err := m.EnrollDevice(mesh.EnrollDeviceParams{
		Master: master, Name: "shield-tv", Group: mesh.GroupMedia, Pubkey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A fake hub: enough of the three endpoints to drive the client.
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mesh/enroll/device":
			if r.FormValue("pubkey") != kp.PubHex {
				t.Errorf("hub saw pubkey %q, want %q", r.FormValue("pubkey"), kp.PubHex)
			}
			if r.FormValue("proposed_name") != "shield-tv" || r.FormValue("proposed_group") != "media" {
				t.Errorf("proposals = (%q,%q)", r.FormValue("proposed_name"), r.FormValue("proposed_group"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_code":"dc-1","user_code":"WDGF-XKCD","verification_uri_complete":"https://hub/status?user_code=WDGF-XKCD","qr_png_base64":"","expires_in":600,"interval":5}`))
		case "/token":
			if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.FormValue("device_code") != "dc-1" {
				t.Errorf("token poll = %v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"tok-1","token_type":"Bearer","expires_in":60}`))
		case "/mesh/enroll/config":
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				t.Errorf("config fetch auth = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
			_, _ = w.Write(minted)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Start → poll (pending, then ok) → fetch+splice.
	startRaw, err := StartEnroll(srv.URL, kp.PrivHex, "shield-tv", "media")
	if err != nil {
		t.Fatal(err)
	}
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := json.Unmarshal([]byte(startRaw), &start); err != nil {
		t.Fatal(err)
	}
	if start.UserCode != "WDGF-XKCD" {
		t.Fatalf("user_code = %q", start.UserCode)
	}

	p1, err := PollEnroll(srv.URL, start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p1, `"status":"pending"`) {
		t.Fatalf("first poll = %s, want pending", p1)
	}
	p2, err := PollEnroll(srv.URL, start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	var poll struct{ Status, AccessToken string }
	if err := json.Unmarshal([]byte(p2), &poll); err != nil {
		t.Fatal(err)
	}
	if poll.Status != "ok" || poll.AccessToken != "tok-1" {
		t.Fatalf("second poll = %s", p2)
	}

	cfg, err := FetchConfig(srv.URL, poll.AccessToken, kp.PrivHex)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "-----BEGIN NEBULA X25519 PRIVATE KEY-----") {
		t.Error("fetched config is missing the spliced private key")
	}

	// The VpnService parse must recover the derived identity.
	infoRaw, err := ConfigInfo(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Name      string `json:"name"`
		OwnIP     string `json:"ownIP"`
		PrefixLen int    `json:"prefixLen"`
		HubIP     string `json:"hubIP"`
		MTU       int    `json:"mtu"`
	}
	if err := json.Unmarshal([]byte(infoRaw), &info); err != nil {
		t.Fatal(err)
	}
	wantIP, err := nebderive.DeviceIP(master, "shield-tv", subnet)
	if err != nil {
		t.Fatal(err)
	}
	hubIP, err := nebderive.HubIP(subnet)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "shield-tv" {
		t.Errorf("name = %q", info.Name)
	}
	if info.OwnIP != wantIP.String() {
		t.Errorf("ownIP = %s, want %s (derived)", info.OwnIP, wantIP)
	}
	if info.PrefixLen != subnet.Bits() {
		t.Errorf("prefixLen = %d, want %d", info.PrefixLen, subnet.Bits())
	}
	if info.HubIP != hubIP.String() {
		t.Errorf("hubIP = %s, want %s", info.HubIP, hubIP)
	}
	if info.MTU <= 0 {
		t.Errorf("mtu = %d, want > 0", info.MTU)
	}
}

// TestPollEnrollTerminalStates pins the denied/expired mappings the
// app's enroll screen branches on.
func TestPollEnrollTerminalStates(t *testing.T) {
	for oauthErr, wantStatus := range map[string]string{
		"access_denied": "denied",
		"expired_token": "expired",
		"slow_down":     "slow_down",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"` + oauthErr + `"}`))
		}))
		got, err := PollEnroll(srv.URL, "dc")
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", oauthErr, err)
		}
		if !strings.Contains(got, `"status":"`+wantStatus+`"`) {
			t.Errorf("%s → %s, want status %q", oauthErr, got, wantStatus)
		}
	}
}

// TestGenerateKeypairIsHexAndClamped sanity-checks the JSON crossing
// into Kotlin: both fields hex, private key clamped.
func TestGenerateKeypairIsHexAndClamped(t *testing.T) {
	raw, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var kp struct{ PrivHex, PubHex string }
	if err := json.Unmarshal([]byte(raw), &kp); err != nil {
		t.Fatal(err)
	}
	priv, err := hex.DecodeString(kp.PrivHex)
	if err != nil || len(priv) != 32 {
		t.Fatalf("privHex not 32-byte hex: %q", kp.PrivHex)
	}
	if pub, err := hex.DecodeString(kp.PubHex); err != nil || len(pub) != 32 {
		t.Fatalf("pubHex not 32-byte hex: %q", kp.PubHex)
	}
	if priv[0]&7 != 0 {
		t.Error("private key not clamped")
	}
}
