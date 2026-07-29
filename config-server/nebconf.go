package main

// The hub's nebula configuration, rendered from derived state. Nothing
// here is read from disk or remembered: given the master, the config is
// a pure function of (master, subnet, port) — invariant 2.
//
// The hub is the mesh's lighthouse and relay. It is the only public
// surface (invariant 5), so it is also the only member with a stable
// endpoint; every other member finds the mesh through it and nothing
// pins a home IP.

import (
	"fmt"
	"net/netip"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/nebderive"
)

// Cert groups. Nebula evaluates firewall rules against the groups in a
// peer's certificate, which is a stronger predicate than a source
// address: the group is signed by the CA, so it cannot be spoofed by a
// peer that merely reaches us.
//
// Three groups. Two follow the kinds of membership the domain model
// names: machines (Talos nodes, cert minted at compose time) and admins
// (owner devices, cert minted at enrollment). The third exists because
// "owner device" and "device the owner controls" are not the same thing:
// a TV in a shared space runs an appliance client anyone in the room can
// operate, so it must not inherit admin access to nodes. It gets to
// reach the media it is for, and nothing else.
//
// The group is signed into the cert, so it cannot be spoofed — and so
// regrouping a device means re-enrolling it. It is *not* part of the
// derivation: a regrouped device keeps its key and address.
const (
	nebGroupMachines = "machines"
	nebGroupAdmins   = "admins"
	nebGroupMedia    = "media"
)

// nebFlyListenHost is the hostname nebula binds on fly.io. Fly's UDP
// proxy only delivers to sockets bound to this address, and nebula
// resolves listen.host itself (main.go:145 in 1.11.0), so unlike
// wireguard-go — which needed the whole flyBind shim in wgbind.go —
// there is no custom bind code here. Verified in the 2026-07-29 spike.
const nebFlyListenHost = "fly-global-services"

// nebHubCertValidity bounds the hub's own leaf certificate. The hub
// re-mints it on every unseal, so this window only has to outlive an
// uptime streak; it is not a revocation control (see the revocation
// thread, uuid dc04e3e8). Generous on purpose: an expiry cliff on the
// lighthouse would take the whole mesh down.
const nebHubCertValidity = 365 * 24 * time.Hour

// nebClockSkew backdates notBefore so a hub whose clock runs slightly
// ahead of a peer's does not mint certs the peer rejects as future.
const nebClockSkew = time.Hour

// nebHubParams is everything needed to render the hub's config.
type nebHubParams struct {
	master     []byte
	subnet     netip.Prefix // mesh CIDR, e.g. 10.42.0.0/16
	listenHost string       // nebFlyListenHost on fly, "0.0.0.0" locally
	listenPort int          // public UDP port
	serveDNS   bool         // hub answers overlay DNS (nebdns, not nebula's)
	logLevel   string       // nebula log level ("" = info)
	blocklist  []string     // revoked cert fingerprints (mesh-blocklist.txt)
	now        func() time.Time
}

// YAML shapes. Written as structs rather than assembled by string
// formatting because the pki block carries multi-line PEM: yaml.Marshal
// gets the block scalars right, hand-indented Sprintf eventually does
// not.
// One shape serves both the hub (hubNebulaConfig) and the nodes
// (nodeNebulaConfig) so the two configs cannot drift in what a field
// means. The fields only one side uses are omitempty; the fields both
// sides set are always written, because "nebula's default" is not a
// thing this repo wants to depend on silently.
type nebConfigYAML struct {
	PKI           nebPKIYAML          `yaml:"pki"`
	StaticHostMap map[string][]string `yaml:"static_host_map"`
	Lighthouse    nebLighthouseYAML   `yaml:"lighthouse"`
	Listen        nebListenYAML       `yaml:"listen"`
	Tun           *nebTunYAML         `yaml:"tun,omitempty"` // nodes only: the hub has no TUN
	Punchy        nebPunchyYAML       `yaml:"punchy"`
	Relay         nebRelayYAML        `yaml:"relay"`
	Firewall      nebFirewallYAML     `yaml:"firewall"`
	Logging       nebLoggingYAML      `yaml:"logging"`
}

type nebPKIYAML struct {
	CA   string `yaml:"ca"`
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
	// Blocklist is the git-managed revocation list (nebblock.go),
	// present in every composed config so a revoked cert is refused
	// mesh-wide.
	Blocklist []string `yaml:"blocklist,omitempty"`
}

type nebLighthouseYAML struct {
	AmLighthouse bool     `yaml:"am_lighthouse"`
	ServeDNS     bool     `yaml:"serve_dns"`
	Interval     int      `yaml:"interval,omitempty"` // seconds between reports; lighthouses do not report
	Hosts        []string `yaml:"hosts,omitempty"`    // must be empty on a lighthouse
}

type nebListenYAML struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type nebPunchyYAML struct {
	Punch   bool `yaml:"punch"`
	Respond bool `yaml:"respond"`
}

type nebRelayYAML struct {
	AmRelay   bool     `yaml:"am_relay"`
	UseRelays bool     `yaml:"use_relays"`
	Relays    []string `yaml:"relays,omitempty"` // overlay addresses of relays to use
}

type nebTunYAML struct {
	Disabled bool   `yaml:"disabled"`
	Dev      string `yaml:"dev"`
	MTU      int    `yaml:"mtu"`
}

type nebFirewallYAML struct {
	OutboundAction string        `yaml:"outbound_action"`
	InboundAction  string        `yaml:"inbound_action"`
	Outbound       []nebRuleYAML `yaml:"outbound"`
	Inbound        []nebRuleYAML `yaml:"inbound"`
}

type nebRuleYAML struct {
	Port  string `yaml:"port"`
	Proto string `yaml:"proto"`
	Host  string `yaml:"host,omitempty"`
	Group string `yaml:"group,omitempty"`
}

type nebLoggingYAML struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// hubNebulaConfig renders the hub's nebula config from derived state.
//
// serve_dns is always false: nebula's built-in lighthouse DNS binds a
// kernel socket and needs a TUN, which the TUN-less hub does not have.
// The hub serves the zone itself over the netstack (nebstack.ListenUDP).
//
// static_host_map is empty by construction — the hub *is* the
// lighthouse, so it has nothing to look up. That is also why the hub
// holds no peer list: peers announce themselves, and membership is
// decided by the CA signature on their cert, not by a registry
// (invariant 1).
func hubNebulaConfig(p nebHubParams) ([]byte, error) {
	if p.listenPort <= 0 || p.listenPort > 65535 {
		return nil, fmt.Errorf("mesh listen port %d out of range", p.listenPort)
	}
	if p.listenHost == "" {
		return nil, fmt.Errorf("mesh listen host must be set")
	}
	now := time.Now
	if p.now != nil {
		now = p.now
	}

	hubIP, err := nebderive.HubIP(p.subnet)
	if err != nil {
		return nil, err
	}
	caPEM, err := nebderive.CACertPEM(p.master)
	if err != nil {
		return nil, fmt.Errorf("deriving mesh CA: %w", err)
	}
	priv, pub := nebderive.HubKey(p.master)
	crt, err := nebderive.HostCert(p.master, nebderive.HubName, pub, hubIP, p.subnet,
		nil, now().Add(-nebClockSkew), now().Add(nebHubCertValidity))
	if err != nil {
		return nil, fmt.Errorf("minting hub cert: %w", err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		return nil, fmt.Errorf("marshalling hub cert: %w", err)
	}

	level := p.logLevel
	if level == "" {
		level = "info"
	}

	cfg := nebConfigYAML{
		PKI: nebPKIYAML{
			CA:        string(caPEM),
			Cert:      string(crtPEM),
			Key:       string(nebderive.HostKeyPEM(priv)),
			Blocklist: p.blocklist,
		},
		StaticHostMap: map[string][]string{},
		Lighthouse: nebLighthouseYAML{
			AmLighthouse: true,
			ServeDNS:     false,
		},
		Listen: nebListenYAML{Host: p.listenHost, Port: p.listenPort},
		// Both off, and neither costs us the direct paths driver 1 is
		// about: the rendezvous that gets two NATed peers punched
		// together is a *lighthouse* function, not a punchy one —
		// handleHostQuery calls sendHostPunchNotification
		// unconditionally (lighthouse.go:1191 in 1.11.0), ungated by the
		// hub's punchy settings. punchy.* only governs this host punching
		// on its own behalf, and the hub has a stable public endpoint, so
		// it has nothing to punch through.
		Punchy: nebPunchyYAML{Punch: false, Respond: false},
		Relay: nebRelayYAML{
			AmRelay: true,
			// A relay does not itself relay through others; nebula
			// forces this off when am_relay is set, so it is written
			// only to say so out loud.
			UseRelays: false,
		},
		Firewall: nebFirewallYAML{
			// Outbound open: the hub dials peers (apid over the overlay,
			// auto-bootstrap). Inbound closed by default — every rule
			// below is deliberate.
			OutboundAction: "drop",
			InboundAction:  "drop",
			Outbound:       []nebRuleYAML{{Port: "any", Proto: "any", Host: "any"}},
			Inbound: []nebRuleYAML{
				// Reachability checks from any member.
				{Port: "any", Proto: "icmp", Host: "any"},
			},
		},
		Logging: nebLoggingYAML{Level: level, Format: "text"},
	}
	if p.serveDNS {
		// Every member resolves mesh names, machines included.
		cfg.Firewall.Inbound = append(cfg.Firewall.Inbound,
			nebRuleYAML{Port: "53", Proto: "udp", Host: "any"})
	}
	// The tunnel HTTP surface (/config) is admin-only. Under wg0 that is
	// enforced by source address (ADR-0003); here the group is signed
	// into the peer's certificate, so the firewall drops machines before
	// a request is even accepted. The source-address check stays as the
	// second layer.
	cfg.Firewall.Inbound = append(cfg.Firewall.Inbound,
		nebRuleYAML{Port: "80", Proto: "tcp", Group: nebGroupAdmins})

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling nebula config: %w", err)
	}
	return out, nil
}
