package mesh

import (
	"net/netip"
	"testing"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

var nebDNSSubnet = netip.MustParsePrefix("10.42.0.0/16")

func TestBuildMeshZone(t *testing.T) {
	master := []byte("mesh-zone-test-master-key-32byte")
	byMAC := map[string]machines.Machine{
		"aa:bb:cc:dd:ee:01": {Name: "cp1"},
		"aa:bb:cc:dd:ee:02": {}, // no name: label is the MAC with dashes
	}
	zone, err := buildMeshZone(master, nebDNSSubnet, byMAC)
	if err != nil {
		t.Fatal(err)
	}

	hubIP, err := nebderive.HubIP(nebDNSSubnet)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]netip.Addr{nebderive.HubName: hubIP}
	for mac, label := range map[string]string{
		"aa:bb:cc:dd:ee:01": "cp1",
		"aa:bb:cc:dd:ee:02": "aa-bb-cc-dd-ee-02",
	} {
		ip, err := nebderive.MachineIP(master, mac, nebDNSSubnet)
		if err != nil {
			t.Fatal(err)
		}
		want[label] = ip
	}
	// Devices are not in the git-derived zone under ADR-0012.

	if len(zone) != len(want) {
		t.Fatalf("zone has %d names, want %d: %v", len(zone), len(want), zone)
	}
	for label, ip := range want {
		if zone[label] != ip {
			t.Errorf("zone[%q] = %s, want %s", label, zone[label], ip)
		}
	}
}

// The hub owns the first host address unconditionally, so a machine
// pinned to it must be refused rather than silently shadowing the
// lighthouse and resolver.
func TestBuildMeshZoneRejectsHubAddress(t *testing.T) {
	master := []byte("mesh-zone-test-master-key-32byte")
	hubIP, err := nebderive.HubIP(nebDNSSubnet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = buildMeshZone(master, nebDNSSubnet, map[string]machines.Machine{
		"aa:bb:cc:dd:ee:01": {Name: "cp1", MeshIP: hubIP.String()},
	})
	if err == nil {
		t.Fatal("expected a collision error against the hub address")
	}
}

func TestBuildMeshZoneRejectsDuplicateLabels(t *testing.T) {
	master := []byte("mesh-zone-test-master-key-32byte")
	_, err := buildMeshZone(master, nebDNSSubnet, map[string]machines.Machine{
		"aa:bb:cc:dd:ee:01": {Name: "cp1"},
		"aa:bb:cc:dd:ee:02": {Name: "cp1"},
	})
	if err == nil {
		t.Fatal("expected a name collision error")
	}
}

func TestBuildMeshZoneRejectsAddressCollision(t *testing.T) {
	master := []byte("mesh-zone-test-master-key-32byte")
	// Two machines pinned to the same override address: the shape a
	// derived collision takes, made deterministic.
	_, err := buildMeshZone(master, nebDNSSubnet, map[string]machines.Machine{
		"aa:bb:cc:dd:ee:01": {Name: "cp1", MeshIP: "10.42.7.7"},
		"aa:bb:cc:dd:ee:02": {Name: "cp2", MeshIP: "10.42.7.7"},
	})
	if err == nil {
		t.Fatal("expected an address collision error")
	}
}

func TestBuildMeshZoneRejectsInvalidLabels(t *testing.T) {
	master := []byte("mesh-zone-test-master-key-32byte")
	for _, name := range []string{"Not_A_Label", "-leading", "a..b"} {
		if _, err := buildMeshZone(master, nebDNSSubnet, map[string]machines.Machine{
			"aa:bb:cc:dd:ee:01": {Name: name},
		}); err == nil {
			t.Errorf("accepted invalid label %q", name)
		}
	}
}

func TestMachineMeshIPOverride(t *testing.T) {
	master := []byte("mesh-zone-test-master-key-32byte")
	const mac = "aa:bb:cc:dd:ee:01"

	got, err := MachineMeshIP(master, mac, machines.Machine{MeshIP: "10.42.5.5"}, nebDNSSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if want := netip.MustParseAddr("10.42.5.5"); got != want {
		t.Errorf("override ignored: got %s, want %s", got, want)
	}

	got, err = MachineMeshIP(master, mac, machines.Machine{}, nebDNSSubnet)
	if err != nil {
		t.Fatal(err)
	}
	want, err := nebderive.MachineIP(master, mac, nebDNSSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("derived address = %s, want %s", got, want)
	}

	// An override outside the subnet would mint a cert nebula routes
	// nowhere, so it must fail at config time.
	if _, err := MachineMeshIP(master, mac, machines.Machine{MeshIP: "10.99.0.5"}, nebDNSSubnet); err == nil {
		t.Error("accepted a meshIP outside the mesh subnet")
	}
	if _, err := MachineMeshIP(master, mac, machines.Machine{MeshIP: "not-an-ip"}, nebDNSSubnet); err == nil {
		t.Error("accepted a malformed meshIP")
	}
}
