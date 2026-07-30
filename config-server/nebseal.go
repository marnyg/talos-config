package main

// Mesh lifecycle. The mesh unseals from the *same* master as wg0 — one
// derivation tree, two overlays — so there is no second unseal flow and
// no second secret. nebManager owns only what nebula needs: the config,
// the netstack, and the DNS listener.
//
// Phase 1 runs the mesh beside wg0, which still carries production
// traffic (talosconfig, KMS, auto-bootstrap). That asymmetry decides the
// failure policy below: a mesh that fails to start must not take the
// unseal with it.

import (
	"fmt"
	"log"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

// nebManager owns the mesh side of an unseal. Created when the mesh is
// enabled (--mesh-port) and idle until the hub is unsealed.
type nebManager struct {
	port       int
	subnet     netip.Prefix // mesh CIDR; the hub's address is derived, not configured
	listenHost string
	endpoint   string      // hub's public host:port, injected into node configs
	dnsZone    string      // "" = no mesh DNS
	devices    []nebDevice // named devices whose identities are derived
	root       string      // talos/ directory

	// start is startMeshNebula, stubbed in tests.
	start func(cfg []byte) (*nebstack.Service, error)

	mu   sync.Mutex
	svc  *nebstack.Service // nil until unsealed
	zone map[string]netip.Addr
	err  error // last startup failure, surfaced by state()
}

// nebDevice is one enrollable device: a derived identity plus the group
// its certificate will carry. Declared, not registered — the list says
// which names are allowed to enroll and what access they get, and the
// identity itself is a pure function of (master, name).
type nebDevice struct {
	name  string
	group string // nebGroupAdmins or nebGroupMedia
}

func newNebManager(port int, subnet netip.Prefix, listenHost, endpoint, dnsZone, root string, devices []nebDevice) *nebManager {
	return &nebManager{
		port:       port,
		subnet:     subnet,
		listenHost: listenHost,
		endpoint:   endpoint,
		dnsZone:    dnsZone,
		devices:    devices,
		root:       root,
		start:      startMeshNebula,
	}
}

// machinesDir is where declared machines live under the talos tree.
func (m *nebManager) machinesDir() string {
	return filepath.Join(m.root, "machines")
}

// hubIP is the hub's overlay address: derived from the subnet, never
// configured, so static_host_map entries and the resolver address are
// knowable from the CIDR alone.
func (m *nebManager) hubIP() (netip.Addr, error) {
	return nebderive.HubIP(m.subnet)
}

// state reports the live service, the zone, and the last startup error.
func (m *nebManager) state() (*nebstack.Service, map[string]netip.Addr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.svc, m.zone, m.err
}

func (m *nebManager) up() bool {
	svc, _, _ := m.state()
	return svc != nil
}

// unsealWithMaster renders the hub's config from the master, brings the
// mesh up, and starts mesh DNS. Idempotent: nebula cannot be restarted
// in-process, so a second call is a no-op.
//
// Errors are returned for the caller to decide on; see the fan-out in
// wgManager.unsealWithMaster for why phase 1 treats them as non-fatal.
func (m *nebManager) unsealWithMaster(master []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.svc != nil {
		return nil
	}
	m.err = nil

	// The zone is built before nebula starts, and validated even when
	// the start is stubbed: a duplicate label or a colliding address is
	// a repo mistake, and it should fail before an overlay exists rather
	// than after certs have been minted against it.
	var zone map[string]netip.Addr
	if m.dnsZone != "" {
		machines, err := loadMachines(m.machinesDir())
		if err != nil {
			return m.fail(fmt.Errorf("loading machines: %w", err))
		}
		if zone, err = buildMeshZone(master, m.subnet, machines, m.devices); err != nil {
			return m.fail(fmt.Errorf("building mesh zone: %w", err))
		}
	}

	blocklist, err := loadMeshBlocklist(m.root)
	if err != nil {
		return m.fail(fmt.Errorf("loading mesh blocklist: %w", err))
	}

	cfg, err := hubNebulaConfig(nebHubParams{
		master:     master,
		subnet:     m.subnet,
		listenHost: m.listenHost,
		listenPort: m.port,
		serveDNS:   m.dnsZone != "",
		blocklist:  blocklist,
	})
	if err != nil {
		return m.fail(fmt.Errorf("rendering mesh config: %w", err))
	}

	svc, err := m.start(cfg)
	if err != nil {
		return m.fail(fmt.Errorf("starting mesh: %w", err))
	}
	m.svc, m.zone = svc, zone

	if svc != nil && zone != nil {
		if err := serveMeshDNS(svc, zone, m.dnsZone); err != nil {
			return m.fail(fmt.Errorf("starting mesh dns: %w", err))
		}
	}

	hubIP, _ := nebderive.HubIP(m.subnet)
	fp, err := nebderive.CAFingerprint(master)
	if err != nil {
		return m.fail(fmt.Errorf("fingerprinting mesh CA: %w", err))
	}
	// The CA fingerprint is logged because it is the value every member
	// pins and the one an out-of-band channel would publish; it is a
	// public commitment, not a secret.
	if svc != nil {
		log.Printf("mesh up: lighthouse+relay %s on udp/%d, CA %s", hubIP, m.port, fp)
	} else {
		log.Printf("mesh configured (start stubbed): %s on udp/%d, CA %s", hubIP, m.port, fp)
	}
	for _, d := range m.devices {
		ip, err := nebderive.DeviceIP(master, d.name, m.subnet)
		if err != nil {
			continue
		}
		log.Printf("mesh device %q (%s): overlay address %s", d.name, d.group, ip)
	}
	return nil
}

// meshMemberRow is one /status mesh-table line: a derived member joined
// with its live tunnel state from the embedded nebula hostmap.
type meshMemberRow struct {
	Name, Group, Addr, Tunnel, Endpoint, Relays string
}

// members lists every derived mesh member with live tunnel state.
// Offline members still appear: membership is derived from git +
// declarations, not from who happens to be connected. Nil until the
// zone exists (built at unseal), so a sealed hub shows nothing — the
// same rule the DNS zone follows.
func (m *nebManager) members() []meshMemberRow {
	svc, zone, _ := m.state()
	if zone == nil {
		return nil
	}
	var live []nebula.ControlHostInfo
	if svc != nil {
		live = svc.Peers()
	}
	return meshMemberRows(zone, m.devices, m.endpoint, live)
}

// meshMemberRows is the pure join: derived membership × live hostmap.
// Separated from nebManager so tests can fabricate hostmap entries
// without a running nebula.
func meshMemberRows(zone map[string]netip.Addr, devices []nebDevice, hubEndpoint string, live []nebula.ControlHostInfo) []meshMemberRow {
	groups := map[string]string{}
	for _, d := range devices {
		groups[d.name] = d.group
	}
	// Reverse zone, so relay targets render as names, not addresses.
	names := map[netip.Addr]string{}
	for n, a := range zone {
		names[a] = n
	}
	byAddr := map[netip.Addr]nebula.ControlHostInfo{}
	for _, hi := range live {
		if len(hi.VpnAddrs) > 0 {
			byAddr[hi.VpnAddrs[0]] = hi
		}
	}

	var rows []meshMemberRow
	for _, n := range slices.Sorted(maps.Keys(zone)) {
		addr := zone[n]
		row := meshMemberRow{Name: n, Addr: addr.String(), Tunnel: "—", Endpoint: "—", Relays: "—"}
		if n == nebderive.HubName {
			// The hub is this process: no tunnel to itself, and its
			// endpoint is configuration, not hostmap observation.
			row.Group = "lighthouse+relay"
			row.Tunnel = "self"
			row.Endpoint = hubEndpoint
			rows = append(rows, row)
			continue
		}
		row.Group = groups[n]
		if row.Group == "" {
			row.Group = nebGroupMachines
		}
		if hi, ok := byAddr[addr]; ok {
			row.Tunnel = "up"
			if len(hi.CurrentRelaysToMe) > 0 {
				// The hub needs a relay to reach this member — should
				// never happen while the hub is the only relay, so it
				// is worth surfacing loudly if it does.
				row.Tunnel = "up (via relay)"
			}
			if hi.CurrentRemote.IsValid() {
				row.Endpoint = hi.CurrentRemote.String()
			}
			if len(hi.CurrentRelaysThroughMe) > 0 {
				var ts []string
				for _, a := range hi.CurrentRelaysThroughMe {
					if peer, ok := names[a]; ok {
						ts = append(ts, peer)
					} else {
						ts = append(ts, a.String())
					}
				}
				slices.Sort(ts)
				row.Relays = strings.Join(ts, ", ")
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// fail records and returns err, so a later /status or /sealed can say
// why the mesh is down instead of only that it is.
func (m *nebManager) fail(err error) error {
	m.err = err
	return err
}

// startMeshNebula boots nebula from a rendered config and wraps it in
// the netstack. overlay.NewUserDeviceFromConfig is the TUN-less device
// factory — the hub has no TUN, which is the whole reason nebstack
// exists.
func startMeshNebula(cfg []byte) (*nebstack.Service, error) {
	var c config.C
	if err := c.LoadString(string(cfg)); err != nil {
		return nil, fmt.Errorf("loading nebula config: %w", err)
	}
	logger := logging.NewLogger(os.Stderr)
	// nebula.Main deliberately does not apply logging config itself, so
	// an embedder that wants logging.level honored has to ask.
	if err := logging.ApplyConfig(logger, &c); err != nil {
		return nil, fmt.Errorf("applying nebula logging config: %w", err)
	}
	control, err := nebula.Main(&c, false, meshBuildVersion, logger, overlay.NewUserDeviceFromConfig)
	if err != nil {
		return nil, fmt.Errorf("nebula: %w", err)
	}
	svc, err := nebstack.New(control)
	if err != nil {
		return nil, fmt.Errorf("netstack: %w", err)
	}
	return svc, nil
}

// meshBuildVersion is what the hub reports to peers in handshakes.
const meshBuildVersion = "talos-config-hub"

// parseMeshDevices splits the two device lists into one declared set,
// normalizing names the same way the derivation does so a stray capital
// cannot produce a different identity than the one an enrolled device
// holds.
//
// A name in both lists is an error rather than a precedence rule: the
// group is signed into the cert, so "which group did laptop get?" must
// have one answer, and guessing it silently is how a TV ends up with
// admin access.
func parseMeshDevices(admins, media string) ([]nebDevice, error) {
	var out []nebDevice
	seen := map[string]string{}
	for _, spec := range []struct {
		list  string
		group string
	}{{admins, nebGroupAdmins}, {media, nebGroupMedia}} {
		for _, name := range strings.Split(spec.list, ",") {
			n := nebderive.Normalize(name)
			if n == "" {
				continue
			}
			if prev, dup := seen[n]; dup {
				return nil, fmt.Errorf("device %q is declared in both the %s and %s groups", n, prev, spec.group)
			}
			seen[n] = spec.group
			out = append(out, nebDevice{name: n, group: spec.group})
		}
	}
	return out, nil
}

// device returns the declared device with this name. Enrollment refuses
// anything else: a name that is not declared has no group, and a cert
// without a group reaches nothing.
func (m *nebManager) device(name string) (nebDevice, bool) {
	name = nebderive.Normalize(name)
	for _, d := range m.devices {
		if d.name == name {
			return d, true
		}
	}
	return nebDevice{}, false
}

// resolveMeshListenHost picks the address nebula binds.
//
// On fly the hostname is passed through verbatim: fly's UDP proxy only
// delivers to that address, and nebula resolves listen.host itself, so
// unlike wireguard-go there is no bind shim. Off fly the name does not
// exist, and passing it would fail the start, so fall back to the
// wildcard for local runs.
func resolveMeshListenHost() string {
	if !resolveFlyGlobalServices().IsUnspecified() {
		return nebFlyListenHost
	}
	return "0.0.0.0"
}
