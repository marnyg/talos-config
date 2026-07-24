package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// testWGManager returns a sealed manager with a stubbed WG start and a
// minimal machines tree.
func testWGManager(t *testing.T, adminAddrs []string, pinnedPub string) *wgManager {
	t.Helper()
	root := t.TempDir()
	machineDir := filepath.Join(root, "machines", "aa-bb-cc-dd-ee-ff")
	if err := os.MkdirAll(machineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := "ip: 127.0.0.1\nconfig: base.yaml\npatches: []\n"
	if err := os.WriteFile(filepath.Join(machineDir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	base := "version: v1alpha1\nmachine:\n  type: worker\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newWGManager(51820, netip.MustParsePrefix("10.99.0.1/24"), "203.0.113.7:51820", pinnedPub, root, adminAddrs)
	m.start = func([32]byte, int, netip.Addr, []wgPeer) error { return nil } // no real socket
	return m
}

// unsealSig produces the well-known test key's signature over the
// master message.
func unsealSig(t *testing.T) string {
	t.Helper()
	return personalSign(t, testKey(t), wgderive.MasterMessage)
}

func TestUnsealWithSignature(t *testing.T) {
	m := testWGManager(t, []string{wellKnownAddr}, "")
	if !m.sealed() {
		t.Fatal("manager should start sealed")
	}
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	if m.sealed() {
		t.Fatal("still sealed after valid unseal")
	}

	// The derived master must match deriving directly from the sig.
	master, err := wgderive.MasterFromSignatureHex(unsealSig(t))
	if err != nil {
		t.Fatal(err)
	}
	wantPub := wgderive.PublicKey(wgderive.ServerKey(master))
	if m.current().serverPub != wantPub {
		t.Error("server pubkey does not match signature-derived master")
	}

	// Idempotent re-unseal.
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatalf("re-unseal should be a no-op, got: %v", err)
	}
}

func TestUnsealRejectsUnknownWallet(t *testing.T) {
	m := testWGManager(t, []string{"0x0000000000000000000000000000000000000001"}, "")
	if err := m.unsealWithSignature(unsealSig(t)); err == nil {
		t.Fatal("expected rejection for non-allowlisted wallet")
	}
	if !m.sealed() {
		t.Fatal("must remain sealed after rejected unseal")
	}
}

func TestUnsealRejectsGarbageSignature(t *testing.T) {
	m := testWGManager(t, []string{wellKnownAddr}, "")
	for _, sig := range []string{"", "0xdeadbeef", "not-hex"} {
		if err := m.unsealWithSignature(sig); err == nil {
			t.Errorf("expected rejection for signature %q", sig)
		}
	}
}

func TestUnsealPinnedPubkey(t *testing.T) {
	// Correct pin: compute from the signature, then unseal.
	master, err := wgderive.MasterFromSignatureHex(unsealSig(t))
	if err != nil {
		t.Fatal(err)
	}
	pin := wgderive.KeyBase64(wgderive.PublicKey(wgderive.ServerKey(master)))

	m := testWGManager(t, []string{wellKnownAddr}, pin)
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatalf("unseal with correct pin: %v", err)
	}

	// Wrong pin: must fail and stay sealed.
	m = testWGManager(t, []string{wellKnownAddr}, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err := m.unsealWithSignature(unsealSig(t)); err == nil {
		t.Fatal("expected pin mismatch error")
	}
	if !m.sealed() {
		t.Fatal("must remain sealed after pin mismatch")
	}
}

func TestConfigRefusedWhileSealed(t *testing.T) {
	m := testWGManager(t, []string{wellKnownAddr}, "")
	s := &server{root: m.root, store: newAuthStore(), wgm: m, adminAddrs: m.adminAddrs}

	req := httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil)
	rec := httptest.NewRecorder()
	s.handleConfig(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sealed /config: got %d, want 503", rec.Code)
	}

	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s.handleConfig(rec, httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unsealed /config: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "wg0") {
		t.Error("unsealed config missing injected wg0 interface")
	}
}

func TestUnsealEndpoint(t *testing.T) {
	m := testWGManager(t, []string{wellKnownAddr}, "")
	s := &server{root: m.root, store: newAuthStore(), wgm: m, adminAddrs: m.adminAddrs}

	// Sealed status endpoint.
	rec := httptest.NewRecorder()
	s.handleSealed(rec, httptest.NewRequest("GET", "/sealed", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/sealed while sealed: got %d, want 503", rec.Code)
	}

	// Bad signature over HTTP.
	form := url.Values{"signature": {"0xdeadbeef"}}
	req := httptest.NewRequest("POST", "/unseal", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	s.handleUnseal(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad unseal: got %d, want 403", rec.Code)
	}

	// Valid signature over HTTP.
	form = url.Values{"signature": {unsealSig(t)}}
	req = httptest.NewRequest("POST", "/unseal", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	s.handleUnseal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid unseal: got %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleSealed(rec, httptest.NewRequest("GET", "/sealed", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/sealed after unseal: got %d, want 200", rec.Code)
	}
}
