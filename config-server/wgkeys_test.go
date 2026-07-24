package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

func testWGSettings(t *testing.T) *wgSettings {
	t.Helper()
	master, err := wgderive.MasterFromHex("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	return &wgSettings{
		master:    master,
		serverPub: wgderive.PublicKey(wgderive.ServerKey(master)),
		serverIP:  netip.MustParseAddr("10.99.0.1"),
		subnet:    netip.MustParsePrefix("10.99.0.0/24"),
		endpoint:  "203.0.113.7:51820",
	}
}

// TestWGInjection verifies serve-time WireGuard injection changes the
// composed config ONLY by adding the wg0 interface and appending the
// tunnel address to certSANs — everything else must be identical to a
// compose without injection. Both composes
// include a regular patch (as every real machine does) so they take
// the same re-render path through the machinery encoder.
func TestWGInjection(t *testing.T) {
	root := t.TempDir()
	base := "version: v1alpha1\nmachine:\n  type: worker\n  certSANs:\n    - 10.0.0.20\ncluster:\n  clusterName: test\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	clusterPatch := "cluster:\n  clusterName: patched\n"
	if err := os.WriteFile(filepath.Join(root, "cluster.yaml"), []byte(clusterPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	m := machine{Config: "base.yaml", Patches: []string{"cluster.yaml"}, dir: root}
	mac := "b0:41:6f:15:3b:8f"
	wg := testWGSettings(t)

	patch, err := wg.machinePatch(mac, m)
	if err != nil {
		t.Fatal(err)
	}

	plain, err := buildConfig(root, m)
	if err != nil {
		t.Fatal(err)
	}
	injected, err := buildConfig(root, m, patch)
	if err != nil {
		t.Fatal(err)
	}

	var plainDoc, injectedDoc map[string]any
	if err := yaml.Unmarshal(plain, &plainDoc); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(injected, &injectedDoc); err != nil {
		t.Fatal(err)
	}

	machineDoc := injectedDoc["machine"].(map[string]any)

	// The tunnel address must be APPENDED to the existing certSANs
	// (strategic merge must not replace the list).
	sans, ok := machineDoc["certSANs"].([]any)
	if !ok || len(sans) != 2 || sans[0] != "10.0.0.20" || sans[1] != "10.99.0.16" {
		t.Fatalf("certSANs: got %#v, want [10.0.0.20 10.99.0.16]", machineDoc["certSANs"])
	}

	// Pull out and verify the injected interface.
	network, ok := machineDoc["network"].(map[string]any)
	if !ok {
		t.Fatal("injected config has no machine.network")
	}
	ifaces, ok := network["interfaces"].([]any)
	if !ok || len(ifaces) != 1 {
		t.Fatalf("expected exactly one interface, got %#v", network["interfaces"])
	}
	iface := ifaces[0].(map[string]any)
	if iface["interface"] != "wg0" {
		t.Errorf("interface name: got %v", iface["interface"])
	}
	if addrs := iface["addresses"].([]any); addrs[0] != "10.99.0.16/24" {
		t.Errorf("address: got %v", addrs[0])
	}
	wgSection := iface["wireguard"].(map[string]any)
	if wgSection["privateKey"] != wgderive.KeyBase64(wgderive.MachineKey(wg.master, mac)) {
		t.Error("privateKey does not match derived machine key")
	}
	peer := wgSection["peers"].([]any)[0].(map[string]any)
	if peer["publicKey"] != wgderive.KeyBase64(wg.serverPub) {
		t.Error("peer publicKey does not match server pubkey")
	}
	if peer["endpoint"] != wg.endpoint {
		t.Errorf("peer endpoint: got %v", peer["endpoint"])
	}
	if peer["persistentKeepaliveInterval"] != "25s" {
		t.Errorf("keepalive: got %v", peer["persistentKeepaliveInterval"])
	}
	if allowed := peer["allowedIPs"].([]any); allowed[0] != "10.99.0.0/24" {
		t.Errorf("allowedIPs: got %v", allowed[0])
	}

	// With the injected sections removed, the docs must be deep-equal.
	delete(network, "interfaces")
	if len(network) == 0 {
		delete(machineDoc, "network")
	}
	machineDoc["certSANs"] = sans[:1]
	if !reflect.DeepEqual(plainDoc, injectedDoc) {
		t.Error("injection changed the config beyond machine.network.interfaces and certSANs")
	}
}

// TestWGInjectionValidConfig ensures the injected config still parses
// as a valid Talos machine config (configpatcher round-trips it).
func TestWGInjectionValidConfig(t *testing.T) {
	root := t.TempDir()
	base := "version: v1alpha1\nmachine:\n  type: controlplane\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	m := machine{Config: "base.yaml", dir: root}
	wg := testWGSettings(t)

	patch, err := wg.machinePatch("aa:bb:cc:dd:ee:ff", m)
	if err != nil {
		t.Fatal(err)
	}
	out, err := buildConfig(root, m, patch)
	if err != nil {
		t.Fatalf("composing with wg patch: %v", err)
	}
	if !strings.Contains(string(out), "wg0") {
		t.Error("composed config missing wg0 interface")
	}
}

func TestDerivePeers(t *testing.T) {
	wg := testWGSettings(t)
	machines := map[string]machine{
		"b0:41:6f:15:3b:8f": {},
		"aa:bb:cc:dd:ee:01": {},
	}
	peers, err := wg.derivePeers(machines)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	seen := map[netip.Prefix]bool{}
	for _, p := range peers {
		if p.allowedIP.Bits() != 32 {
			t.Errorf("peer allowedIP not /32: %s", p.allowedIP)
		}
		if seen[p.allowedIP] {
			t.Errorf("duplicate allowedIP %s", p.allowedIP)
		}
		seen[p.allowedIP] = true
	}
}

func TestDerivePeersCollision(t *testing.T) {
	wg := testWGSettings(t)
	// Force a collision via explicit wgIP overrides.
	machines := map[string]machine{
		"aa:bb:cc:dd:ee:01": {WGIP: "10.99.0.50"},
		"aa:bb:cc:dd:ee:02": {WGIP: "10.99.0.50"},
	}
	if _, err := wg.derivePeers(machines); err == nil {
		t.Error("expected collision error")
	}

	// Colliding with the server address must also fail.
	machines = map[string]machine{"aa:bb:cc:dd:ee:01": {WGIP: "10.99.0.1"}}
	if _, err := wg.derivePeers(machines); err == nil {
		t.Error("expected server-address collision error")
	}
}

func TestWGIPOverride(t *testing.T) {
	wg := testWGSettings(t)
	ip, err := wg.machineTunnelIP("aa:bb:cc:dd:ee:01", machine{WGIP: "10.99.0.200"})
	if err != nil {
		t.Fatal(err)
	}
	if ip.String() != "10.99.0.200" {
		t.Errorf("override not honored: got %s", ip)
	}
	if _, err := wg.machineTunnelIP("aa:bb:cc:dd:ee:01", machine{WGIP: "not-an-ip"}); err == nil {
		t.Error("expected error for invalid wgIP")
	}
}
