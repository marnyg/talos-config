package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slackhq/nebula"
	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

var nebSealSubnet = netip.MustParsePrefix("10.42.0.0/16")

// testTalosTree writes a minimal talos/ tree holding one machine named
// cp1, which is enough for the zone to be non-trivial.
func testTalosTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "machines", "aa-bb-cc-dd-ee-01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := "ip: 127.0.0.1\nname: cp1\nconfig: base.yaml\npatches: []\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	base := "version: v1alpha1\nmachine:\n  type: worker\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// testNebManager returns a mesh manager whose nebula start is stubbed:
// everything up to and including config rendering runs for real, only
// the UDP socket is skipped.
func testNebManager(t *testing.T, root string, devices []string) (*nebManager, *[]byte) {
	t.Helper()
	var rendered []byte
	m := newNebManager(4242, nebSealSubnet, "0.0.0.0", nebTestEndpoint, meshDNSZone, root, adminDevices(devices...))
	m.start = func(cfg []byte) (*nebstack.Service, error) {
		rendered = cfg
		return nil, nil
	}
	return m, &rendered
}

func TestNebManagerUnseal(t *testing.T) {
	root := testTalosTree(t)
	m, rendered := testNebManager(t, root, []string{"laptop", "phone"})
	master := []byte("neb-seal-test-master-key-32bytes")

	if m.up() {
		t.Fatal("mesh should not be up before unseal")
	}
	if err := m.unsealWithMaster(master); err != nil {
		t.Fatal(err)
	}

	// The stub returns a nil service, so up() stays false; what matters
	// is that the config was rendered from the master and the zone built.
	if len(*rendered) == 0 {
		t.Fatal("no nebula config was rendered")
	}
	caPEM, err := nebderive.CACertPEM(master)
	if err != nil {
		t.Fatal(err)
	}
	// Compare through the YAML, not as a substring: the PEM is emitted as
	// an indented block scalar, so the raw bytes never appear verbatim.
	var cfg nebConfigYAML
	if err := yaml.Unmarshal(*rendered, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.PKI.CA != string(caPEM) {
		t.Error("rendered config does not carry the derived CA")
	}
	if !cfg.Lighthouse.AmLighthouse || !cfg.Relay.AmRelay {
		t.Error("hub did not render as lighthouse + relay")
	}

	_, zone, err := m.state()
	if err != nil {
		t.Fatal(err)
	}
	hubIP, err := nebderive.HubIP(nebSealSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if zone[nebderive.HubName] != hubIP {
		t.Errorf("zone hub = %s, want %s", zone[nebderive.HubName], hubIP)
	}
	for _, name := range []string{"laptop", "phone"} {
		if !zone[name].IsValid() {
			t.Errorf("device %q missing from the zone", name)
		}
	}
}

// A bad zone must fail before nebula starts: a duplicate label is a repo
// mistake, and it should surface while no overlay exists rather than
// after certs have been minted against it.
func TestNebManagerZoneFailsBeforeStart(t *testing.T) {
	root := testTalosTree(t)
	m, rendered := testNebManager(t, root, []string{"cp1"}) // collides with the machine's name
	started := false
	m.start = func(cfg []byte) (*nebstack.Service, error) {
		started = true
		return nil, nil
	}

	err := m.unsealWithMaster([]byte("neb-seal-test-master-key-32bytes"))
	if err == nil {
		t.Fatal("expected a zone collision error")
	}
	if started {
		t.Error("nebula was started despite an invalid zone")
	}
	if len(*rendered) != 0 {
		t.Error("config was rendered despite an invalid zone")
	}
	if _, _, stored := m.state(); stored == nil {
		t.Error("failure was not recorded on the manager")
	}
}

// Zone building is skipped entirely when mesh DNS is off, and the
// rendered config must not open udp/53.
func TestNebManagerWithoutDNS(t *testing.T) {
	root := testTalosTree(t)
	m, rendered := testNebManager(t, root, nil)
	m.dnsZone = ""

	if err := m.unsealWithMaster([]byte("neb-seal-test-master-key-32bytes")); err != nil {
		t.Fatal(err)
	}
	if _, zone, _ := m.state(); zone != nil {
		t.Errorf("zone built despite mesh DNS being off: %v", zone)
	}
	if strings.Contains(string(*rendered), `port: "53"`) {
		t.Error("udp/53 opened despite mesh DNS being off")
	}
}

func TestNebManagerUnsealIsIdempotent(t *testing.T) {
	root := testTalosTree(t)
	m, _ := testNebManager(t, root, nil)
	calls := 0
	// A non-nil service is needed for the idempotence guard to engage,
	// and Service is opaque, so drive it through the field directly after
	// the first unseal.
	m.start = func(cfg []byte) (*nebstack.Service, error) {
		calls++
		return nil, nil
	}
	master := []byte("neb-seal-test-master-key-32bytes")
	if err := m.unsealWithMaster(master); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.svc = &nebstack.Service{} // stand in for a running mesh
	m.mu.Unlock()
	if err := m.unsealWithMaster(master); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("nebula started %d times, want 1", calls)
	}
}

// A mesh that cannot start must not take the unseal with it: KMS disk
// unlocks ride the WAN listener and must not depend on the overlay
// (invariant 4). The failure is recorded, not swallowed.
func TestMeshFailureDoesNotBreakHubUnseal(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	mesh, _ := testNebManager(t, m.root, nil)
	mesh.start = func([]byte) (*nebstack.Service, error) {
		return nil, errors.New("simulated mesh failure")
	}
	m.mesh = mesh

	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatalf("mesh failure broke the hub unseal: %v", err)
	}
	if m.sealed() {
		t.Fatal("hub still sealed after a mesh-only failure")
	}
	if _, _, err := mesh.state(); err == nil {
		t.Error("mesh failure was not recorded")
	}
}

// /sealed pages on a mesh startup failure — the phase-2 inversion: the
// mesh is the control channel now, so "unsealed but mesh down" is an
// incident, not a footnote.
func TestSealedEndpointPagesOnMeshFailure(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	mesh, _ := testNebManager(t, m.root, nil)
	mesh.start = func([]byte) (*nebstack.Service, error) {
		return nil, errors.New("simulated mesh failure")
	}
	m.mesh = mesh
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

// adminDevices is the common case in tests: named devices, all in the
// admins group.
func adminDevices(names ...string) []nebDevice {
	var out []nebDevice
	for _, n := range names {
		out = append(out, nebDevice{name: nebderive.Normalize(n), group: nebGroupAdmins})
	}
	return out
}

func TestParseMeshDevices(t *testing.T) {
	got, err := parseMeshDevices(" Laptop , ,phone ", "ANDROIDTV")
	if err != nil {
		t.Fatal(err)
	}
	want := []nebDevice{
		{name: "laptop", group: nebGroupAdmins},
		{name: "phone", group: nebGroupAdmins},
		{name: "androidtv", group: nebGroupMedia},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestParseMeshDevicesRejectsBothGroups: the group is signed into the
// cert, so a name in both lists has no single answer and must not get a
// guessed one — guessing is how a shared-space TV ends up an admin.
func TestParseMeshDevicesRejectsBothGroups(t *testing.T) {
	if _, err := parseMeshDevices("laptop,androidtv", "androidtv"); err == nil {
		t.Fatal("expected an error for a device declared in both groups, got none")
	}
}

// TestMeshMemberRows: the pure join of derived membership × live
// hostmap. The hub is self, a live member shows its WAN endpoint and
// who it relays to (by name), and an offline member still appears —
// membership comes from derivation, not from who is connected.
func TestMeshMemberRows(t *testing.T) {
	hubIP := netip.MustParseAddr("10.42.0.1")
	cp1IP := netip.MustParseAddr("10.42.218.125")
	lapIP := netip.MustParseAddr("10.42.117.202")
	tvIP := netip.MustParseAddr("10.42.33.44")
	zone := map[string]netip.Addr{
		nebderive.HubName: hubIP,
		"cp1":             cp1IP,
		"laptop":          lapIP,
		"androidtv":       tvIP,
	}
	devices := []nebDevice{
		{name: "laptop", group: nebGroupAdmins},
		{name: "androidtv", group: nebGroupMedia},
	}
	live := []nebula.ControlHostInfo{
		{
			VpnAddrs:               []netip.Addr{lapIP},
			CurrentRemote:          netip.MustParseAddrPort("198.51.100.7:4242"),
			CurrentRelaysThroughMe: []netip.Addr{cp1IP},
		},
	}

	got := meshMemberRows(zone, devices, "hub.example:4242", live)
	want := []meshMemberRow{
		{Name: "androidtv", Group: nebGroupMedia, Addr: tvIP.String(), Tunnel: "—", Endpoint: "—", Relays: "—"},
		{Name: "cp1", Group: nebGroupMachines, Addr: cp1IP.String(), Tunnel: "—", Endpoint: "—", Relays: "—"},
		{Name: "hub", Group: "lighthouse+relay", Addr: hubIP.String(), Tunnel: "self", Endpoint: "hub.example:4242", Relays: "—"},
		{Name: "laptop", Group: nebGroupAdmins, Addr: lapIP.String(), Tunnel: "up", Endpoint: "198.51.100.7:4242", Relays: "cp1"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestNebManagerMembers: before unseal there is no zone and therefore
// no membership; after unseal every derived member is listed even with
// nebula stubbed out (nothing is live, everything still appears).
func TestNebManagerMembers(t *testing.T) {
	root := testTalosTree(t)
	m, _ := testNebManager(t, root, []string{"laptop"})

	if rows := m.members(); rows != nil {
		t.Fatalf("sealed manager should report no members, got %v", rows)
	}
	if err := m.unsealWithMaster([]byte("neb-seal-test-master-key-32bytes")); err != nil {
		t.Fatal(err)
	}
	rows := m.members()
	byName := map[string]meshMemberRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if r := byName["hub"]; r.Tunnel != "self" || r.Endpoint != nebTestEndpoint {
		t.Errorf("hub row = %+v", r)
	}
	if r := byName["cp1"]; r.Group != nebGroupMachines || r.Tunnel != "—" {
		t.Errorf("cp1 row = %+v", r)
	}
	if r := byName["laptop"]; r.Group != nebGroupAdmins || r.Tunnel != "—" {
		t.Errorf("laptop row = %+v", r)
	}
}
