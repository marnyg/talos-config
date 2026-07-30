package main

import (
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/validation"
	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/cert"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

var nebNodeSubnet = netip.MustParsePrefix("10.42.0.0/16")

const nebTestEndpoint = "203.0.113.7:4242"

func nebTestManager(root string) *nebManager {
	return newNebManager(4242, nebNodeSubnet, "0.0.0.0", nebTestEndpoint, meshDNSZone, root, adminDevices("laptop"))
}

// nebTestMachines is a machine set with one named control plane, mirroring
// talos/machines/.
func nebTestMachines() map[string]machines.Machine {
	return map[string]machines.Machine{
		"b0:41:6f:15:3b:8f": {Name: "cp1"},
	}
}

// nodeParams renders a node config for the derived identity of mac.
func nodeParams(t *testing.T, mac string, m machines.Machine) nebNodeParams {
	t.Helper()
	addr, err := machineMeshIP(nebTestMaster, mac, m, nebNodeSubnet)
	if err != nil {
		t.Fatal(err)
	}
	return nebNodeParams{
		master:   nebTestMaster,
		name:     machineDNSName(mac, m),
		addr:     addr,
		subnet:   nebNodeSubnet,
		endpoint: nebTestEndpoint,
	}
}

// TestNodeNebulaConfigValidates is the load-bearing test, the node-side
// twin of TestHubNebulaConfigValidates: nebula's own validation accepts
// the config we render.
//
// The rendered pki paths are the extension's mount paths, which exist
// only on a node, so the test rewrites them to real files under
// t.TempDir before handing the config to nebula. Rewriting here rather
// than parameterising nodeNebulaConfig keeps the production renderer
// free of a knob only tests use.
func TestNodeNebulaConfigValidates(t *testing.T) {
	mac := "b0:41:6f:15:3b:8f"
	m := machines.Machine{Name: "cp1"}
	p := nodeParams(t, mac, m)

	dir := t.TempDir()
	caPEM, err := nebderive.CACertPEM(p.master)
	if err != nil {
		t.Fatal(err)
	}
	priv, pub := nebderive.MachineKey(p.master, mac)
	crt, err := nebderive.HostCert(p.master, p.name, pub, p.addr, p.subnet,
		[]string{nebGroupMachines}, time.Now().Add(-nebClockSkew), time.Now().Add(nebMachineCertValidity))
	if err != nil {
		t.Fatal(err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, body []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	raw, err := nodeNebulaConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var parsed nebConfigYAML
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.PKI.CA != nebNodeCAPath || parsed.PKI.Cert != nebNodeCertPath || parsed.PKI.Key != nebNodeKeyPath {
		t.Fatalf("pki = %+v, want the extension's mount paths", parsed.PKI)
	}
	parsed.PKI = nebPKIYAML{
		CA:   write("ca.crt", caPEM),
		Cert: write("node.crt", crtPEM),
		Key:  write("node.key", nebderive.HostKeyPEM(priv)),
	}
	if raw, err = yaml.Marshal(parsed); err != nil {
		t.Fatal(err)
	}

	var c config.C
	if err := c.LoadString(string(raw)); err != nil {
		t.Fatalf("nebula cannot parse the rendered node config: %v\n%s", err, raw)
	}
	// configTest=true runs every config-driven constructor (pki,
	// firewall, lighthouse, relay, listen) without creating a TUN, so a
	// typo'd key or a malformed rule fails here rather than on a node.
	if _, err := nebula.Main(&c, true, "nebmachine-test", logging.NewLogger(io.Discard), overlay.NewUserDeviceFromConfig); err != nil {
		t.Fatalf("nebula rejected the rendered node config: %v\n%s", err, raw)
	}
}

// TestNodeNebulaConfigTopology pins the node's view of the mesh: the hub
// is its only lighthouse, its only relay and its only static host — a
// node pins nothing about the home network (invariants 5 and 7).
func TestNodeNebulaConfigTopology(t *testing.T) {
	raw, err := nodeNebulaConfig(nodeParams(t, "b0:41:6f:15:3b:8f", machines.Machine{Name: "cp1"}))
	if err != nil {
		t.Fatal(err)
	}
	var got nebConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	hubIP, err := nebderive.HubIP(nebNodeSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lighthouse.AmLighthouse || got.Lighthouse.ServeDNS {
		t.Error("a node must be neither lighthouse nor DNS server")
	}
	if want := []string{hubIP.String()}; !equalStrings(got.Lighthouse.Hosts, want) {
		t.Errorf("lighthouse.hosts = %v, want %v", got.Lighthouse.Hosts, want)
	}
	if want := []string{hubIP.String()}; !equalStrings(got.Relay.Relays, want) {
		t.Errorf("relay.relays = %v, want %v", got.Relay.Relays, want)
	}
	if got.Relay.AmRelay || !got.Relay.UseRelays {
		t.Error("a node uses relays but is not one")
	}
	if eps := got.StaticHostMap[hubIP.String()]; !equalStrings(eps, []string{nebTestEndpoint}) {
		t.Errorf("static_host_map[%s] = %v, want [%s]", hubIP, eps, nebTestEndpoint)
	}
	if len(got.StaticHostMap) != 1 {
		t.Errorf("static_host_map has %d entries, want only the hub", len(got.StaticHostMap))
	}
	if !got.Punchy.Punch || !got.Punchy.Respond {
		t.Error("nodes are behind NAT: punchy must be on")
	}
	if got.Tun == nil || got.Tun.Disabled || got.Tun.Dev != nebNodeTunDev {
		t.Errorf("tun = %+v, want enabled on %s", got.Tun, nebNodeTunDev)
	}
	if got.Listen.Port != nebNodeListenPort {
		t.Errorf("listen.port = %d, want the deterministic %d (LAN fallback pins it)", got.Listen.Port, nebNodeListenPort)
	}
}

// TestNodeFirewallGrantsHubAndAdminsOnly pins who may reach a node over
// the overlay, as an allow-list: an unrecognised rule fails the test, so
// widening node access is never a silent diff. Machines are mesh members
// too, and a machine reaching another machine's apid is exactly what the
// group split is for.
func TestNodeFirewallGrantsHubAndAdminsOnly(t *testing.T) {
	raw, err := nodeNebulaConfig(nodeParams(t, "b0:41:6f:15:3b:8f", machines.Machine{Name: "cp1"}))
	if err != nil {
		t.Fatal(err)
	}
	var got nebConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Firewall.InboundAction != "drop" {
		t.Errorf("inbound_action = %q, want drop", got.Firewall.InboundAction)
	}
	for _, r := range got.Firewall.Inbound {
		switch {
		case r.Proto == "icmp":
			continue // reachability from any member
		case r.Host == nebderive.HubName, r.Group == nebGroupAdmins:
			continue
		case r.Group == nebGroupMedia:
			// The media group's whole point is that it is narrow: one
			// port, one protocol. "any" here would hand a shared-space
			// appliance the node.
			if r.Port != strconv.Itoa(nebMediaPort) || r.Proto != "tcp" {
				t.Errorf("media rule %+v is wider than %d/tcp", r, nebMediaPort)
			}
		default:
			t.Errorf("unexpected inbound rule %+v: only icmp, the hub, %q and %q may reach a node", r, nebGroupAdmins, nebGroupMedia)
		}
	}
	if !hasRule(got.Firewall.Inbound, nebRuleYAML{Port: strconv.Itoa(nebMediaPort), Proto: "tcp", Group: nebGroupMedia}) {
		t.Errorf("the media group must reach jellyfin on %d", nebMediaPort)
	}
	if !hasRule(got.Firewall.Inbound, nebRuleYAML{Port: "any", Proto: "any", Host: nebderive.HubName}) {
		t.Error("the hub must be able to dial apid (auto-bootstrap, status probes)")
	}
	if !hasRule(got.Firewall.Inbound, nebRuleYAML{Port: "any", Proto: "any", Group: nebGroupAdmins}) {
		t.Errorf("admin devices must keep the access wg0 already grants them")
	}
	if hasRule(got.Firewall.Inbound, nebRuleYAML{Port: "any", Proto: "any", Group: nebGroupMachines}) {
		t.Error("machines must not reach each other's control surfaces over the mesh")
	}
}

// splitNebPatch decodes the two documents of the machine mesh patch:
// the machine.certSANs merge and the ExtensionServiceConfig.
func splitNebPatch(t *testing.T, raw string) (map[string]any, nebExtSvcYAML) {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(raw))
	var machineDoc map[string]any
	if err := dec.Decode(&machineDoc); err != nil {
		t.Fatalf("decoding machine document: %v\n%s", err, raw)
	}
	var ext nebExtSvcYAML
	if err := dec.Decode(&ext); err != nil {
		t.Fatalf("decoding extension document: %v\n%s", err, raw)
	}
	return machineDoc, ext
}

// certSANsOf digs machine.certSANs out of a decoded YAML document.
func certSANsOf(t *testing.T, doc map[string]any) []string {
	t.Helper()
	m, ok := doc["machine"].(map[string]any)
	if !ok {
		t.Fatalf("no machine: section in %v", doc)
	}
	raw, ok := m["certSANs"].([]any)
	if !ok {
		t.Fatalf("no certSANs in machine: %v", m)
	}
	sans := make([]string, 0, len(raw))
	for _, s := range raw {
		sans = append(sans, s.(string))
	}
	return sans
}

// TestNebMachinePatchIdentity is the end-to-end check on the injected
// document: it is an ExtensionServiceConfig for the nebula service, and
// the cert it carries is the derived machine identity — right name,
// right address, machines group, signed by the derived CA. The
// accompanying certSANs merge must carry the same identity (address and
// mesh DNS name) so TLS dials over the overlay verify.
func TestNebMachinePatchIdentity(t *testing.T) {
	mac := "b0:41:6f:15:3b:8f"
	byMAC := nebTestMachines()
	n := nebTestManager(t.TempDir())

	raw, err := n.nebMachinePatch(nebTestMaster, mac, byMAC[mac], byMAC)
	if err != nil {
		t.Fatal(err)
	}
	machineDoc, doc := splitNebPatch(t, raw)
	if doc.Kind != "ExtensionServiceConfig" || doc.Name != nebNodeService {
		t.Errorf("doc = %s/%s, want ExtensionServiceConfig/%s", doc.Kind, doc.Name, nebNodeService)
	}
	files := map[string]string{}
	for _, f := range doc.ConfigFiles {
		files[f.MountPath] = f.Content
	}
	for _, want := range []string{nebNodeConfigPath, nebNodeCAPath, nebNodeCertPath, nebNodeKeyPath} {
		if files[want] == "" {
			t.Errorf("no content mounted at %s", want)
		}
	}

	caPEM, err := nebderive.CACertPEM(nebTestMaster)
	if err != nil {
		t.Fatal(err)
	}
	if files[nebNodeCAPath] != string(caPEM) {
		t.Error("mounted ca.crt is not the derived CA")
	}
	priv, _ := nebderive.MachineKey(nebTestMaster, mac)
	if files[nebNodeKeyPath] != string(nebderive.HostKeyPEM(priv)) {
		t.Error("mounted node.key is not the derived machine key")
	}

	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(files[nebNodeCertPath]))
	if err != nil {
		t.Fatal(err)
	}
	if crt.Name() != "cp1" {
		t.Errorf("cert name = %q, want the machine's mesh label cp1", crt.Name())
	}
	wantIP, err := nebderive.MachineIP(nebTestMaster, mac, nebNodeSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if nets := crt.Networks(); len(nets) != 1 || nets[0].Addr() != wantIP || nets[0].Bits() != nebNodeSubnet.Bits() {
		t.Errorf("cert networks = %v, want %s/%d", crt.Networks(), wantIP, nebNodeSubnet.Bits())
	}
	if got, want := certSANsOf(t, machineDoc), []string{wantIP.String(), "cp1." + meshDNSZone}; !equalStrings(got, want) {
		t.Errorf("machine.certSANs = %v, want %v", got, want)
	}
	if groups := crt.Groups(); len(groups) != 1 || groups[0] != nebGroupMachines {
		t.Errorf("cert groups = %v, want [%s]", groups, nebGroupMachines)
	}
	ca, err := nebderive.CACert(nebTestMaster)
	if err != nil {
		t.Fatal(err)
	}
	pool := cert.NewCAPool()
	if err := pool.AddCA(ca); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.VerifyCertificate(time.Now(), crt); err != nil {
		t.Errorf("minted cert does not verify against the derived CA: %v", err)
	}
}

// TestNebMachinePatchAgreesWithMeshDNS is the reason nebMachinePatch takes
// the whole machine set: the address in the cert must be the address the
// mesh DNS zone hands out, or `cp1.mesh.internal` resolves to an address
// nothing answers on.
func TestNebMachinePatchAgreesWithMeshDNS(t *testing.T) {
	mac := "b0:41:6f:15:3b:8f"
	byMAC := nebTestMachines()
	n := nebTestManager(t.TempDir())

	raw, err := n.nebMachinePatch(nebTestMaster, mac, byMAC[mac], byMAC)
	if err != nil {
		t.Fatal(err)
	}
	machineDoc, doc := splitNebPatch(t, raw)
	var certPEM string
	for _, f := range doc.ConfigFiles {
		if f.MountPath == nebNodeCertPath {
			certPEM = f.Content
		}
	}
	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	zone, err := buildMeshZone(nebTestMaster, nebNodeSubnet, byMAC, n.devices)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := crt.Networks()[0].Addr(), zone["cp1"]; got != want {
		t.Errorf("cert address %s but mesh DNS answers %s", got, want)
	}
	if got, want := certSANsOf(t, machineDoc)[0], zone["cp1"].String(); got != want {
		t.Errorf("certSAN address %s but mesh DNS answers %s", got, want)
	}
}

// TestNebMachinePatchRejectsCollision: a derived-address collision must be
// caught before a cert claims a duplicate address, since certs bake it.
func TestNebMachinePatchRejectsCollision(t *testing.T) {
	mac := "b0:41:6f:15:3b:8f"
	byMAC := nebTestMachines()
	other, err := nebderive.MachineIP(nebTestMaster, mac, nebNodeSubnet)
	if err != nil {
		t.Fatal(err)
	}
	byMAC["aa:bb:cc:dd:ee:ff"] = machines.Machine{Name: "cp2", MeshIP: other.String()}

	n := nebTestManager(t.TempDir())
	if _, err := n.nebMachinePatch(nebTestMaster, mac, byMAC[mac], byMAC); err == nil {
		t.Fatal("expected a collision error, got none")
	}
}

// TestNebMachinePatchNeedsEndpoint: without the hub's public endpoint a
// node has no way to find the lighthouse, so a config that omits it is
// worse than no config.
func TestNebMachinePatchNeedsEndpoint(t *testing.T) {
	mac := "b0:41:6f:15:3b:8f"
	byMAC := nebTestMachines()
	n := nebTestManager(t.TempDir())
	n.endpoint = ""
	if _, err := n.nebMachinePatch(nebTestMaster, mac, byMAC[mac], byMAC); err == nil {
		t.Fatal("expected an error without --mesh-endpoint, got none")
	}
}

// TestNebMachinePatchComposes runs the patch through the real compose
// pipeline and then through Talos's own loader: an ExtensionServiceConfig
// is a *document*, not a machine: merge, so the thing to prove is that
// configpatcher appends it and that machinery parses and validates what
// comes out. A hand-indented patch that yaml.Unmarshal tolerates but
// Talos rejects would strand the node.
func TestNebMachinePatchComposes(t *testing.T) {
	root := t.TempDir()
	base := "version: v1alpha1\nmachine:\n  type: worker\n  token: aa.bbbbbbbbbbbbbbbb\n  certSANs:\n    - 10.0.0.20\ncluster:\n  clusterName: test\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	mac := "b0:41:6f:15:3b:8f"
	byMAC := map[string]machines.Machine{mac: {Name: "cp1", Config: "base.yaml", Dir: root}}
	n := nebTestManager(root)

	patch, err := n.nebMachinePatch(nebTestMaster, mac, byMAC[mac], byMAC)
	if err != nil {
		t.Fatal(err)
	}
	composed, err := machines.BuildConfig(root, byMAC[mac], patch)
	if err != nil {
		t.Fatal(err)
	}

	provider, err := configloader.NewFromBytes(composed)
	if err != nil {
		t.Fatalf("talos cannot load the composed config: %v\n%s", err, composed)
	}
	svc := provider.ExtensionServiceConfigs()
	if len(svc) != 1 || svc[0].Name() != nebNodeService {
		t.Fatalf("extension service configs = %#v, want one named %s", svc, nebNodeService)
	}
	// Validate the document itself rather than the whole config: the
	// stub base config here is deliberately minimal (no install disk, no
	// CA), and those are the real repo's business, not this patch's.
	type validator interface {
		Validate(validation.RuntimeMode, ...validation.Option) ([]string, error)
	}
	v, ok := svc[0].(validator)
	if !ok {
		t.Fatalf("extension service document %T does not validate", svc[0])
	}
	if _, err := v.Validate(nebTestRuntimeMode{}); err != nil {
		t.Fatalf("talos rejected the extension service document: %v\n%s", err, composed)
	}
	mounted := map[string]bool{}
	for _, f := range svc[0].ConfigFiles() {
		mounted[f.MountPath()] = f.Content() != ""
	}
	for _, want := range []string{nebNodeConfigPath, nebNodeCAPath, nebNodeCertPath, nebNodeKeyPath} {
		if !mounted[want] {
			t.Errorf("composed config does not mount %s", want)
		}
	}
	// The mesh must not disturb wg0's injection surface: phase 1 runs
	// both, and wg0 is the one carrying production traffic.
	if ifaces := provider.Machine().Network().Devices(); len(ifaces) != 0 {
		t.Errorf("mesh patch touched machine.network.interfaces: %#v", ifaces)
	}
	// The certSANs merge must APPEND to the repo's own SANs — replacing
	// them would break whatever those SANs are for — and both mesh
	// identities (overlay address, mesh DNS name) must land.
	wantIP, err := nebderive.MachineIP(nebTestMaster, mac, nebNodeSubnet)
	if err != nil {
		t.Fatal(err)
	}
	var composedDoc map[string]any
	if err := yaml.NewDecoder(strings.NewReader(string(composed))).Decode(&composedDoc); err != nil {
		t.Fatal(err)
	}
	if got, want := certSANsOf(t, composedDoc), []string{"10.0.0.20", wantIP.String(), "cp1." + meshDNSZone}; !equalStrings(got, want) {
		t.Errorf("composed certSANs = %v, want %v", got, want)
	}
}

// nebTestRuntimeMode is the validation mode of a machine about to
// install — what a PXE node fetching /config actually is.
type nebTestRuntimeMode struct{}

func (nebTestRuntimeMode) String() string        { return "metal" }
func (nebTestRuntimeMode) RequiresInstall() bool { return true }
func (nebTestRuntimeMode) InContainer() bool     { return false }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasRule(rules []nebRuleYAML, want nebRuleYAML) bool {
	for _, r := range rules {
		if r == want {
			return true
		}
	}
	return false
}

// TestNodeUnderlayFilterExcludesOverlaysNotLAN guards the fix for the
// junk-candidate bug: a node used to advertise its wg0 address, its
// flannel/cni pod-network addresses and (in principle) the mesh itself
// as underlay candidates. Peers then wasted handshakes on them, and a
// peer that also had wg0 up would carry nebula over wireguard and
// hairpin through the hub — which silently invalidated two phase-1 punch
// measurements before anyone noticed.
//
// The LAN assertion is the load-bearing half: same-LAN direct paths are
// the entire remaining value of the mesh (ADR-0006), so a filter that
// excluded the LAN would be a worse bug than the one being fixed.
func TestNodeUnderlayFilterExcludesOverlaysNotLAN(t *testing.T) {
	p := nodeParams(t, "b0:41:6f:15:3b:8f", machines.Machine{Name: "cp1"})
	raw, err := nodeNebulaConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var got nebConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		list map[string]bool
	}{
		{"local_allow_list", got.Lighthouse.LocalAllowList},
		{"remote_allow_list", got.Lighthouse.RemoteAllowList},
	} {
		if tc.list == nil {
			t.Fatalf("%s: not set — junk underlay candidates are unfiltered", tc.name)
		}
		for _, denied := range []string{nebPodSubnet, nebNodeSubnet.String()} {
			allowed, ok := tc.list[denied]
			if !ok {
				t.Errorf("%s: %s missing — overlay/pod addresses would be used as underlay", tc.name, denied)
				continue
			}
			if allowed {
				t.Errorf("%s: %s allowed, want denied", tc.name, denied)
			}
		}
		if allowed, ok := tc.list["0.0.0.0/0"]; !ok || !allowed {
			t.Errorf("%s: no default-allow rule; the LAN and WAN would both be filtered out", tc.name)
		}
		// The LAN must not be denied by any rule, however written.
		for cidr, allowed := range tc.list {
			if allowed {
				continue
			}
			pfx, err := netip.ParsePrefix(cidr)
			if err != nil {
				t.Fatalf("%s: unparseable rule %q: %v", tc.name, cidr, err)
			}
			if pfx.Contains(netip.MustParseAddr("10.0.0.30")) {
				t.Errorf("%s: rule %s denies the LAN — this breaks LAN-direct paths (ADR-0006)", tc.name, cidr)
			}
		}
	}
}
