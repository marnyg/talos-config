package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"gopkg.in/yaml.v3"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/cert"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/mesh"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// newMeshEnrollServer returns an unsealed server ready to accept
// wallet-signed enrollments (ADR-0012). No declared device list —
// group and name are decided by the caller's request and gated by the
// wallet signature.
func newMeshEnrollServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	m := testHubManager(t, []string{wellKnownAddr}, "")
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

// makeDeviceKeypair generates a fresh X25519 keypair the way nebup does.
// Returned pubkey as hex so it can go straight into POST bodies.
func makeDeviceKeypair(t *testing.T) (priv [32]byte, pubHex string) {
	t.Helper()
	if _, err := rand.Read(priv[:]); err != nil {
		t.Fatal(err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	var pub [32]byte
	curve25519.ScalarBaseMult(&pub, &priv)
	return priv, hex.EncodeToString(pub[:])
}

// meshEnroll runs the full v1 exchange for a caller-chosen name/group
// with a caller-generated pubkey.
func meshEnroll(t *testing.T, base, name, group, pubHex string) (int, string) {
	t.Helper()
	resp, err := http.PostForm(base+"/mesh/enroll/challenge", url.Values{
		"name": {name}, "group": {group}, "pubkey": {pubHex},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, string(body)
	}
	var ch struct{ Name, Group, Nonce, Fingerprint, Message string }
	if err := json.Unmarshal(body, &ch); err != nil {
		t.Fatal(err)
	}
	sig := personalSign(t, testKey(t), ch.Message)
	resp2, err := http.PostForm(base+"/mesh/enroll", url.Values{
		"name": {ch.Name}, "group": {ch.Group}, "pubkey": {pubHex},
		"nonce": {ch.Nonce}, "signature": {sig},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	return resp2.StatusCode, string(out)
}

// TestMeshEnrollIssuesDeviceIdentity is the happy path: the wallet
// signs (name, group, pubkey-fp, nonce), and the hub returns a config
// whose cert binds the requested name and group to the caller's
// pubkey. pki.key is left empty (ADR-0012): the client splices its
// own key before running nebula.
func TestMeshEnrollIssuesDeviceIdentity(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)

	code, body := meshEnroll(t, ts.URL, "laptop", mesh.GroupAdmins, pubHex)
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}

	var got mesh.ConfigYAML
	if err := yaml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	// CA and cert inline; key empty (client splices).
	for name, field := range map[string]string{"ca": got.PKI.CA, "cert": got.PKI.Cert} {
		if !strings.HasPrefix(field, "-----BEGIN") {
			t.Errorf("pki.%s is not inline PEM: %q", name, field)
		}
	}
	if got.PKI.Key != "" {
		t.Errorf("pki.key should be empty on the wire (client splices); got %q", got.PKI.Key)
	}

	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if crt.Name() != "laptop" {
		t.Errorf("cert name = %q, want laptop", crt.Name())
	}
	if groups := crt.Groups(); len(groups) != 1 || groups[0] != mesh.GroupAdmins {
		t.Errorf("cert groups = %v, want [%s]", groups, mesh.GroupAdmins)
	}
	// Address derives from the (approved) name, not from the pubkey:
	// the same name always resolves to the same overlay address.
	wantIP, err := nebderive.DeviceIP(testMeshMaster(t), "laptop", nebSealSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if nets := crt.Networks(); len(nets) != 1 || nets[0].Addr() != wantIP {
		t.Errorf("cert networks = %v, want %s", crt.Networks(), wantIP)
	}
	ca, err := nebderive.CACert(testMeshMaster(t))
	if err != nil {
		t.Fatal(err)
	}
	pool := cert.NewCAPool()
	if err := pool.AddCA(ca); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.VerifyCertificate(time.Now(), crt); err != nil {
		t.Errorf("issued cert does not verify against the derived CA: %v", err)
	}
	if want := crt.NotBefore().Add(mesh.ClockSkew + mesh.DeviceCertValidity); !crt.NotAfter().Equal(want) {
		t.Errorf("validity window = %s..%s, want %s wide", crt.NotBefore(), crt.NotAfter(), mesh.DeviceCertValidity)
	}
}

// TestDeviceNebulaConfigParses: nebula's own loader must accept what
// enrollment hands a device once the client has spliced its key in.
// A key-less config wouldn't parse; splice a raw inline key first.
func TestDeviceNebulaConfigParses(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	priv, pubHex := makeDeviceKeypair(t)
	code, body := meshEnroll(t, ts.URL, "laptop", mesh.GroupAdmins, pubHex)
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}
	keyPEM := nebderive.HostKeyPEM(priv)
	// nebup-style splice: pki.key is emitted as an empty scalar; find
	// the actual indentation from the sibling ca: line and match it.
	lines := strings.Split(body, "\n")
	var indent, keyLine string
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		if strings.HasPrefix(trimmed, "key:") {
			indent = l[:len(l)-len(trimmed)]
			keyLine = lines[i]
			break
		}
	}
	if keyLine == "" {
		t.Fatalf("no key: line in rendered config:\n%s", body)
	}
	var replacement strings.Builder
	replacement.WriteString(indent + "key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(keyPEM), "\n"), "\n") {
		replacement.WriteString(indent + "    " + line + "\n")
	}
	spliced := strings.Replace(body, keyLine+"\n", replacement.String(), 1)

	var c config.C
	if err := c.LoadString(spliced); err != nil {
		t.Fatalf("nebula cannot parse the enrolled config: %v\n%s", err, spliced)
	}
	if _, err := nebula.Main(&c, true, "nebenroll-test", logging.NewLogger(io.Discard), overlay.NewUserDeviceFromConfig); err != nil {
		t.Fatalf("nebula rejected the enrolled config: %v\n%s", err, spliced)
	}
}

// TestMeshEnrollGroupFollowsSignature: the group is one field in the
// signed message, so a caller can request media just as easily as
// admins — but a mismatch between the requested group and the signed
// message must be refused. This is what keeps a shared-space device
// off the nodes when the approver picks media.
func TestMeshEnrollGroupFollowsSignature(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)

	code, body := meshEnroll(t, ts.URL, "androidtv", mesh.GroupMedia, pubHex)
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}
	var got mesh.ConfigYAML
	if err := yaml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if groups := crt.Groups(); len(groups) != 1 || groups[0] != mesh.GroupMedia {
		t.Fatalf("cert groups = %v, want [%s]", groups, mesh.GroupMedia)
	}
}

// TestMeshEnrollIsIdempotentSameKey: -reenroll with the same device key
// yields a config with the same identity — same address, same pubkey
// in the cert. Only the validity window may differ.
func TestMeshEnrollIsIdempotentSameKey(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)

	first := mustEnroll(t, ts.URL, "laptop", mesh.GroupAdmins, pubHex)
	second := mustEnroll(t, ts.URL, "laptop", mesh.GroupAdmins, pubHex)
	if first.PKI.CA != second.PKI.CA {
		t.Error("re-enrolling produced a different CA")
	}
	a, _, err := cert.UnmarshalCertificateFromPEM([]byte(first.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := cert.UnmarshalCertificateFromPEM([]byte(second.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if a.Networks()[0] != b.Networks()[0] {
		t.Errorf("re-enrolling moved the device: %v then %v", a.Networks(), b.Networks())
	}
}

// TestMeshEnrollTopology: a device finds the mesh through the hub and
// nothing else, and takes an OS-assigned port because it roams.
func TestMeshEnrollTopology(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)
	got := mustEnroll(t, ts.URL, "laptop", mesh.GroupAdmins, pubHex)

	hubIP, err := nebderive.HubIP(nebSealSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if eps := got.StaticHostMap[hubIP.String()]; len(eps) != 1 || eps[0] != nebTestEndpoint {
		t.Errorf("static_host_map[%s] = %v, want [%s]", hubIP, eps, nebTestEndpoint)
	}
	if got.Lighthouse.AmLighthouse {
		t.Error("a device is not a lighthouse")
	}
	if got.Listen.Port != 0 {
		t.Errorf("listen.port = %d, want 0 (devices roam)", got.Listen.Port)
	}
	if len(got.Firewall.Inbound) != 1 || got.Firewall.Inbound[0].Proto != "icmp" {
		t.Errorf("device inbound rules = %+v, want icmp only", got.Firewall.Inbound)
	}
}

// TestMeshEnrollRejects covers the failure paths: a bad signature; a
// replayed nonce; and a name that collides with the git zone (a
// machine label) — the last is the collision-check ADR-0012 requires.
func TestMeshEnrollRejects(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)

	// Wrong signature: sign a different nonce, replay against a fresh
	// challenge. The hub rebuilds the message from server state and
	// checks that recover(signed) matches an allowlisted address.
	resp, err := http.PostForm(ts.URL+"/mesh/enroll/challenge", url.Values{
		"name": {"laptop"}, "group": {mesh.GroupAdmins}, "pubkey": {pubHex},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var ch struct{ Name, Group, Nonce, Fingerprint, Message string }
	if err := json.Unmarshal(body, &ch); err != nil {
		t.Fatal(err)
	}
	badMsg := meshEnrollMessageV1("laptop", mesh.GroupAdmins, ch.Fingerprint, "not-the-nonce")
	badSig := personalSign(t, testKey(t), badMsg)
	badResp, err := http.PostForm(ts.URL+"/mesh/enroll", url.Values{
		"name": {ch.Name}, "group": {ch.Group}, "pubkey": {pubHex},
		"nonce": {ch.Nonce}, "signature": {badSig},
	})
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong-nonce signature: got %d, want 403", badResp.StatusCode)
	}

	// Correct signature, redeemed twice: the second must fail.
	sig := personalSign(t, testKey(t), ch.Message)
	form := url.Values{
		"name": {ch.Name}, "group": {ch.Group}, "pubkey": {pubHex},
		"nonce": {ch.Nonce}, "signature": {sig},
	}
	first, err := http.PostForm(ts.URL+"/mesh/enroll", form)
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first redeem: got %d", first.StatusCode)
	}
	second, err := http.PostForm(ts.URL+"/mesh/enroll", form)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusForbidden {
		t.Errorf("replayed nonce: got %d, want 403", second.StatusCode)
	}

	// Colliding name: the well-known test tree has a machine at
	// "aa-bb-cc-dd-ee-ff" (nameless, so its label is the MAC with
	// dashes). A device asking for that label must be refused.
	if code, _ := meshEnroll(t, ts.URL, "aa-bb-cc-dd-ee-ff", mesh.GroupAdmins, pubHex); code != http.StatusForbidden {
		t.Errorf("git-zone-colliding name: got %d, want 403", code)
	}
}

// TestMeshEnrollSealed: while sealed there is no master, so there is
// nothing to derive an identity from — say so rather than 500.
func TestMeshEnrollSealed(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	// leave sealed
	s := &server{
		root:       m.root,
		store:      deviceflow.NewStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		hub:        m,
	}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	_, pubHex := makeDeviceKeypair(t)
	resp, err := http.PostForm(ts.URL+"/mesh/enroll/challenge", url.Values{
		"name": {"laptop"}, "group": {mesh.GroupAdmins}, "pubkey": {pubHex},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("sealed challenge: got %d, want 503", resp.StatusCode)
	}
}

// TestMeshEnrollDisabled: no mesh, no route.
func TestMeshEnrollDisabled(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	m.mesh = nil // hub without a mesh (tests only; main refuses this)
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
	defer ts.Close()

	_, pubHex := makeDeviceKeypair(t)
	resp, err := http.PostForm(ts.URL+"/mesh/enroll/challenge", url.Values{
		"name": {"laptop"}, "group": {mesh.GroupAdmins}, "pubkey": {pubHex},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("mesh disabled: got %d, want 404", resp.StatusCode)
	}
}

func mustEnroll(t *testing.T, base, name, group, pubHex string) mesh.ConfigYAML {
	t.Helper()
	code, body := meshEnroll(t, base, name, group, pubHex)
	if code != http.StatusOK {
		t.Fatalf("enroll %q: got %d: %s", name, code, body)
	}
	var got mesh.ConfigYAML
	if err := yaml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// testMeshMaster is the master the well-known test wallet's unseal
// signature derives.
func testMeshMaster(t *testing.T) []byte {
	t.Helper()
	master, err := masterderive.MasterFromSignatureHex(unsealSig(t))
	if err != nil {
		t.Fatal(err)
	}
	return master
}

// TestDeviceConfigOmitsTunDevAndFiltersRemotes covers the two
// portability fixes to the enrolled device config: no tun dev name
// (Darwin rejects any tun name not utun[0-9]+), and a remote allow
// list that keeps the client from dialling a peer's pod-network
// address.
func TestDeviceConfigOmitsTunDevAndFiltersRemotes(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)
	code, body := meshEnroll(t, ts.URL, "laptop", mesh.GroupAdmins, pubHex)
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}

	var got mesh.ConfigYAML
	if err := yaml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tun == nil {
		t.Fatal("device config has no tun block")
	}
	if got.Tun.Dev != "" {
		t.Errorf("tun.dev = %q, want unset: a named device is rejected on Darwin", got.Tun.Dev)
	}
	if strings.Contains(body, "dev:") {
		t.Errorf("rendered config still carries a dev key:\n%s", body)
	}
	if got.Lighthouse.RemoteAllowList == nil {
		t.Fatal("remote_allow_list not set — a device could dial a peer's pod-network address")
	}
	for _, denied := range []string{"10.244.0.0/16"} {
		if allowed, ok := got.Lighthouse.RemoteAllowList[denied]; !ok || allowed {
			t.Errorf("remote_allow_list: %s not denied", denied)
		}
	}
}

// startDeviceFlow begins an RFC 8628 mesh enrollment and returns the
// parsed start response.
func startDeviceFlow(t *testing.T, base, pubHex, proposedName, proposedGroup string) (deviceCode, userCode, fingerprint string) {
	t.Helper()
	resp, err := http.PostForm(base+"/mesh/enroll/device", url.Values{
		"pubkey": {pubHex}, "proposed_name": {proposedName}, "proposed_group": {proposedGroup},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start device flow: got %d: %s", resp.StatusCode, body)
	}
	var start struct {
		DeviceCode  string `json:"device_code"`
		UserCode    string `json:"user_code"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(body, &start); err != nil {
		t.Fatal(err)
	}
	return start.DeviceCode, start.UserCode, start.Fingerprint
}

// approveMeshEnroll posts the operator's approval with a fresh status
// session. sig must be over the v1 message for (name, group).
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

// pollToken polls /token once and returns (accessToken, oauthError).
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

// TestMeshEnrollDeviceFlow is the RFC 8628 path end to end: device
// submits its pubkey → operator approves on /status with a wallet
// signature over the v1 message (final name differs from the proposal
// — rubber-stamp resistance) → device polls /token → bearer token
// redeems the minted config exactly once.
func TestMeshEnrollDeviceFlow(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)

	deviceCode, userCode, fp := startDeviceFlow(t, ts.URL, pubHex, "TV ", mesh.GroupMedia)

	// Approver picks a different final name than the device proposed.
	nonce, err := s.store.NonceFor(userCode)
	if err != nil {
		t.Fatal(err)
	}
	sig := personalSign(t, testKey(t), meshEnrollMessageV1("livingroom", mesh.GroupMedia, fp, nonce))
	resp := approveMeshEnroll(t, s, ts.URL, url.Values{
		"user_code": {userCode}, "name": {"livingroom"},
		"group": {mesh.GroupMedia}, "signature": {sig},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve: got %d, want 303", resp.StatusCode)
	}

	token, oauthErr := pollToken(t, ts.URL, deviceCode)
	if oauthErr != "" || token == "" {
		t.Fatalf("token poll: err=%q", oauthErr)
	}

	req, err := http.NewRequest("GET", ts.URL+"/mesh/enroll/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	cfgResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(cfgResp.Body)
	cfgResp.Body.Close()
	if cfgResp.StatusCode != http.StatusOK {
		t.Fatalf("config redeem: got %d: %s", cfgResp.StatusCode, body)
	}
	var got mesh.ConfigYAML
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if crt.Name() != "livingroom" {
		t.Errorf("cert name = %q, want the approver's name, not the proposal", crt.Name())
	}
	if groups := crt.Groups(); len(groups) != 1 || groups[0] != mesh.GroupMedia {
		t.Errorf("cert groups = %v, want [media]", groups)
	}
	if got.PKI.Key != "" {
		t.Errorf("pki.key must be empty on the wire, got %q", got.PKI.Key)
	}

	// Single use: the second redeem must fail.
	again, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusForbidden {
		t.Errorf("second config redeem: got %d, want 403", again.StatusCode)
	}
}

// TestMeshEnrollApproveAdminsRequiresRetype: promoting a device-flow
// enrollment to admins demands the operator re-type the final name;
// without it the flow stays pending.
func TestMeshEnrollApproveAdminsRequiresRetype(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)
	deviceCode, userCode, fp := startDeviceFlow(t, ts.URL, pubHex, "console", mesh.GroupAdmins)

	nonce, err := s.store.NonceFor(userCode)
	if err != nil {
		t.Fatal(err)
	}
	sig := personalSign(t, testKey(t), meshEnrollMessageV1("console", mesh.GroupAdmins, fp, nonce))

	// No admin_retype: refused, flow still pending.
	approveMeshEnroll(t, s, ts.URL, url.Values{
		"user_code": {userCode}, "name": {"console"},
		"group": {mesh.GroupAdmins}, "signature": {sig},
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
		"group": {mesh.GroupAdmins}, "signature": {sig},
	})
	if _, ok := s.pendingMeshEnroll(userCode); ok {
		t.Fatal("flow should be approved after the retype")
	}
}

// TestVerifyRefusesMeshEnrollApprove: the generic /verify approve path
// must not approve a mesh enrollment — it would hand the device a
// token that redeems to nothing. Deny remains allowed.
func TestVerifyRefusesMeshEnrollApprove(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	_, pubHex := makeDeviceKeypair(t)
	deviceCode, userCode, _ := startDeviceFlow(t, ts.URL, pubHex, "tv", mesh.GroupMedia)

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
