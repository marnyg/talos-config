package main

// Device enrollment (ADR-0012, enrollmsg.V3): a device presents its
// NodeId, the wallet signs (name, group, node, nonce), and the Issuer
// actor mints the member Kit via Enroll (#mint-device over the
// in-process network). Successor of the nebula-era nebenroll tests
// (Mesh v3 Phase 4 P4.2).

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

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/enrollmsg"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/policy"
	pcert "github.com/marnyg/talos-config/protocol/cert"
)

// newMeshEnrollServer is a hub with the master unsealed and the
// identity plane still sealed.
func newMeshEnrollServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	m := testHubManager(t, []string{wellKnownAddr})
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	s := &server{
		root:       m.root,
		store:      deviceflow.NewStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		hub:        m,
	}
	ts := httptest.NewServer(s.mux())
	t.Cleanup(ts.Close)
	return s, ts
}

// newEnrollServer is newMeshEnrollServer with the identity plane
// unsealed too (both signatures from the well-known wallet) and the
// Issuer actor listening.
func newEnrollServer(t *testing.T) (*server, *httptest.Server) {
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

type challenge struct{ Name, Group, Node, Nonce, Message string }

func fetchChallenge(t *testing.T, base, name, group, node string) (int, challenge) {
	t.Helper()
	resp, err := http.PostForm(base+"/mesh/enroll/challenge", url.Values{
		"name": {name}, "group": {group}, "node": {node},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var ch challenge
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &ch); err != nil {
			t.Fatalf("challenge not JSON: %s", body)
		}
	}
	return resp.StatusCode, ch
}

func redeem(t *testing.T, base string, ch challenge, sig string) (int, []byte, string) {
	t.Helper()
	resp, err := http.PostForm(base+"/mesh/enroll", url.Values{
		"name": {ch.Name}, "group": {ch.Group}, "node": {ch.Node},
		"nonce": {ch.Nonce}, "signature": {sig},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, body, resp.Header.Get("Content-Type")
}

func TestMeshEnrollDirect(t *testing.T) {
	_, ts := newEnrollServer(t)
	node := newNodeID(t)

	code, ch := fetchChallenge(t, ts.URL, "Laptop", policy.GroupAdmins, node)
	if code != http.StatusOK {
		t.Fatalf("challenge: %d", code)
	}
	if ch.Node != node || ch.Name != "laptop" || ch.Message != enrollmsg.V3("laptop", policy.GroupAdmins, node, ch.Nonce) {
		t.Fatalf("challenge is not the v3 message for the node: %+v", ch)
	}

	sig := personalSign(t, testKey(t), ch.Message)
	code, body, ct := redeem(t, ts.URL, ch, sig)
	if code != http.StatusOK {
		t.Fatalf("enroll: %d %s", code, body)
	}
	if ct != enrollContentType {
		t.Fatalf("content type %q, want %q", ct, enrollContentType)
	}
	kit, err := issuer.DecodeKit(body)
	if err != nil {
		t.Fatal(err)
	}
	m := kit.Member
	if m.Aud != node || m.Cav.Name != "laptop" || !slices.Equal(m.Cav.Groups, []string{policy.GroupAdmins}) {
		t.Fatalf("member: %+v", m)
	}
	if kit.SpeakAs.Iss != issuer.WalletID(wellKnownAddr) {
		t.Fatalf("speak-as issuer %s, want the approving wallet", kit.SpeakAs.Iss)
	}
	if !slices.Equal(kit.BeatGrant.Cav.Target, []pcert.ActorID{issuer.WalletID(wellKnownAddr)}) {
		t.Fatalf("renew grant target %v, want the wallet (ADR-0024 F)", kit.BeatGrant.Cav.Target)
	}
}

func TestMeshEnrollRejects(t *testing.T) {
	_, ts := newEnrollServer(t)
	node := newNodeID(t)

	// Wrong signature: sign a different nonce, replay against a fresh
	// challenge. The hub rebuilds the message from server state and
	// checks that recover(signed) matches an allowlisted address.
	_, ch := fetchChallenge(t, ts.URL, "laptop", policy.GroupAdmins, node)
	badSig := personalSign(t, testKey(t), enrollmsg.V3("laptop", policy.GroupAdmins, node, "not-the-nonce"))
	if code, _, _ := redeem(t, ts.URL, ch, badSig); code != http.StatusForbidden {
		t.Errorf("wrong-nonce signature: got %d, want 403", code)
	}

	// A signature over the SAME fields for a different node: refused,
	// the node is bound into what the wallet signed.
	otherSig := personalSign(t, testKey(t), enrollmsg.V3("laptop", policy.GroupAdmins, newNodeID(t), ch.Nonce))
	if code, _, _ := redeem(t, ts.URL, ch, otherSig); code != http.StatusForbidden {
		t.Errorf("other-node signature: got %d, want 403", code)
	}

	// A non-allowlisted wallet: refused.
	if code, _, _ := redeem(t, ts.URL, ch, personalSign(t, otherKey(t), ch.Message)); code != http.StatusForbidden {
		t.Errorf("stranger wallet: got %d, want 403", code)
	}

	// Correct signature, redeemed twice: the second must fail.
	sig := personalSign(t, testKey(t), ch.Message)
	if code, _, _ := redeem(t, ts.URL, ch, sig); code != http.StatusOK {
		t.Fatalf("first redeem: got %d", code)
	}
	if code, _, _ := redeem(t, ts.URL, ch, sig); code != http.StatusForbidden {
		t.Errorf("replayed nonce: got %d, want 403", code)
	}

	// Colliding name: the well-known test tree has a machine at
	// "aa-bb-cc-dd-ee-ff" (nameless, so its label is the MAC with
	// dashes). A device asking for that label — or for "hub" — is
	// refused at challenge time, and at mint time should the approver
	// type it on the card.
	for _, taken := range []string{"aa-bb-cc-dd-ee-ff", "hub"} {
		if code, _ := fetchChallenge(t, ts.URL, taken, policy.GroupAdmins, node); code != http.StatusConflict {
			t.Errorf("git-owned name %q: challenge got %d, want 409", taken, code)
		}
	}
	_, ch = fetchChallenge(t, ts.URL, "laptop2", policy.GroupAdmins, node)
	ch.Name = "aa-bb-cc-dd-ee-ff"
	if code, _, _ := redeem(t, ts.URL, ch, personalSign(t, testKey(t), enrollmsg.V3(ch.Name, ch.Group, node, ch.Nonce))); code != http.StatusForbidden {
		t.Errorf("git-owned name at mint: got %d, want 403", code)
	}

	// Group outside the device vocabulary: refused at challenge time.
	if code, _ := fetchChallenge(t, ts.URL, "laptop", policy.GroupMachines, node); code != http.StatusBadRequest {
		t.Errorf("machines group: got %d, want 400", code)
	}
	if code, _ := fetchChallenge(t, ts.URL, "", policy.GroupAdmins, node); code != http.StatusBadRequest {
		t.Errorf("empty name: got %d, want 400", code)
	}
}

// TestMeshEnrollNeedsIdentityPlane: with only the master unsealed, an
// enrollment is refused before any wallet act (challenge and
// device-flow start both 503).
func TestMeshEnrollNeedsIdentityPlane(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	node := newNodeID(t)

	if code, _ := fetchChallenge(t, ts.URL, "laptop", policy.GroupAdmins, node); code != http.StatusServiceUnavailable {
		t.Errorf("challenge with identity sealed: %d, want 503", code)
	}
	resp, err := http.PostForm(ts.URL+"/mesh/enroll/device", url.Values{
		"proposed_name": {"tv"}, "node": {node},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("device start with identity sealed: %d, want 503", resp.StatusCode)
	}
}

// TestMeshEnrollNodeRequired: a malformed or missing node is a 400.
func TestMeshEnrollNodeRequired(t *testing.T) {
	_, ts := newEnrollServer(t)
	for _, node := range []string{"", "eth:0xabc", "ed:nothex"} {
		if code, _ := fetchChallenge(t, ts.URL, "laptop", policy.GroupAdmins, node); code != http.StatusBadRequest {
			t.Errorf("node %q: %d, want 400", node, code)
		}
	}
}

func startDeviceFlow(t *testing.T, base, node, proposedName, proposedGroup string) (deviceCode, userCode string) {
	t.Helper()
	resp, err := http.PostForm(base+"/mesh/enroll/device", url.Values{
		"node": {node}, "proposed_name": {proposedName}, "proposed_group": {proposedGroup},
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
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := json.Unmarshal(body, &start); err != nil {
		t.Fatal(err)
	}
	return start.DeviceCode, start.UserCode
}

func approveMeshEnroll(t *testing.T, s *server, base string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", base+"/mesh/enroll/approve", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: s.sessions.create(wellKnownAddr)})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // the 303 to /status is the success signal
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func pollToken(t *testing.T, base, deviceCode string) (string, string) {
	t.Helper()
	resp, err := http.PostForm(base+"/token", url.Values{
		"grant_type": {deviceCodeGrantType}, "device_code": {deviceCode},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("token response not JSON: %s", body)
	}
	return out.AccessToken, out.Error
}

func TestMeshEnrollDeviceFlow(t *testing.T) {
	s, ts := newEnrollServer(t)
	node := newNodeID(t)
	deviceCode, userCode := startDeviceFlow(t, ts.URL, node, "tv", policy.GroupMedia)

	if da, ok := s.pendingMeshEnroll(userCode); !ok || da.Identity["node"] != node {
		t.Fatal("pending flow does not carry the node server-side")
	}

	// The approver picks the final name; the wallet signs the node too.
	nonce, err := s.store.NonceFor(userCode)
	if err != nil {
		t.Fatal(err)
	}
	sig := personalSign(t, testKey(t), enrollmsg.V3("livingroom", policy.GroupMedia, node, nonce))
	if r := approveMeshEnroll(t, s, ts.URL, url.Values{
		"user_code": {userCode}, "name": {"livingroom"},
		"group": {policy.GroupMedia}, "signature": {sig},
	}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve: %d", r.StatusCode)
	}
	token, oauthErr := pollToken(t, ts.URL, deviceCode)
	if oauthErr != "" || token == "" {
		t.Fatalf("token poll: %q", oauthErr)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/mesh/enroll/config", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != enrollContentType {
		t.Fatalf("redeem: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	kit, err := issuer.DecodeKit(body)
	if err != nil {
		t.Fatal(err)
	}
	if kit.Member.Aud != node || kit.Member.Cav.Name != "livingroom" || !slices.Equal(kit.Member.Cav.Groups, []string{policy.GroupMedia}) {
		t.Fatalf("member: %+v", kit.Member)
	}

	// The token is single-use.
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Error("kit redeemed twice with one token")
	}
}

func TestMeshEnrollApproveAdminsRequiresRetype(t *testing.T) {
	s, ts := newEnrollServer(t)
	node := newNodeID(t)
	deviceCode, userCode := startDeviceFlow(t, ts.URL, node, "console", policy.GroupAdmins)

	nonce, err := s.store.NonceFor(userCode)
	if err != nil {
		t.Fatal(err)
	}
	sig := personalSign(t, testKey(t), enrollmsg.V3("console", policy.GroupAdmins, node, nonce))

	// No admin_retype: refused, flow still pending.
	approveMeshEnroll(t, s, ts.URL, url.Values{
		"user_code": {userCode}, "name": {"console"},
		"group": {policy.GroupAdmins}, "signature": {sig},
	})
	if _, oauthErr := pollToken(t, ts.URL, deviceCode); oauthErr != deviceflow.ErrCodeAuthorizationPending {
		t.Fatalf("poll after refused approve: err=%q, want %q", oauthErr, deviceflow.ErrCodeAuthorizationPending)
	}
	if _, ok := s.pendingMeshEnroll(userCode); !ok {
		t.Fatal("flow should still be pending without the admins retype")
	}

	// With the retype (and the same still-unredeemed nonce): approved.
	approveMeshEnroll(t, s, ts.URL, url.Values{
		"user_code": {userCode}, "name": {"console"}, "admin_retype": {"console"},
		"group": {policy.GroupAdmins}, "signature": {sig},
	})
	if _, ok := s.pendingMeshEnroll(userCode); ok {
		t.Fatal("flow should be approved after the retype")
	}
}

func TestVerifyRefusesMeshEnrollApprove(t *testing.T) {
	s, ts := newEnrollServer(t)
	deviceCode, userCode := startDeviceFlow(t, ts.URL, newNodeID(t), "tv", policy.GroupMedia)

	nonce, err := s.store.NonceFor(userCode)
	if err != nil {
		t.Fatal(err)
	}
	approveSig := personalSign(t, testKey(t), approvalMessage("approve", userCode, nonce))
	resp, err := http.PostForm(ts.URL+"/verify", url.Values{
		"user_code": {userCode}, "action": {"approve"}, "signature": {approveSig},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, ok := s.pendingMeshEnroll(userCode); !ok {
		t.Fatal("generic /verify approve must not decide a mesh enrollment")
	}

	// Deny through /verify still works and kills the flow.
	denySig := personalSign(t, testKey(t), approvalMessage("deny", userCode, nonce))
	resp2, err := http.PostForm(ts.URL+"/verify", url.Values{
		"user_code": {userCode}, "action": {"deny"}, "signature": {denySig},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if _, ok := s.pendingMeshEnroll(userCode); ok {
		t.Fatal("deny through /verify should end the flow")
	}
	if _, oauthErr := pollToken(t, ts.URL, deviceCode); oauthErr != deviceflow.ErrCodeAccessDenied {
		t.Fatalf("poll after deny: err=%q, want %q", oauthErr, deviceflow.ErrCodeAccessDenied)
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
