package mesh

// The node side of the mesh: a machine's nebula identity and config,
// injected into its Talos machine config at serve time.
//
// Key material is derived from the master and handed to the machine
// that just proved (device flow) it is that machine. Nothing is
// stored: the cert is minted per serve, and re-minting never changes
// who the machine is (nebderive's leaf identities are byte-stable).
//
// Nebula runs as a system extension service, configured by a separate
// ExtensionServiceConfig document — so this patch adds a document to
// the config rather than merging into machine: (unlike wg0, which was
// a Talos network interface).

import (
	"fmt"
	"net/netip"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// Where the nebula extension looks for its config and key material. The
// service is started with `-config /usr/local/etc/nebula/config.yml`
// (siderolabs/extensions network/nebula/nebula.yaml), and the rest of
// the tree is ours to name. Every file here is mounted from the
// ExtensionServiceConfig document, never written by us.
const (
	nebNodeConfigPath = "/usr/local/etc/nebula/config.yml"
	nebNodeCAPath     = "/usr/local/etc/nebula/ca.crt"
	nebNodeCertPath   = "/usr/local/etc/nebula/node.crt"
	nebNodeKeyPath    = "/usr/local/etc/nebula/node.key"
)

// nebNodeService is the extension service name. It must match the
// extension's own name or Talos will not deliver the config to it.
const nebNodeService = "nebula"

// nebNodeListenPort is the UDP port nodes bind. Deterministic on
// purpose, even though a non-lighthouse host could take an OS-assigned
// port: when fly is down, the surviving access path is a LAN peer with a
// static_host_map entry pinning the node's LAN address (see the failure
// matrix in docs/mesh-v2-nebula.md), and that entry needs a port that is
// knowable without asking the lighthouse.
const nebNodeListenPort = 4242

// nebNodeTunDev is the interface name nebula creates on the node. Named
// explicitly so `talosctl get links` output is readable.
const nebNodeTunDev = "nebula0"

// nebNodeMTU is nebula's default overlay MTU (1300), stated rather than
// inherited so a change is a visible diff.
const nebNodeMTU = 1300

// nebMachineCertValidity bounds a machine's leaf certificate.
//
// Longer-lived than it looks: unlike the hub, which re-mints its cert on
// every unseal, a node's cert is minted once per *config serve* and then
// persists in the node's stored machine config. Nothing re-serves a
// config on a schedule, so this window is how long a node stays in the
// mesh without operator action — an expiry cliff, not a revocation
// control. Shortening it (the short-lived-cert revocation strategy,
// thread uuid dc04e3e8) requires a config-refresh mechanism first.
const nebMachineCertValidity = 5 * 365 * 24 * time.Hour

// MachinePatch renders the strategic-merge patch that gives a machine
// its mesh identity: an ExtensionServiceConfig document carrying the
// nebula config plus the derived CA, cert and key, preceded by a
// machine.certSANs merge adding the overlay address and mesh DNS name.
//
// The SANs exist because apid's node-address discovery does not pick
// up nebula0: without them a TLS dial to the machine over the mesh
// (talosconfig pointed at the overlay address, the hub's
// auto-bootstrap) is rejected.
//
// machines is the full machine set, not just this one, because the
// overlay address comes from buildMeshZone — the same function that
// builds mesh DNS. One source for both means a machine's cert can never
// claim an address its DNS name does not resolve to, and a derived-address
// collision is caught here (before a cert is minted) rather than after.
func (n *Manager) MachinePatch(master []byte, mac string, m machines.Machine, byMAC map[string]machines.Machine) (string, error) {
	if n.endpoint == "" {
		return "", fmt.Errorf("mesh endpoint is not configured (--mesh-endpoint)")
	}
	zone, err := buildMeshZone(master, n.subnet, byMAC)
	if err != nil {
		return "", fmt.Errorf("building mesh zone: %w", err)
	}
	name := MachineDNSName(mac, m)
	addr, ok := zone[name]
	if !ok {
		return "", fmt.Errorf("machine %s (%q) is not in the mesh zone", mac, name)
	}

	blocklist, err := LoadBlocklist(n.root)
	if err != nil {
		return "", fmt.Errorf("loading mesh blocklist: %w", err)
	}
	policy, err := n.effectivePolicy()
	if err != nil {
		return "", fmt.Errorf("loading mesh policy: %w", err)
	}
	cfg, err := nodeNebulaConfig(nebNodeParams{
		master:    master,
		name:      name,
		addr:      addr,
		subnet:    n.subnet,
		endpoint:  n.endpoint,
		blocklist: blocklist,
		inbound:   policy.Node.Inbound,
	})
	if err != nil {
		return "", err
	}

	caPEM, err := nebderive.CACertPEM(master)
	if err != nil {
		return "", fmt.Errorf("deriving mesh CA: %w", err)
	}
	priv, pub := nebderive.MachineKey(master, mac)
	now := time.Now()
	crt, err := nebderive.HostCert(master, name, pub, addr, n.subnet,
		[]string{GroupMachines}, now.Add(-ClockSkew), now.Add(nebMachineCertValidity))
	if err != nil {
		return "", fmt.Errorf("minting cert for %s: %w", mac, err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		return "", fmt.Errorf("marshalling cert for %s: %w", mac, err)
	}

	doc := nebExtSvcYAML{
		APIVersion: "v1alpha1",
		Kind:       "ExtensionServiceConfig",
		Name:       nebNodeService,
		ConfigFiles: []nebConfigFileYAML{
			{Content: string(cfg), MountPath: nebNodeConfigPath},
			{Content: string(caPEM), MountPath: nebNodeCAPath},
			{Content: string(crtPEM), MountPath: nebNodeCertPath},
			{Content: string(nebderive.HostKeyPEM(priv)), MountPath: nebNodeKeyPath},
		},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshalling extension config for %s: %w", mac, err)
	}
	// Two documents: the machine.certSANs merge, then the extension
	// config. The separator matters — configpatcher merges the first
	// into machine: and appends the second as its own document.
	sans := "machine:\n  certSANs:\n    - " + addr.String() + "\n"
	if n.dnsZone != "" {
		sans += "    - " + name + "." + n.dnsZone + "\n"
	}
	return sans + "---\n" + string(out), nil
}

// nebExtSvcYAML is Talos's ExtensionServiceConfig document.
type nebExtSvcYAML struct {
	APIVersion  string              `yaml:"apiVersion"`
	Kind        string              `yaml:"kind"`
	Name        string              `yaml:"name"`
	ConfigFiles []nebConfigFileYAML `yaml:"configFiles"`
}

type nebConfigFileYAML struct {
	Content   string `yaml:"content"`
	MountPath string `yaml:"mountPath"`
}

// nebNodeParams is everything needed to render one node's config.
type nebNodeParams struct {
	master    []byte
	name      string        // nebula/DNS name; also the cert name
	addr      netip.Addr    // overlay address
	subnet    netip.Prefix  // mesh CIDR
	endpoint  string        // hub's public host:port, for static_host_map
	blocklist []string      // revoked cert fingerprints (mesh-blocklist.txt)
	inbound   []nebRuleYAML // firewall admissions (mesh-policy.yaml, node scope)
}

// nodeNebulaConfig renders a node's nebula config.
//
// Everything about it follows from the hub being the only public surface
// (invariant 5): the hub is the single lighthouse, the single relay, and
// the only static_host_map entry. The node itself pins nothing about the
// home network, so a DHCP lease change or a new WAN address costs
// nothing (invariant 7).
func nodeNebulaConfig(p nebNodeParams) ([]byte, error) {
	if p.endpoint == "" {
		return nil, fmt.Errorf("mesh endpoint must be set")
	}
	if p.name == "" {
		return nil, fmt.Errorf("node name must be set")
	}
	if len(p.inbound) == 0 {
		return nil, fmt.Errorf("node inbound rules must be provided (%s, node scope)", PolicyFile)
	}
	hubIP, err := nebderive.HubIP(p.subnet)
	if err != nil {
		return nil, err
	}
	if p.addr == hubIP {
		return nil, fmt.Errorf("node %q would take the hub's address %s", p.name, hubIP)
	}

	cfg := ConfigYAML{
		// Paths, not inline PEM (the hub's own config inlines it): the
		// key material is mounted beside the config by the same
		// ExtensionServiceConfig document.
		PKI: nebPKIYAML{CA: nebNodeCAPath, Cert: nebNodeCertPath, Key: nebNodeKeyPath, Blocklist: p.blocklist},
		// The one thing a node knows without being told: where the hub
		// is. Everything else it learns from the hub.
		StaticHostMap: map[string][]string{hubIP.String(): {p.endpoint}},
		Lighthouse: nebLighthouseYAML{
			AmLighthouse: false,
			ServeDNS:     false,
			Interval:     60,
			Hosts:        []string{hubIP.String()},
			// A node holds several addresses that are not paths to it:
			// wg0, the pod network, the mesh itself. Filter both
			// directions so neither we nor our peers waste handshakes.
			LocalAllowList:  nebUnderlayFilter(p.subnet),
			RemoteAllowList: nebUnderlayFilter(p.subnet),
		},
		Listen: nebListenYAML{Host: "0.0.0.0", Port: nebNodeListenPort},
		Tun: &nebTunYAML{
			Disabled: false,
			Dev:      nebNodeTunDev,
			MTU:      nebNodeMTU,
		},
		// Nodes sit behind the home NAT, so unlike the hub they do have
		// something to punch through: punch keeps the mapping alive and
		// respond answers a peer that only we can reach.
		Punchy: nebPunchyYAML{Punch: true, Respond: true},
		Relay: nebRelayYAML{
			AmRelay:   false,
			UseRelays: true,
			Relays:    []string{hubIP.String()},
		},
		Firewall: nebFirewallYAML{
			// Outbound open (the node dials out: media, updates over the
			// overlay if ever needed). Inbound is the policy's who×what
			// table — the rationale for each admission lives as comments
			// in talos/mesh-policy.yaml, next to the rules themselves.
			OutboundAction: "drop",
			InboundAction:  "drop",
			Outbound:       []nebRuleYAML{{Port: "any", Proto: "any", Host: "any"}},
			Inbound:        append([]nebRuleYAML(nil), p.inbound...),
		},
		Logging: nebLoggingYAML{Level: "info", Format: "text"},
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling node nebula config: %w", err)
	}
	return out, nil
}
