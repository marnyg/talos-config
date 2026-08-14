package mesh

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/cert"
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
func testNebManager(t *testing.T, root string) (*Manager, *[]byte) {
	t.Helper()
	var rendered []byte
	m := NewManager(4242, nebSealSubnet, "0.0.0.0", nebTestEndpoint, nebderive.DNSZone, root)
	m.Start = func(cfg []byte) (*nebstack.Service, error) {
		rendered = cfg
		return nil, nil
	}
	return m, &rendered
}

func TestNebManagerUnseal(t *testing.T) {
	root := testTalosTree(t)
	m, rendered := testNebManager(t, root)
	master := []byte("neb-seal-test-master-key-32bytes")

	if m.Up() {
		t.Fatal("mesh should not be up before unseal")
	}
	if err := m.UnsealWithMaster(master); err != nil {
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
	var cfg ConfigYAML
	if err := yaml.Unmarshal(*rendered, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.PKI.CA != string(caPEM) {
		t.Error("rendered config does not carry the derived CA")
	}
	if !cfg.Lighthouse.AmLighthouse || !cfg.Relay.AmRelay {
		t.Error("hub did not render as lighthouse + relay")
	}

	_, zone, err := m.State()
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
	// Under ADR-0012, devices are not in the git-derived zone.
	for _, name := range []string{"laptop", "phone"} {
		if _, ok := zone[name]; ok {
			t.Errorf("device %q leaked into the git-derived zone", name)
		}
	}
}

// A bad zone must fail before nebula starts: a duplicate label is a repo
// mistake, and it should surface while no overlay exists rather than
// after certs have been minted against it.
//
// Under ADR-0012 devices are not part of the git zone, so collisions
// only come from the machines directory. Simulate by putting two
// machines under the same DNS name in the tree.
func TestNebManagerZoneFailsBeforeStart(t *testing.T) {
	root := t.TempDir()
	// Two machines both named cp1 → label collision.
	for _, mac := range []string{"aa-bb-cc-dd-ee-01", "aa-bb-cc-dd-ee-02"} {
		dir := filepath.Join(root, "machines", mac)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		meta := "ip: 127.0.0.1\nname: cp1\nconfig: base.yaml\npatches: []\n"
		if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := "version: v1alpha1\nmachine:\n  type: worker\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}

	m, rendered := testNebManager(t, root)
	started := false
	m.Start = func(cfg []byte) (*nebstack.Service, error) {
		started = true
		return nil, nil
	}

	err := m.UnsealWithMaster([]byte("neb-seal-test-master-key-32bytes"))
	if err == nil {
		t.Fatal("expected a zone collision error")
	}
	if started {
		t.Error("nebula was started despite an invalid zone")
	}
	if len(*rendered) != 0 {
		t.Error("config was rendered despite an invalid zone")
	}
	if _, _, stored := m.State(); stored == nil {
		t.Error("failure was not recorded on the manager")
	}
}

// Zone building is skipped entirely when mesh DNS is off, and the
// rendered config must not open udp/53.
func TestNebManagerWithoutDNS(t *testing.T) {
	root := testTalosTree(t)
	m, rendered := testNebManager(t, root)
	m.dnsZone = ""

	if err := m.UnsealWithMaster([]byte("neb-seal-test-master-key-32bytes")); err != nil {
		t.Fatal(err)
	}
	if _, zone, _ := m.State(); zone != nil {
		t.Errorf("zone built despite mesh DNS being off: %v", zone)
	}
	if strings.Contains(string(*rendered), `port: "53"`) {
		t.Error("udp/53 opened despite mesh DNS being off")
	}
}

func TestNebManagerUnsealIsIdempotent(t *testing.T) {
	root := testTalosTree(t)
	m, _ := testNebManager(t, root)
	calls := 0
	// A non-nil service is needed for the idempotence guard to engage,
	// and Service is opaque, so drive it through the field directly after
	// the first unseal.
	m.Start = func(cfg []byte) (*nebstack.Service, error) {
		calls++
		return nil, nil
	}
	master := []byte("neb-seal-test-master-key-32bytes")
	if err := m.UnsealWithMaster(master); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.svc = &nebstack.Service{} // stand in for a running mesh
	m.mu.Unlock()
	if err := m.UnsealWithMaster(master); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("nebula started %d times, want 1", calls)
	}
}

// fakeCert stands in for a real nebula cert on tests that only need
// Name() and Groups(); the other cert.Certificate methods are unused
// by memberRows and dnsRespond.
type fakeCert struct {
	cert.Certificate
	name   string
	groups []string
}

func (f fakeCert) Name() string     { return f.name }
func (f fakeCert) Groups() []string { return f.groups }

// TestMeshMemberRows: hub + machines come from the (git-derived) zone;
// live devices come from the peer map's certs (ADR-0012 — devices are
// not enumerable from git).
func TestMeshMemberRows(t *testing.T) {
	hubIP := netip.MustParseAddr("10.42.0.1")
	cp1IP := netip.MustParseAddr("10.42.218.125")
	lapIP := netip.MustParseAddr("10.42.117.202")
	tvIP := netip.MustParseAddr("10.42.33.44")
	zone := map[string]netip.Addr{
		nebderive.HubName: hubIP,
		"cp1":             cp1IP,
	}
	live := []nebula.ControlHostInfo{
		{
			VpnAddrs:               []netip.Addr{lapIP},
			Cert:                   fakeCert{name: "laptop", groups: []string{GroupAdmins}},
			CurrentRemote:          netip.MustParseAddrPort("198.51.100.7:4242"),
			CurrentRelaysThroughMe: []netip.Addr{cp1IP},
		},
		{
			VpnAddrs: []netip.Addr{tvIP},
			Cert:     fakeCert{name: "androidtv", groups: []string{GroupMedia}},
		},
	}

	got := memberRows(zone, "hub.example:4242", live)
	want := []MemberRow{
		{Name: "cp1", Group: GroupMachines, Addr: cp1IP.String(), Tunnel: "—", Endpoint: "—", Relays: "—"},
		{Name: "hub", Group: "lighthouse+relay", Addr: hubIP.String(), Tunnel: "self", Endpoint: "hub.example:4242", Relays: "—"},
		{Name: "androidtv", Group: GroupMedia, Addr: tvIP.String(), Tunnel: "up", Endpoint: "—", Relays: "—"},
		{Name: "laptop", Group: GroupAdmins, Addr: lapIP.String(), Tunnel: "up", Endpoint: "198.51.100.7:4242", Relays: "cp1"},
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
// no membership; after unseal the hub + declared machines are listed
// even with nebula stubbed out. Devices only appear while their tunnel
// is live (not exercised here — nebula is stubbed to nil).
func TestNebManagerMembers(t *testing.T) {
	root := testTalosTree(t)
	m, _ := testNebManager(t, root)

	if rows := m.Members(); rows != nil {
		t.Fatalf("sealed manager should report no members, got %v", rows)
	}
	if err := m.UnsealWithMaster([]byte("neb-seal-test-master-key-32bytes")); err != nil {
		t.Fatal(err)
	}
	rows := m.Members()
	byName := map[string]MemberRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if r := byName["hub"]; r.Tunnel != "self" || r.Endpoint != nebTestEndpoint {
		t.Errorf("hub row = %+v", r)
	}
	if r := byName["cp1"]; r.Group != GroupMachines || r.Tunnel != "—" {
		t.Errorf("cp1 row = %+v", r)
	}
	if _, present := byName["laptop"]; present {
		t.Errorf("device leaked into Members() without a live tunnel (ADR-0012)")
	}
}
