package mesh

// The device side of the mesh: the self-contained nebula config that
// enrollment (nebenroll.go in main) and the TV flow hand a device. The
// HTTP handlers and the wallet-signature checks stay with the server;
// what lives here is the derivation — given the master and a declared
// device, the config is a pure function.

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// DeviceCertValidity bounds a device's leaf certificate. Shorter than
// a machine's five years because re-enrolling a device is one command
// against a hub that needs no memory of the last one — this is the one
// place where short-lived certs are a practical revocation strategy
// rather than a maintenance cliff (thread uuid dc04e3e8).
const DeviceCertValidity = 90 * 24 * time.Hour

// DeviceConfig renders a device's self-contained nebula config: the
// derived identity, inline, plus the hub as its only lighthouse and
// relay.
//
// The address comes from buildMeshZone for the same reason a machine's
// does — mesh DNS and the cert must agree, and this is where a collision
// with a machine or another device is caught before a cert bakes it in.
func (m *Manager) DeviceConfig(master []byte, d Device) ([]byte, error) {
	if m.endpoint == "" {
		return nil, fmt.Errorf("mesh endpoint is not configured (--mesh-endpoint)")
	}
	byMAC, err := machines.Load(m.machinesDir())
	if err != nil {
		return nil, fmt.Errorf("loading machines: %w", err)
	}
	zone, err := buildMeshZone(master, m.subnet, byMAC, m.devices)
	if err != nil {
		return nil, fmt.Errorf("building mesh zone: %w", err)
	}
	addr, ok := zone[d.Name]
	if !ok {
		return nil, fmt.Errorf("device %q is not in the mesh zone", d.Name)
	}

	hubIP, err := nebderive.HubIP(m.subnet)
	if err != nil {
		return nil, err
	}
	caPEM, err := nebderive.CACertPEM(master)
	if err != nil {
		return nil, fmt.Errorf("deriving mesh CA: %w", err)
	}
	priv, pub := nebderive.DeviceKey(master, d.Name)
	now := time.Now()
	crt, err := nebderive.HostCert(master, d.Name, pub, addr, m.subnet,
		[]string{d.Group}, now.Add(-ClockSkew), now.Add(DeviceCertValidity))
	if err != nil {
		return nil, fmt.Errorf("minting cert for %q: %w", d.Name, err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		return nil, fmt.Errorf("marshalling cert for %q: %w", d.Name, err)
	}
	blocklist, err := LoadBlocklist(m.root)
	if err != nil {
		return nil, fmt.Errorf("loading mesh blocklist: %w", err)
	}

	cfg := ConfigYAML{
		PKI: nebPKIYAML{
			CA:        string(caPEM),
			Cert:      string(crtPEM),
			Key:       string(nebderive.HostKeyPEM(priv)),
			Blocklist: blocklist,
		},
		StaticHostMap: map[string][]string{hubIP.String(): {m.endpoint}},
		Lighthouse: nebLighthouseYAML{
			Interval: 60,
			Hosts:    []string{hubIP.String()},
			// Devices roam, so their own addresses are not worth
			// filtering — but a peer's wg0 or pod-network address must
			// never be dialed: that is how nebula ends up tunnelled
			// inside wireguard and hairpinning through the hub.
			RemoteAllowList: nebUnderlayFilter(m.subnet),
		},
		// Port 0: devices roam. A fixed port buys a node the
		// lighthouse-less LAN fallback (nebmachine.go), but a laptop that
		// changes networks daily gains nothing from it and can lose to
		// whatever else holds 4242 on a coffee-shop NAT.
		Listen: nebListenYAML{Host: "0.0.0.0", Port: 0},
		// No dev name: device configs are portable by design (one file,
		// moved by scp/clipboard/QR) and Darwin rejects any name that is
		// not utun[0-9]+. Let each client pick its own.
		Tun:    &nebTunYAML{MTU: nebNodeMTU},
		Punchy: nebPunchyYAML{Punch: true, Respond: true},
		Relay:  nebRelayYAML{UseRelays: true, Relays: []string{hubIP.String()}},
		Firewall: nebFirewallYAML{
			// Outbound open: a device is the thing that initiates. Inbound
			// needs only ICMP, because nebula's firewall is stateful —
			// replies to flows this device started are already allowed.
			OutboundAction: "drop",
			InboundAction:  "drop",
			Outbound:       []nebRuleYAML{{Port: "any", Proto: "any", Host: "any"}},
			Inbound:        []nebRuleYAML{{Port: "any", Proto: "icmp", Host: "any"}},
		},
		Logging: nebLoggingYAML{Level: "info", Format: "text"},
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling device config: %w", err)
	}
	return out, nil
}
