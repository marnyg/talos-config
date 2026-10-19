package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

// TestUnsealIssuerPinAdmits: the speak-as issuer is judged by the hub's
// pin for the wallet (protocol ADR-0010). With the trivial pin every
// admin unseals as before; with a pin whose current policy names
// another root — a rotation the hub holds, constructed directly here —
// the wallet's own signature is refused (the old root died with its
// epoch) and the new root's accepted under the wallet's name; a dead
// pin admits neither. The Issuer's actor holds the same pins, so
// Authorize sees the same view.
func TestUnsealIssuerPinAdmits(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr, otherAddr(t)})
	wk := issuer.WalletID(wellKnownAddr)
	if got := m.issuer.Actor.Pins(); len(got) != 2 || got[0].Address != wk || !got[0].Admits(wk) {
		t.Fatalf("actor pins = %v", got)
	}
	if _, err := m.unsealIssuer(speakAsSig(t, m, testKey(t))); err != nil {
		t.Fatalf("trivial pin refused the wallet: %v", err)
	}

	// Rotate the well-known wallet to otherKey (guardian = the wallet
	// itself, k = 1): the hub now admits otherKey AS the well-known
	// identity and the well-known key not at all.
	m2 := testHubManager(t, []string{wellKnownAddr})
	p0 := trivialPolicy(wk)
	next := issuer.WalletID(otherAddr(t))
	p1 := cert.Policy{Root: next, Guardians: []cert.ActorID{next}, K: 1}
	rc, err := cert.Sign(cert.Cert{Aud: string(wk), Can: cert.VerbRotateRoot,
		Cav: cert.Caveats{Epoch: 1, Prev: p0.Hash(), Policy: &p1}, Iat: 1, Exp: m2.issuer.Actor.Now() + 3600}, cert.NewEthSigner(testKey(t)))
	if err != nil {
		t.Fatal(err)
	}
	rot, err := cert.AssembleRotation(wk, p0, 1, []cert.Cert{rc}, m2.issuer.Actor.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.pin(wk).Hold(rot); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.unsealIssuer(speakAsSig(t, m2, testKey(t))); err == nil || !errors.Is(err, issuer.ErrNotAllowed) {
		t.Fatalf("old root unsealed after rotation: %v", err)
	}
	if m2.issuer.Serving() == nil {
		t.Fatal("identity unsealed by the rotated-away root")
	}
	// The new root signs the proposal addressed to ITSELF (the speak-as
	// names the signing key as iss; the pin maps it to the identity).
	if addr, err := m2.unsealIssuer(speakAsSig(t, m2, otherKey(t))); err != nil || addr != otherAddr(t) {
		t.Fatalf("new root refused after rotation: %v (%s)", err, addr)
	}
	// The hub's speak-as is now from the new root; its Authorize must
	// resolve that key to the well-known identity through the pin.
	sa := m2.issuer.SpeakAs()
	if sa == nil || sa.Iss != next {
		t.Fatalf("held speak-as iss = %v", sa)
	}

	// Dead pin: a second, distinct successor at epoch 1 ⇒ equivocation;
	// neither key unseals.
	m3 := testHubManager(t, []string{wellKnownAddr})
	p1b := cert.Policy{Root: next, Guardians: []cert.ActorID{next, wk}, K: 1}
	rcb, err := cert.Sign(cert.Cert{Aud: string(wk), Can: cert.VerbRotateRoot,
		Cav: cert.Caveats{Epoch: 1, Prev: p0.Hash(), Policy: &p1b}, Iat: 1, Exp: m3.issuer.Actor.Now() + 3600}, cert.NewEthSigner(testKey(t)))
	if err != nil {
		t.Fatal(err)
	}
	rotb, err := cert.AssembleRotation(wk, p0, 1, []cert.Cert{rcb}, m3.issuer.Actor.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := m3.pin(wk).Hold(rot); err != nil {
		t.Fatal(err)
	}
	if err := m3.pin(wk).Hold(rotb); !errors.Is(err, cert.ErrEquivocation) {
		t.Fatalf("second successor: %v", err)
	}
	for name, k := range map[string]*secp256k1.PrivateKey{"old root": testKey(t), "new root": otherKey(t)} {
		if _, err := m3.unsealIssuer(speakAsSig(t, m3, k)); err == nil || !errors.Is(err, issuer.ErrNotAllowed) {
			t.Fatalf("dead pin admitted the %s: %v", name, err)
		}
	}
}

// TestPinsFromGit: talos/pins is the durable copy (d0o0). A rotation on
// file at construction means the hub comes up admitting the NEW root
// for the wallet, not the wallet itself — the view a restart would
// otherwise lose; #bundle carries the same rotation to members. A
// later tip (reloadPins) is the whole truth: a file that no longer
// exists re-pins trivially, a malformed one keeps the installed set,
// and an identity pinned by git without being an admin is held too.
func TestPinsFromGit(t *testing.T) {
	wk := issuer.WalletID(wellKnownAddr)
	next := issuer.WalletID(otherAddr(t))
	p0 := trivialPolicy(wk)
	p1 := trivialPolicy(next)
	rc, err := cert.Sign(cert.Cert{Aud: string(wk), Can: cert.VerbRotateRoot,
		Cav: cert.Caveats{Epoch: 1, Prev: p0.Hash(), Policy: &p1}, Iat: 1, Exp: 1 << 40}, cert.NewEthSigner(testKey(t)))
	if err != nil {
		t.Fatal(err)
	}
	file, err := policy.EncodePin(wk, p0, [][]cert.Cert{{rc}})
	if err != nil {
		t.Fatal(err)
	}

	// Build the tree first, then the hub over it: the construction path.
	m := testHubManager(t, []string{wellKnownAddr})
	dir := filepath.Join(m.root, policy.PinsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, policy.PinFileName(wk))
	if err := os.WriteFile(path, file, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err = newHubManager(m.root, []string{wellKnownAddr}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.issuer.Actor.Pins(); len(got) != 1 || got[0].View().Epoch != 1 || !got[0].Admits(next) || got[0].Admits(wk) {
		t.Fatalf("actor pins after load = %v", got)
	}
	if _, err := m.unsealIssuer(speakAsSig(t, m, testKey(t))); !errors.Is(err, issuer.ErrNotAllowed) {
		t.Fatalf("rotated-away root unsealed: %v", err)
	}
	if addr, err := m.unsealIssuer(speakAsSig(t, m, otherKey(t))); err != nil || addr != otherAddr(t) {
		t.Fatalf("new root: %v (%s)", err, addr)
	}

	// A malformed file on the next tip keeps the installed pins.
	if err := os.WriteFile(path, []byte(`{"address":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.reloadPins(); err == nil {
		t.Fatal("malformed pin file accepted on reload")
	}
	if p := m.pin(wk); p == nil || !p.Admits(next) {
		t.Fatalf("malformed reload replaced the pins: %v", p)
	}

	// The file gone (a re-pin by commit) ⇒ trivial again; a further
	// identity on file is held beside the wallets.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	extra, err := policy.EncodePin(next, p1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, policy.PinFileName(next)), extra, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.reloadPins(); err != nil {
		t.Fatal(err)
	}
	got := m.issuer.Actor.Pins()
	if len(got) != 2 || got[0].Address != wk || !got[0].Admits(wk) || got[1].Address != next || !got[1].Admits(next) {
		t.Fatalf("actor pins after re-pin = %v", got)
	}
	// The speak-as held was signed by `next` AS the well-known wallet;
	// `next` is still admitted — only now as its own identity — so the
	// identity stays unsealed. Drop that extra pin: nothing admits the
	// issuer, and the hub re-seals rather than serve under a dead root.
	if m.issuer.SpeakAs() == nil {
		t.Fatal("re-pin sealed the identity while its issuer is still admitted")
	}
	if err := os.Remove(filepath.Join(dir, policy.PinFileName(next))); err != nil {
		t.Fatal(err)
	}
	if err := m.reloadPins(); err != nil {
		t.Fatal(err)
	}
	if sa := m.issuer.SpeakAs(); sa != nil || !errors.Is(m.issuer.Serving(), issuer.ErrSealed) {
		t.Fatalf("identity still unsealed under an un-admitted root: %v", sa)
	}
	// And the wallet itself unseals again, trivially pinned.
	if _, err := m.unsealIssuer(speakAsSig(t, m, testKey(t))); err != nil {
		t.Fatalf("wallet refused after re-pin: %v", err)
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

// wg0Interface matches the `interface: wg0` line of the Talos
// network.interfaces document the hub injected before phase 2.
var wg0Interface = regexp.MustCompile(`(?m)^\s*-?\s*interface:\s*["']?wg0["']?\s*$`)

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
	// Anchor on the YAML line the old network.interfaces entry carried
	// rather than a bare substring: the served config embeds freshly
	// generated base64 cert material, and any 3-byte window of that can
	// spell "wg0" (base64 has no ':' or whitespace, so this cannot).
	if wg0Interface.MatchString(rec.Body.String()) {
		t.Error("served config still injects the wg0 interface document — phase 2 removed it")
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
