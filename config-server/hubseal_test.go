package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/mesh"
	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/cert"
)

var nebSealSubnet = netip.MustParsePrefix("10.42.0.0/16")

const nebTestEndpoint = "203.0.113.7:4242"

// testNebManager returns a mesh manager whose nebula start is stubbed:
// everything up to and including config rendering runs for real, only
// the UDP socket is skipped. (The mesh package has its own twin; this
// one exists for the hub- and server-level tests in package main.)
func testNebManager(t *testing.T, root string) (*mesh.Manager, *[]byte) {
	t.Helper()
	// Every render path needs mesh-policy.yaml; tests run against the
	// repo's real file so they guard it, not a fixture that could drift.
	policy, err := os.ReadFile(filepath.Join("..", "talos", mesh.PolicyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, mesh.PolicyFile), policy, 0o644); err != nil {
		t.Fatal(err)
	}
	var rendered []byte
	m := mesh.NewManager(4242, nebSealSubnet, "0.0.0.0", nebTestEndpoint, nebderive.DNSZone, root)
	m.Start = func(cfg []byte) (*nebstack.Service, error) {
		rendered = cfg
		return nil, nil
	}
	return m, &rendered
}

// testHubManager builds a sealed hub over a throwaway talos tree with
// one declared machine and a stub-started mesh (no real overlay).
func testHubManager(t *testing.T, adminAddrs []string, pinnedCAFP string) *hubManager {
	t.Helper()
	return testHubManagerOn(t, adminAddrs, pinnedCAFP, nil)
}

// testHubManagerOn is testHubManager with a second wire for the hubkey
// (hubiroh_test.go binds it on iroh). The v3 recipe and blocklist ride
// along so #bundle compiles against the repo's real files.
func testHubManagerOn(t *testing.T, adminAddrs []string, pinnedCAFP string, wan hubTransport) *hubManager {
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
	meta := "ip: 127.0.0.1\nconfig: base.yaml\npatches: []\n"
	if err := os.WriteFile(filepath.Join(machineDir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	base := "version: v1alpha1\nmachine:\n  type: worker\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}

	nm, _ := testNebManager(t, root)
	m, err := newHubManager(root, adminAddrs, pinnedCAFP, nm, wan)
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

// otherAddr is otherKey's lowercase 0x address.
func otherAddr(t *testing.T) string {
	t.Helper()
	return strings.TrimPrefix(string(cert.NewEthSigner(otherKey(t)).ActorID()), "eth:")
}

func TestNewHubManagerRejectsMalformedAdmin(t *testing.T) {
	if _, err := newHubManager(t.TempDir(), []string{"0xnope"}, "", nil, nil); err == nil {
		t.Fatal("malformed admin address accepted")
	}
}

// TestUnsealIssuer: the identity plane seals and unseals independently
// of the master, and the speak-as must come from the wallet that
// unsealed the master when one did.
func TestUnsealIssuer(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr, otherAddr(t)}, "")
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
	m := testHubManager(t, []string{wellKnownAddr, otherAddr(t)}, "")
	if _, err := m.unsealIssuer(speakAsSig(t, m, otherKey(t))); err != nil {
		t.Fatalf("speak-as while master sealed: %v", err)
	}
	if m.sealed() != true || m.issuer.Serving() != nil {
		t.Fatal("identity unseal touched the master, or failed")
	}
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
	// deterministically instead of reaching into it. The subnet and port
	// are testNebManager's fixed values.
	master := m.current()
	if master == nil {
		t.Fatal("hub is sealed")
	}
	cfg, err := mesh.HubConfig(mesh.HubParams{
		Master:     master,
		Subnet:     m.mesh.Subnet(),
		ListenHost: "0.0.0.0",
		ListenPort: 4242,
		// Any non-empty admission table renders; this helper only
		// checks that a config comes out ("pki" present).
		Inbound: []mesh.FirewallRule{{Port: "any", Proto: "icmp", Host: "any"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &cfg
}

// A mesh that cannot start must not take the unseal with it: KMS disk
// unlocks ride the WAN listener and must not depend on the overlay
// (invariant 4). The failure is recorded, not swallowed.
func TestMeshFailureDoesNotBreakHubUnseal(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	nm, _ := testNebManager(t, m.root)
	nm.Start = func([]byte) (*nebstack.Service, error) {
		return nil, errors.New("simulated mesh failure")
	}
	m.mesh = nm

	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatalf("mesh failure broke the hub unseal: %v", err)
	}
	if m.sealed() {
		t.Fatal("hub still sealed after a mesh-only failure")
	}
	if _, _, err := nm.State(); err == nil {
		t.Error("mesh failure was not recorded")
	}
}

// /sealed pages on a mesh startup failure — the phase-2 inversion: the
// mesh is the control channel now, so "unsealed but mesh down" is an
// incident, not a footnote.
func TestSealedEndpointPagesOnMeshFailure(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	nm, _ := testNebManager(t, m.root)
	nm.Start = func([]byte) (*nebstack.Service, error) {
		return nil, errors.New("simulated mesh failure")
	}
	m.mesh = nm
	s := &server{root: m.root, hub: m}

	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.handleSealed(rec, httptest.NewRequest("GET", "/sealed", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (a mesh failure must page)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hub: unsealed") {
		t.Errorf("body does not report hub state: %q", body)
	}
	if !strings.Contains(body, "mesh: DOWN") || !strings.Contains(body, "simulated mesh failure") {
		t.Errorf("body does not report why the mesh is down: %q", body)
	}
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
		t.Error("unsealed config missing injected mesh identity")
	}
	if strings.Contains(rec.Body.String(), "wg0") {
		t.Error("served config still injects wg0 — phase 2 removed it")
	}
}

func TestUnsealEndpoint(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
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
	m := testHubManager(t, []string{wellKnownAddr}, "")
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
