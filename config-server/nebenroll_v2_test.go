package main

// Dual-plane enrollment (Mesh v3 Phase 1, 359.8.2.3): a device that
// presents its NodeId signs the v2 message and gets the nebula config
// AND a member kit from one wallet signature; the kit comes from the
// Issuer actor via Enroll (#mint-device over the in-process network).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/config-server/enrollmsg"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/mesh"
	pcert "github.com/marnyg/talos-config/protocol/cert"
)

// newDualPlaneServer is newMeshEnrollServer with the identity plane
// unsealed too (both signatures from the well-known wallet) and the
// Issuer actor listening.
func newDualPlaneServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	s, ts := newMeshEnrollServer(t)
	unsealIdentity(t, s)
	return s, ts
}

func unsealIdentity(t *testing.T, s *server) {
	t.Helper()
	if _, err := s.hub.unsealIssuer(speakAsSig(t, s.hub, testKey(t))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.hub.listen(ctx)
	t.Cleanup(cancel)
}

// newNodeID is a device's own Ed25519 identity, as it would present it.
func newNodeID(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(pcert.NewEdSigner(priv).ActorID())
}

type kitEnvelope struct {
	Config string          `json:"config"`
	Kit    json.RawMessage `json:"kit"`
}

func TestMeshEnrollV2Direct(t *testing.T) {
	_, ts := newDualPlaneServer(t)
	_, pubHex := makeDeviceKeypair(t)
	node := newNodeID(t)

	resp, err := http.PostForm(ts.URL+"/mesh/enroll/challenge", url.Values{
		"name": {"Laptop"}, "group": {mesh.GroupAdmins}, "pubkey": {pubHex}, "node": {node},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("challenge: %d %s", resp.StatusCode, body)
	}
	var ch struct{ Name, Group, Node, Nonce, Fingerprint, Message string }
	if err := json.Unmarshal(body, &ch); err != nil {
		t.Fatal(err)
	}
	if ch.Node != node || ch.Message != enrollmsg.V2("laptop", mesh.GroupAdmins, ch.Fingerprint, node, ch.Nonce) {
		t.Fatalf("challenge is not the v2 message for the node: %+v", ch)
	}

	sig := personalSign(t, testKey(t), ch.Message)
	resp, err = http.PostForm(ts.URL+"/mesh/enroll", url.Values{
		"name": {ch.Name}, "group": {ch.Group}, "pubkey": {pubHex}, "node": {node},
		"nonce": {ch.Nonce}, "signature": {sig},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll: %d %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != enrollKitContentType {
		t.Fatalf("content type %q, want %q", ct, enrollKitContentType)
	}
	var env kitEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.Config, "pki:") {
		t.Fatalf("config missing from the v2 payload: %q", env.Config)
	}
	kit, err := issuer.DecodeKit(env.Kit)
	if err != nil {
		t.Fatal(err)
	}
	m := kit.Member
	if m.Aud != node || m.Cav.Name != "laptop" || !slices.Equal(m.Cav.Groups, []string{mesh.GroupAdmins}) {
		t.Fatalf("member: %+v", m)
	}
	if kit.SpeakAs.Iss != issuer.WalletID(wellKnownAddr) {
		t.Fatalf("speak-as issuer %s, want the approving wallet", kit.SpeakAs.Iss)
	}
	if !slices.Equal(kit.BeatGrant.Cav.Target, []pcert.ActorID{issuer.WalletID(wellKnownAddr)}) {
		t.Fatalf("renew grant target %v, want the wallet (ADR-0024 F)", kit.BeatGrant.Cav.Target)
	}
}

// TestMeshEnrollV2NeedsIdentityPlane: with only the master unsealed,
// a node-naming enrollment is refused before any wallet act (challenge
// and device-flow start both 503), and v1 enrollment is untouched.
func TestMeshEnrollV2NeedsIdentityPlane(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)
	node := newNodeID(t)

	resp, err := http.PostForm(ts.URL+"/mesh/enroll/challenge", url.Values{
		"name": {"laptop"}, "group": {mesh.GroupAdmins}, "pubkey": {pubHex}, "node": {node},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("v2 challenge with identity sealed: %d, want 503", resp.StatusCode)
	}
	resp, err = http.PostForm(ts.URL+"/mesh/enroll/device", url.Values{
		"pubkey": {pubHex}, "proposed_name": {"tv"}, "node": {node},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("v2 device start with identity sealed: %d, want 503", resp.StatusCode)
	}
	if code, _ := meshEnroll(t, ts.URL, "laptop", mesh.GroupAdmins, pubHex); code != http.StatusOK {
		t.Errorf("v1 enrollment with identity sealed: %d, want 200", code)
	}

	// A malformed node is a 400, not a silent v1 fallback.
	resp, err = http.PostForm(ts.URL+"/mesh/enroll/challenge", url.Values{
		"name": {"laptop"}, "group": {mesh.GroupAdmins}, "pubkey": {pubHex}, "node": {"eth:0xabc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-ed node: %d, want 400", resp.StatusCode)
	}
}

func TestMeshEnrollV2DeviceFlow(t *testing.T) {
	s, ts := newDualPlaneServer(t)
	_, pubHex := makeDeviceKeypair(t)
	node := newNodeID(t)

	resp, err := http.PostForm(ts.URL+"/mesh/enroll/device", url.Values{
		"pubkey": {pubHex}, "proposed_name": {"tv"}, "proposed_group": {mesh.GroupMedia}, "node": {node},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %s", resp.StatusCode, body)
	}
	var start struct {
		DeviceCode  string `json:"device_code"`
		UserCode    string `json:"user_code"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(body, &start); err != nil {
		t.Fatal(err)
	}
	if da, ok := s.pendingMeshEnroll(start.UserCode); !ok || da.Identity["node"] != node {
		t.Fatal("pending flow does not carry the node server-side")
	}

	// The approver's card is v2: the wallet signs the node too.
	nonce, err := s.store.NonceFor(start.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	sig := personalSign(t, testKey(t), enrollmsg.V2("livingroom", mesh.GroupMedia, start.Fingerprint, node, nonce))
	if r := approveMeshEnroll(t, s, ts.URL, url.Values{
		"user_code": {start.UserCode}, "name": {"livingroom"},
		"group": {mesh.GroupMedia}, "signature": {sig},
	}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve: %d", r.StatusCode)
	}
	// A v1 signature over the same fields would NOT have approved it:
	// the node is bound into what the wallet signed.
	token, oauthErr := pollToken(t, ts.URL, start.DeviceCode)
	if oauthErr != "" || token == "" {
		t.Fatalf("token poll: %q", oauthErr)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/mesh/enroll/config", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	cfgResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(cfgResp.Body)
	cfgResp.Body.Close()
	if cfgResp.StatusCode != http.StatusOK || cfgResp.Header.Get("Content-Type") != enrollKitContentType {
		t.Fatalf("redeem: %d %q", cfgResp.StatusCode, cfgResp.Header.Get("Content-Type"))
	}
	var env kitEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	kit, err := issuer.DecodeKit(env.Kit)
	if err != nil {
		t.Fatal(err)
	}
	if kit.Member.Aud != node || kit.Member.Cav.Name != "livingroom" || !slices.Equal(kit.Member.Cav.Groups, []string{mesh.GroupMedia}) {
		t.Fatalf("member: %+v", kit.Member)
	}
}

// TestMeshEnrollV2ApproveWithV1Signature: the operator's wallet signed
// the v1 text for a flow that named a node — refused; nothing mints.
func TestMeshEnrollV2ApproveWithV1Signature(t *testing.T) {
	s, ts := newDualPlaneServer(t)
	_, pubHex := makeDeviceKeypair(t)
	node := newNodeID(t)
	resp, err := http.PostForm(ts.URL+"/mesh/enroll/device", url.Values{
		"pubkey": {pubHex}, "proposed_name": {"tv"}, "node": {node},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var start struct {
		UserCode    string `json:"user_code"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(body, &start); err != nil {
		t.Fatal(err)
	}
	nonce, _ := s.store.NonceFor(start.UserCode)
	sig := personalSign(t, testKey(t), enrollmsg.V1("tv", mesh.GroupMedia, start.Fingerprint, nonce))
	approveMeshEnroll(t, s, ts.URL, url.Values{
		"user_code": {start.UserCode}, "name": {"tv"}, "group": {mesh.GroupMedia}, "signature": {sig},
	})
	// Still pending (approval would have moved it out of Pending) and
	// no payload stashed.
	if da, ok := s.pendingMeshEnroll(start.UserCode); !ok || da.Payload != nil {
		t.Fatal("v1 signature approved a v2 flow")
	}
}

func TestWellKnownSpeakAs(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	code, _ := get(t, ts.Client(), ts.URL+wellKnownSpeakAsPath)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("sealed identity: %d, want 503", code)
	}
	unsealIdentity(t, s)
	resp, err := ts.Client().Get(ts.URL + wellKnownSpeakAsPath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unsealed: %d %s", resp.StatusCode, body)
	}
	sa, err := pcert.DecodeCert(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := pcert.Verify(sa); err != nil {
		t.Fatal(err)
	}
	if sa.Iss != issuer.WalletID(wellKnownAddr) || sa.Aud != string(s.hub.issuer.ID()) || sa.Can != pcert.VerbSpeakAs {
		t.Fatalf("speak-as: %+v", sa)
	}
}
