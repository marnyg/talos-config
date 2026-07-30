package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/cert"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// newMeshEnrollServer returns an unsealed server whose mesh declares one
// admin device (laptop) and one media device (androidtv).
func newMeshEnrollServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	m := testHubManager(t, []string{wellKnownAddr}, "")
	mesh, _ := testNebManager(t, m.root, nil)
	mesh.devices = []nebDevice{
		{name: "laptop", group: nebGroupAdmins},
		{name: "androidtv", group: nebGroupMedia},
	}
	m.mesh = mesh
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	s := &server{
		root:       m.root,
		store:      newAuthStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		hub:        m,
	}
	ts := httptest.NewServer(s.mux())
	t.Cleanup(ts.Close)
	return s, ts
}

// meshEnroll runs the whole challenge → signature → config exchange.
func meshEnroll(t *testing.T, base, name string) (int, string) {
	t.Helper()
	code, body := get(t, http.DefaultClient, base+"/mesh/enroll?name="+url.QueryEscape(name))
	if code != http.StatusOK {
		return code, body
	}
	var ch struct{ Name, Group, Nonce, Message string }
	if err := json.Unmarshal([]byte(body), &ch); err != nil {
		t.Fatal(err)
	}
	sig := personalSign(t, testKey(t), ch.Message)
	resp, err := http.PostForm(base+"/mesh/enroll", url.Values{
		"name": {ch.Name}, "nonce": {ch.Nonce}, "signature": {sig},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(out)
}

// TestMeshEnrollIssuesDeviceIdentity is the happy path: a wallet-signed
// challenge returns a self-contained config whose cert is the derived
// device identity in the group the hub declared for it.
func TestMeshEnrollIssuesDeviceIdentity(t *testing.T) {
	_, ts := newMeshEnrollServer(t)

	code, body := meshEnroll(t, ts.URL, "laptop")
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}

	var got nebConfigYAML
	if err := yaml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	// Self-contained: inline PEM, not paths. A device has nowhere to put
	// side files, and one file is what transfers by clipboard or QR.
	for name, field := range map[string]string{"ca": got.PKI.CA, "cert": got.PKI.Cert, "key": got.PKI.Key} {
		if !strings.HasPrefix(field, "-----BEGIN") {
			t.Errorf("pki.%s is not inline PEM: %q", name, field)
		}
	}

	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if crt.Name() != "laptop" {
		t.Errorf("cert name = %q, want laptop", crt.Name())
	}
	if groups := crt.Groups(); len(groups) != 1 || groups[0] != nebGroupAdmins {
		t.Errorf("cert groups = %v, want [%s]", groups, nebGroupAdmins)
	}
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
	if want := crt.NotBefore().Add(nebClockSkew + nebDeviceCertValidity); !crt.NotAfter().Equal(want) {
		t.Errorf("validity window = %s..%s, want %s wide", crt.NotBefore(), crt.NotAfter(), nebDeviceCertValidity)
	}
}

// TestDeviceNebulaConfigValidates is the load-bearing test for the third
// config shape (after the hub's and the nodes'): nebula's own validation
// accepts what enrollment hands a device. No path rewriting needed here
// — the key material is inline, which is the point.
func TestDeviceNebulaConfigValidates(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	code, body := meshEnroll(t, ts.URL, "laptop")
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}

	var c config.C
	if err := c.LoadString(body); err != nil {
		t.Fatalf("nebula cannot parse the enrolled config: %v\n%s", err, body)
	}
	if _, err := nebula.Main(&c, true, "nebenroll-test", logging.NewLogger(io.Discard), overlay.NewUserDeviceFromConfig); err != nil {
		t.Fatalf("nebula rejected the enrolled config: %v\n%s", err, body)
	}
}

// TestMeshEnrollGroupFollowsDeclaration: the media device must not get an
// admin cert. This is the one property that keeps a shared-space TV off
// the nodes, and it is decided by the hub, never by the client.
func TestMeshEnrollGroupFollowsDeclaration(t *testing.T) {
	_, ts := newMeshEnrollServer(t)

	code, body := meshEnroll(t, ts.URL, "androidtv")
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}
	var got nebConfigYAML
	if err := yaml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if groups := crt.Groups(); len(groups) != 1 || groups[0] != nebGroupMedia {
		t.Fatalf("cert groups = %v, want [%s] \u2014 a shared-space device must not be an admin", groups, nebGroupMedia)
	}
}

// TestMeshEnrollIsIdempotent: re-running enrollment after a device wipe
// must return the same identity, since it is derived rather than
// registered. Only the certificate's validity window may differ.
func TestMeshEnrollIsIdempotent(t *testing.T) {
	_, ts := newMeshEnrollServer(t)

	first := mustEnroll(t, ts.URL, "laptop")
	second := mustEnroll(t, ts.URL, "laptop")
	if first.PKI.Key != second.PKI.Key {
		t.Error("re-enrolling produced a different key: the identity is not derived")
	}
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

// TestMeshEnrollTopology: a device finds the mesh the same way a node
// does — through the hub, pinning nothing else — but roams, so it takes
// an OS-assigned port.
func TestMeshEnrollTopology(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	got := mustEnroll(t, ts.URL, "laptop")

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
	// Stateful firewall: only unsolicited traffic needs a rule, and the
	// only unsolicited traffic a device wants is a ping.
	if len(got.Firewall.Inbound) != 1 || got.Firewall.Inbound[0].Proto != "icmp" {
		t.Errorf("device inbound rules = %+v, want icmp only", got.Firewall.Inbound)
	}
}

// TestMeshEnrollRejects covers the ways enrollment must fail: an
// undeclared name (no group, so no access), a bad signature, and a
// replayed nonce.
func TestMeshEnrollRejects(t *testing.T) {
	_, ts := newMeshEnrollServer(t)

	if code, _ := meshEnroll(t, ts.URL, "attacker-laptop"); code != http.StatusNotFound {
		t.Errorf("undeclared device: got %d, want 404", code)
	}

	code, body := get(t, http.DefaultClient, ts.URL+"/mesh/enroll?name=laptop")
	if code != http.StatusOK {
		t.Fatalf("challenge: got %d: %s", code, body)
	}
	var ch struct{ Name, Nonce, Message string }
	if err := json.Unmarshal([]byte(body), &ch); err != nil {
		t.Fatal(err)
	}

	// A signature over someone else's challenge is not a signature over
	// this one.
	other := personalSign(t, testKey(t), meshEnrollMessage("laptop", "not-the-nonce"))
	resp, err := http.PostForm(ts.URL+"/mesh/enroll", url.Values{
		"name": {ch.Name}, "nonce": {ch.Nonce}, "signature": {other},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong-nonce signature: got %d, want 403", resp.StatusCode)
	}

	// Correct signature, redeemed twice: the second must fail.
	sig := personalSign(t, testKey(t), ch.Message)
	form := url.Values{"name": {ch.Name}, "nonce": {ch.Nonce}, "signature": {sig}}
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
}

// TestMeshEnrollSealed: while sealed there is no master, so there is
// nothing to derive an identity from — say so rather than 500.
func TestMeshEnrollSealed(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	mesh, _ := testNebManager(t, m.root, nil)
	mesh.devices = adminDevices("laptop")
	m.mesh = mesh
	s := &server{
		root:       m.root,
		store:      newAuthStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		hub:        m,
	}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	if code, _ := get(t, http.DefaultClient, ts.URL+"/mesh/enroll?name=laptop"); code != http.StatusServiceUnavailable {
		t.Errorf("sealed challenge: got %d, want 503", code)
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
		store:      newAuthStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		hub:        m,
	}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	if code, _ := get(t, http.DefaultClient, ts.URL+"/mesh/enroll?name=laptop"); code != http.StatusNotFound {
		t.Errorf("mesh disabled: got %d, want 404", code)
	}
}

func mustEnroll(t *testing.T, base, name string) nebConfigYAML {
	t.Helper()
	code, body := meshEnroll(t, base, name)
	if code != http.StatusOK {
		t.Fatalf("enroll %q: got %d: %s", name, code, body)
	}
	var got nebConfigYAML
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

// TestDeviceConfigOmitsTunDevAndFiltersRemotes covers the two portability
// fixes to the enrolled device config.
//
// The dev name: a device config is meant to move by scp, clipboard or QR
// to whatever client the owner has, and Darwin rejects any tun name that
// is not utun[0-9]+ ("interface name must be utun[0-9]+ on Darwin,
// ignoring"). Omitting it lets every client pick its own.
//
// The remote filter: a device must never dial a peer's wg0 or pod-network
// address — that is how nebula ends up tunnelled inside wireguard.
func TestDeviceConfigOmitsTunDevAndFiltersRemotes(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	code, body := meshEnroll(t, ts.URL, "laptop")
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}

	var got nebConfigYAML
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
	for _, denied := range []string{nebPodSubnet} {
		if allowed, ok := got.Lighthouse.RemoteAllowList[denied]; !ok || allowed {
			t.Errorf("remote_allow_list: %s not denied", denied)
		}
	}
}
