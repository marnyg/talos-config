package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// testHubManager builds a sealed hub over a throwaway talos tree with
// one declared machine and a stub-started mesh (no real overlay).
func testHubManager(t *testing.T, adminAddrs []string, pinnedCAFP string) *hubManager {
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

	mesh, _ := testNebManager(t, root, []string{"laptop"})
	return newHubManager(root, adminAddrs, pinnedCAFP, mesh)
}

// unsealSig produces the well-known test key's signature over the
// master message.
func unsealSig(t *testing.T) string {
	t.Helper()
	return personalSign(t, testKey(t), masterderive.MasterMessage)
}

func TestUnsealWithSignature(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	if !m.sealed() {
		t.Fatal("manager should start sealed")
	}
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	if m.sealed() {
		t.Fatal("still sealed after valid unseal")
	}

	// The held master must match deriving directly from the sig.
	master, err := masterderive.MasterFromSignatureHex(unsealSig(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(m.current()) != string(master) {
		t.Error("held master does not match signature-derived master")
	}

	// The mesh must have been fanned out to.
	if !strings.Contains(string(*meshRendered(t, m)), "pki") {
		t.Error("mesh config was not rendered at unseal")
	}

	// Idempotent re-unseal.
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatalf("re-unseal should be a no-op, got: %v", err)
	}
}

// meshRendered digs the rendered mesh config out of the stub. Small
// helper so tests read as intent, not plumbing.
func meshRendered(t *testing.T, m *hubManager) *[]byte {
	t.Helper()
	// The stub in testNebManager captures into its closure; re-render
	// deterministically instead of reaching into it.
	master := m.current()
	if master == nil {
		t.Fatal("hub is sealed")
	}
	cfg, err := hubNebulaConfig(nebHubParams{
		master:     master,
		subnet:     m.mesh.subnet,
		listenHost: "0.0.0.0",
		listenPort: m.mesh.port,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &cfg
}

func TestUnsealRejectsUnknownWallet(t *testing.T) {
	m := testHubManager(t, []string{"0x0000000000000000000000000000000000000001"}, "")
	if err := m.unsealWithSignature(unsealSig(t)); err == nil {
		t.Fatal("expected rejection for non-allowlisted wallet")
	}
	if !m.sealed() {
		t.Fatal("must remain sealed after rejected unseal")
	}
}

func TestUnsealRejectsGarbageSignature(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	for _, sig := range []string{"", "0xdeadbeef", "not-hex"} {
		if err := m.unsealWithSignature(sig); err == nil {
			t.Errorf("expected rejection for signature %q", sig)
		}
	}
}

// TestUnsealPinnedCAFingerprint: the pin catches a wrong wallet (or a
// subtly different message) before anything derives from the bogus
// master — the phase-2 successor to the wg server-pubkey pin.
func TestUnsealPinnedCAFingerprint(t *testing.T) {
	// Correct pin: compute from the signature, then unseal.
	master, err := masterderive.MasterFromSignatureHex(unsealSig(t))
	if err != nil {
		t.Fatal(err)
	}
	pin, err := nebderive.CAFingerprint(master)
	if err != nil {
		t.Fatal(err)
	}

	m := testHubManager(t, []string{wellKnownAddr}, pin)
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatalf("unseal with correct pin: %v", err)
	}

	// Wrong pin: must fail and stay sealed.
	m = testHubManager(t, []string{wellKnownAddr}, strings.Repeat("00", 32))
	if err := m.unsealWithSignature(unsealSig(t)); err == nil {
		t.Fatal("expected pin mismatch error")
	}
	if !m.sealed() {
		t.Fatal("must remain sealed after pin mismatch")
	}
}

func TestConfigRefusedWhileSealed(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	s := &server{root: m.root, store: newAuthStore(), hub: m, adminAddrs: m.adminAddrs}

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
	if !strings.Contains(rec.Body.String(), "ExtensionServiceConfig") {
		t.Error("unsealed config missing injected mesh identity")
	}
	if strings.Contains(rec.Body.String(), "wg0") {
		t.Error("served config still injects wg0 — phase 2 removed it")
	}
}

func TestUnsealEndpoint(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	s := &server{root: m.root, store: newAuthStore(), hub: m, adminAddrs: m.adminAddrs}

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
