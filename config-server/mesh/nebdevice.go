package mesh

// The device side of the mesh: the (nearly-)self-contained nebula
// config the hub returns to a wallet-signed enrollment. The HTTP
// handlers and the wallet-signature checks stay with the server; what
// lives here is the mint given (master, name, group, device-generated
// pubkey).
//
// Under ADR-0012 the hub never sees a device's private key. The client
// generated it locally, submitted the pubkey, and the hub returns a
// config whose pki.key is left empty — the client splices in its own
// key (either inline PEM or a path to a sibling file) before running
// nebula.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// DeviceCertValidity bounds a device's leaf certificate. Shorter than
// a machine's five years because re-enrolling a device is one wallet
// signature against a hub that needs no memory of the last one — this
// is the one place where short-lived certs are a practical revocation
// strategy rather than a maintenance cliff.
const DeviceCertValidity = 90 * 24 * time.Hour

// PubkeyFingerprint returns the canonical sha256-hex fingerprint of a
// nebula X25519 pubkey. Used in the enrollment message the wallet
// signs (v1) and on the /status approval card so an operator can read
// it back before signing.
func PubkeyFingerprint(pub [32]byte) string {
	sum := sha256.Sum256(pub[:])
	return hex.EncodeToString(sum[:])
}

// EnrollDeviceParams is everything the mint needs. Populated by the
// verify+mint core once the wallet signature is confirmed.
type EnrollDeviceParams struct {
	Master []byte
	Name   string   // final approved name (normalized)
	Group  string   // GroupAdmins or GroupMedia
	Pubkey [32]byte // device-generated X25519 public key
}

// EnrollDevice mints a device's cert from a device-supplied pubkey and
// renders its nebula config. The returned config is nearly self-
// contained: pki.ca and pki.cert are inline PEM, pki.key is the empty
// string — the client splices in its own key before running nebula.
//
// Collision check: the requested name and its derived address must not
// clash with the git-derived zone (hub + machines). Devices are not in
// that zone; a device that collides with a machine label or address is
// refused here rather than after the cert bakes it in.
func (m *Manager) EnrollDevice(p EnrollDeviceParams) ([]byte, error) {
	if m.endpoint == "" {
		return nil, fmt.Errorf("mesh endpoint is not configured (--mesh-endpoint)")
	}
	if p.Group != GroupAdmins && p.Group != GroupMedia {
		return nil, fmt.Errorf("group must be %q or %q, got %q", GroupAdmins, GroupMedia, p.Group)
	}
	name := nebderive.Normalize(p.Name)
	if !dnsLabelRe.MatchString(name) {
		return nil, fmt.Errorf("device name %q is not a valid DNS label", p.Name)
	}

	byMAC, err := machines.Load(m.machinesDir())
	if err != nil {
		return nil, fmt.Errorf("loading machines: %w", err)
	}
	zone, err := buildMeshZone(p.Master, m.subnet, byMAC)
	if err != nil {
		return nil, fmt.Errorf("building mesh zone: %w", err)
	}
	if _, clash := zone[name]; clash {
		return nil, fmt.Errorf("device name %q collides with the git-derived mesh zone (a machine or the hub owns it)", name)
	}
	addr, err := nebderive.DeviceIP(p.Master, name, m.subnet)
	if err != nil {
		return nil, fmt.Errorf("deriving device address: %w", err)
	}
	for zn, za := range zone {
		if za == addr {
			return nil, fmt.Errorf("device %q would take address %s, already claimed by %q in the git zone (retry with a different name, or override the machine's meshIP)", name, addr, zn)
		}
	}

	return m.renderDeviceConfig(p.Master, name, p.Group, addr, p.Pubkey)
}

// renderDeviceConfig is the pure render step: given a verified
// (name, group, addr, pubkey), produce the nebula config. Split from
// EnrollDevice so tests can exercise it without a machines directory.
func (m *Manager) renderDeviceConfig(master []byte, name, group string, addr netip.Addr, pub [32]byte) ([]byte, error) {
	hubIP, err := nebderive.HubIP(m.subnet)
	if err != nil {
		return nil, err
	}
	policy, err := loadPolicy(m.root)
	if err != nil {
		return nil, fmt.Errorf("loading mesh policy: %w", err)
	}
	caPEM, err := nebderive.CACertPEM(master)
	if err != nil {
		return nil, fmt.Errorf("deriving mesh CA: %w", err)
	}
	now := time.Now()
	crt, err := nebderive.HostCert(master, name, pub, addr, m.subnet,
		[]string{group}, now.Add(-ClockSkew), now.Add(DeviceCertValidity))
	if err != nil {
		return nil, fmt.Errorf("minting cert for %q: %w", name, err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		return nil, fmt.Errorf("marshalling cert for %q: %w", name, err)
	}
	blocklist, err := LoadBlocklist(m.root)
	if err != nil {
		return nil, fmt.Errorf("loading mesh blocklist: %w", err)
	}

	cfg := ConfigYAML{
		PKI: nebPKIYAML{
			CA:   string(caPEM),
			Cert: string(crtPEM),
			// Key deliberately empty: the device's private key never
			// leaves the device. The client splices in its own key
			// (inline PEM or path to a sibling file) before starting
			// nebula.
			Key:       "",
			Blocklist: blocklist,
		},
		StaticHostMap: map[string][]string{hubIP.String(): {m.endpoint}},
		Lighthouse: nebLighthouseYAML{
			Interval: 60,
			Hosts:    []string{hubIP.String()},
			// Devices roam, so their own addresses are not worth
			// filtering — but a peer's pod-network address must never
			// be dialed: that is how nebula ends up tunnelled inside
			// another overlay and hairpinning through the hub.
			RemoteAllowList: nebUnderlayFilter(m.subnet),
		},
		// Port 0: devices roam. A fixed port buys a node the
		// lighthouse-less LAN fallback (nebmachine.go), but a laptop
		// that changes networks daily gains nothing from it and can
		// lose to whatever else holds 4242 on a coffee-shop NAT.
		Listen: nebListenYAML{Host: "0.0.0.0", Port: 0},
		// No dev name: device configs are portable by design (one file,
		// moved by scp/clipboard/QR) and Darwin rejects any name that
		// is not utun[0-9]+. Let each client pick its own.
		Tun:    &nebTunYAML{MTU: nebNodeMTU},
		Punchy: nebPunchyYAML{Punch: true, Respond: true},
		Relay:  nebRelayYAML{UseRelays: true, Relays: []string{hubIP.String()}},
		Firewall: nebFirewallYAML{
			// Outbound open: a device is the thing that initiates.
			// Inbound is the policy's who×what table (talos/
			// mesh-policy.yaml, device scope — rationale lives there).
			OutboundAction: "drop",
			InboundAction:  "drop",
			Outbound:       []nebRuleYAML{{Port: "any", Proto: "any", Host: "any"}},
			Inbound:        append([]nebRuleYAML(nil), policy.Device.Inbound...),
		},
		Logging: nebLoggingYAML{Level: "info", Format: "text"},
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling device config: %w", err)
	}
	return out, nil
}
