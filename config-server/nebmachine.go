package main

// The node side of the mesh: a machine's nebula identity and config,
// injected into its Talos machine config at serve time.
//
// Same trust chain as the wg0 key injection in wgkeys.go — key material
// is derived from the master and handed to the machine that just proved
// (device flow) it is that machine. Nothing is stored: the cert is
// minted per serve, and re-minting never changes who the machine is
// (nebderive's leaf identities are byte-stable).
//
// The transport differs from wg0's, though. wg0 is a Talos *network
// interface*, so it goes in machine.network.interfaces. Nebula runs as
// a system extension service, which is configured by a separate
// ExtensionServiceConfig document — so this patch adds a document to
// the config rather than merging into machine:.

import (
	"fmt"
	"net/netip"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

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
// explicitly so `talosctl get links` output is readable and so it can
// never collide with wg0 while both overlays run.
const nebNodeTunDev = "nebula0"

// nebNodeMTU is nebula's default overlay MTU (1300), stated rather than
// inherited so a change is a visible diff.
const nebNodeMTU = 1300

// nebMediaPort is the only thing the media group may reach on a node:
// Jellyfin's NodePort. Kept a single port rather than "the NodePort
// range" because the point of the group is that a shared-space appliance
// reaches the one service it exists for — sonarr and radarr are on
// neighbouring NodePorts and are admin tools.
const nebMediaPort = 30096

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

// nebMachinePatch renders the strategic-merge patch that gives a machine
// its mesh identity: an ExtensionServiceConfig document carrying the
// nebula config plus the derived CA, cert and key.
//
// machines is the full machine set, not just this one, because the
// overlay address comes from buildMeshZone — the same function that
// builds mesh DNS. One source for both means a machine's cert can never
// claim an address its DNS name does not resolve to, and a derived-address
// collision is caught here (before a cert is minted) rather than after.
func (n *nebManager) nebMachinePatch(master []byte, mac string, m machine, machines map[string]machine) (string, error) {
	if n.endpoint == "" {
		return "", fmt.Errorf("mesh endpoint is not configured (--mesh-endpoint)")
	}
	zone, err := buildMeshZone(master, n.subnet, machines, n.devices)
	if err != nil {
		return "", fmt.Errorf("building mesh zone: %w", err)
	}
	name := machineDNSName(mac, m)
	addr, ok := zone[name]
	if !ok {
		return "", fmt.Errorf("machine %s (%q) is not in the mesh zone", mac, name)
	}

	cfg, err := nodeNebulaConfig(nebNodeParams{
		master:   master,
		name:     name,
		addr:     addr,
		subnet:   n.subnet,
		endpoint: n.endpoint,
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
		[]string{nebGroupMachines}, now.Add(-nebClockSkew), now.Add(nebMachineCertValidity))
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
	// The leading separator makes this a *document* patch: configpatcher
	// merges it as a new document instead of into machine:.
	return "---\n" + string(out), nil
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
	master   []byte
	name     string       // nebula/DNS name; also the cert name
	addr     netip.Addr   // overlay address
	subnet   netip.Prefix // mesh CIDR
	endpoint string       // hub's public host:port, for static_host_map
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
	hubIP, err := nebderive.HubIP(p.subnet)
	if err != nil {
		return nil, err
	}
	if p.addr == hubIP {
		return nil, fmt.Errorf("node %q would take the hub's address %s", p.name, hubIP)
	}

	cfg := nebConfigYAML{
		// Paths, not inline PEM (the hub's own config inlines it): the
		// key material is mounted beside the config by the same
		// ExtensionServiceConfig document.
		PKI: nebPKIYAML{CA: nebNodeCAPath, Cert: nebNodeCertPath, Key: nebNodeKeyPath},
		// The one thing a node knows without being told: where the hub
		// is. Everything else it learns from the hub.
		StaticHostMap: map[string][]string{hubIP.String(): {p.endpoint}},
		Lighthouse: nebLighthouseYAML{
			AmLighthouse: false,
			ServeDNS:     false,
			Interval:     60,
			Hosts:        []string{hubIP.String()},
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
			// overlay if ever needed). Inbound closed — the rules below
			// are the whole story.
			OutboundAction: "drop",
			InboundAction:  "drop",
			Outbound:       []nebRuleYAML{{Port: "any", Proto: "any", Host: "any"}},
			Inbound: []nebRuleYAML{
				// Reachability checks from any member.
				{Port: "any", Proto: "icmp", Host: "any"},
				// The hub dials the node: apid for auto-bootstrap and
				// /status probes. Matched by cert *name*, which only the
				// CA can issue — the hub holds no group.
				{Port: "any", Proto: "any", Host: nebderive.HubName},
				// Owner devices, unrestricted: this is the access wg0
				// already grants them (allowed-ips, no firewall), so
				// narrowing it here would be a regression dressed as
				// hardening. What the rule does buy is that *machines*
				// and the media group are not in it.
				{Port: "any", Proto: "any", Group: nebGroupAdmins},
				// Shared-space appliances: the media they are for, and
				// nothing else. No apid, no kube API, no other NodePort.
				{Port: strconv.Itoa(nebMediaPort), Proto: "tcp", Group: nebGroupMedia},
			},
		},
		Logging: nebLoggingYAML{Level: "info", Format: "text"},
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling node nebula config: %w", err)
	}
	return out, nil
}
