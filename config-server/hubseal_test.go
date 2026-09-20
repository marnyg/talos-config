package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/cert"
)

// testHubManager builds a sealed hub over a throwaway talos tree with
// one declared machine; the hubkey lives on the in-process network
// only (no iroh wire).
func testHubManager(t *testing.T, adminAddrs []string) *hubManager {
	t.Helper()
	return testHubManagerOn(t, adminAddrs, nil)
}

// testHubManagerOn is testHubManager with a second wire for the hubkey
// (hubiroh_test.go binds it on iroh). The v3 recipe and blocklist ride
// along so #bundle compiles against the repo's real files.
func testHubManagerOn(t *testing.T, adminAddrs []string, wan hubTransport) *hubManager {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{policy.File, policy.BlocklistFile} {
		b, err := os.ReadFile(filepath.Join("..", "talos", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	machineDir := filepath.Join(root, "machines", "aa-bb-cc-dd-ee-ff")
	if err := os.MkdirAll(machineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := "config: base.yaml\npatches: []\n"
	if err := os.WriteFile(filepath.Join(machineDir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	base := "version: v1alpha1\nmachine:\n  type: worker\n  certSANs:\n    - 10.0.0.20\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := newHubManager(root, adminAddrs, wan)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// unsealSig produces the well-known test key's signature over the
// master message.
func unsealSig(t *testing.T) string {
	t.Helper()
	return personalSign(t, testKey(t), masterderive.MasterMessage)
}

// speakAsSig is the second unseal signature (ce8): priv's EIP-191
// signature over m's speak-as proposal addressed to priv's own wallet.
func speakAsSig(t *testing.T, m *hubManager, priv *secp256k1.PrivateKey) string {
	t.Helper()
	addr := cert.NewEthSigner(priv).ActorID()
	_, msg, err := m.issuer.Proposal(addr)
	if err != nil {
		t.Fatal(err)
	}
	return personalSign(t, priv, msg)
}

// otherKey is a valid wallet that is NOT in the admin allowlist.
func otherKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	b := make([]byte, 32)
	b[31] = 2
	return secp256k1.PrivKeyFromBytes(b)
}

// otherAddr is otherKey's lowercase 0x address.
func otherAddr(t *testing.T) string {
	t.Helper()
	return strings.TrimPrefix(string(cert.NewEthSigner(otherKey(t)).ActorID()), "eth:")
}

func TestNewHubManagerRejectsMalformedAdmin(t *testing.T) {
	if _, err := newHubManager(t.TempDir(), []string{"0xnope"}, nil); err == nil {
		t.Fatal("malformed admin address accepted")
	}
}

// TestUnsealIssuer: the identity plane seals and unseals independently
// of the master, and the speak-as must come from the wallet that
// unsealed the master when one did.
func TestUnsealIssuer(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr, otherAddr(t)})
	if line, warn := m.identityLine(); !warn || !strings.Contains(line, "SEALED") {
		t.Fatalf("fresh identity line: %q warn=%v", line, warn)
	}

	// A non-admin wallet's signature over its own proposal is refused.
	stranger := func() *secp256k1.PrivateKey {
		b := make([]byte, 32)
		b[31] = 3
		return secp256k1.PrivKeyFromBytes(b)
	}()
	if _, err := m.unsealIssuer(speakAsSig(t, m, stranger)); err == nil {
		t.Fatal("stranger unsealed the identity")
	}

	// Master by the well-known wallet, then the OTHER admin tries the
	// speak-as: two roots, refused (ce8).
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	if m.masterWallet() != wellKnownAddr {
		t.Fatalf("master wallet %q", m.masterWallet())
	}
	if _, err := m.unsealIssuer(speakAsSig(t, m, otherKey(t))); err == nil || !strings.Contains(err.Error(), wellKnownAddr) {
		t.Fatalf("other admin's speak-as after well-known's master: %v", err)
	}
	if m.issuer.Serving() == nil {
		t.Fatal("identity unsealed by the wrong wallet")
	}

	// Same wallet: accepted; the Issuer speaks for it with full runway.
	addr, err := m.unsealIssuer(speakAsSig(t, m, testKey(t)))
	if err != nil || addr != wellKnownAddr {
		t.Fatalf("unsealIssuer: %v (%s)", err, addr)
	}
	// Runway counts from the proposal's iat, which is fixed before the
	// unseal completes, so it is SpeakAsTTL minus however long the
	// ceremony took — never exactly SpeakAsTTL. A minute of slack keeps
	// this honest without pinning the clock.
	if r := m.issuer.Runway(); m.issuer.Wallet() != issuer.WalletID(wellKnownAddr) || r > issuer.SpeakAsTTL || r < issuer.SpeakAsTTL-60 {
		t.Fatalf("held speak-as: wallet %s runway %d (want ~%d)", m.issuer.Wallet(), r, issuer.SpeakAsTTL)
	}
	if line, warn := m.identityLine(); warn || !strings.Contains(line, wellKnownAddr) || !strings.Contains(line, "120 d left") {
		t.Fatalf("unsealed identity line: %q warn=%v", line, warn)
	}
}

// TestUnsealIssuerBeforeMaster: with the master from the dev env (no
// wallet), any admin may sign the speak-as; the order of the two
// signatures is free.
func TestUnsealIssuerBeforeMaster(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr, otherAddr(t)})
	if _, err := m.unsealIssuer(speakAsSig(t, m, otherKey(t))); err != nil {
		t.Fatalf("speak-as while master sealed: %v", err)
	}
	if m.sealed() != true || m.issuer.Serving() != nil {
		t.Fatal("identity unseal touched the master, or failed")
	}
}

func TestUnsealWithSignature(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr})
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

	// Idempotent re-unseal.
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatalf("re-unseal should be a no-op, got: %v", err)
	}
}

func TestUnsealRejectsUnknownWallet(t *testing.T) {
	m := testHubManager(t, []string{"0x0000000000000000000000000000000000000001"})
	if err := m.unsealWithSignature(unsealSig(t)); err == nil {
		t.Fatal("expected rejection for non-allowlisted wallet")
	}
	if !m.sealed() {
		t.Fatal("must remain sealed after rejected unseal")
	}
}

func TestUnsealRejectsGarbageSignature(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr})
	for _, sig := range []string{"", "0xdeadbeef", "not-hex"} {
		if err := m.unsealWithSignature(sig); err == nil {
			t.Errorf("expected rejection for signature %q", sig)
		}
	}
}

func TestConfigRefusedWhileSealed(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr})
	m.publicURL = "https://hub.example"
	s := &server{root: m.root, store: deviceflow.NewStore(), hub: m, adminAddrs: m.adminAddrs}

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
		t.Error("unsealed config missing the injected agent document")
	}
	if strings.Contains(rec.Body.String(), "wg0") {
		t.Error("served config still injects wg0 — phase 2 removed it")
	}
}

func TestUnsealEndpoint(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr})
	s := &server{root: m.root, store: deviceflow.NewStore(), hub: m, adminAddrs: m.adminAddrs}

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
	// Identity is reported (not paged for) while still sealed — this
	// hub serves no identity plane (no --iroh-relay).
	if !strings.Contains(rec.Body.String(), "identity: hubkey "+m.issuer.Fingerprint()+" — SEALED") {
		t.Fatalf("/sealed body: %q", rec.Body.String())
	}
	// With an identity plane, a sealed hubkey pages (tqr): members
	// depend on it for #renew/#bundle.
	m.publicURL = "https://hub.example"
	rec = httptest.NewRecorder()
	s.handleSealed(rec, httptest.NewRequest("GET", "/sealed", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/sealed with identity sealed on an identity-plane hub: got %d, want 503", rec.Code)
	}

	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/unseal", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.handleUnseal(rec, req)
		return rec
	}
	// Bad speak-as signature: 403, identity stays sealed.
	if rec := post(url.Values{"speakas_signature": {"0xdeadbeef"}}); rec.Code != http.StatusForbidden || m.issuer.Serving() == nil {
		t.Fatalf("bad speak-as: got %d", rec.Code)
	}
	// Second signature alone, same wallet: identity unsealed.
	rec = post(url.Values{"speakas_signature": {speakAsSig(t, m, testKey(t))}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hub identity unsealed ("+m.issuer.Fingerprint()+")") {
		t.Fatalf("speak-as unseal: got %d: %s", rec.Code, rec.Body.String())
	}
	if m.issuer.Serving() != nil {
		t.Fatal("identity still sealed after a valid speak-as")
	}
	rec = httptest.NewRecorder()
	s.handleSealed(rec, httptest.NewRequest("GET", "/sealed", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "identity: hubkey "+m.issuer.Fingerprint()+" speaks for "+wellKnownAddr) {
		t.Fatalf("/sealed after identity unseal: %d %q", rec.Code, rec.Body.String())
	}
}

// TestUnsealEndpointBothSignatures is the page's normal path: one POST
// with both signatures from one wallet unseals both planes.
func TestUnsealEndpointBothSignatures(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr})
	s := &server{root: m.root, store: deviceflow.NewStore(), hub: m, adminAddrs: m.adminAddrs}
	form := url.Values{"signature": {unsealSig(t)}, "speakas_signature": {speakAsSig(t, m, testKey(t))}}
	req := httptest.NewRequest("POST", "/unseal", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleUnseal(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hub unsealed; hub identity unsealed") {
		t.Fatalf("both: got %d: %s", rec.Code, rec.Body.String())
	}
	if m.sealed() || m.issuer.Serving() != nil {
		t.Fatal("a plane is still sealed")
	}
	// Nothing to sign: 400.
	req = httptest.NewRequest("POST", "/unseal", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	s.handleUnseal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty unseal: got %d", rec.Code)
	}
}
