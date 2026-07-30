// Package mesh owns the hub's overlay network: the nebula lifecycle
// (Manager), the hub/node/device config renderers, mesh DNS, the
// git-managed cert blocklist, and the overlay HTTP surface. Everything
// is derived — given the master, every config is a pure function of
// (git, master), so nothing here is stored or remembered (invariant 2).
// The wallet-gated HTTP handlers that hand these configs out stay with
// the server in package main.
package mesh

// Mesh lifecycle. The mesh unseals from the hub's one master — one
// derivation tree, one overlay — so there is no second unseal flow and
// no second secret. Manager owns only what nebula needs: the config,
// the netstack, and the DNS listener.
//
// The mesh is the control channel (phase 2 deleted wg0). A mesh that
// fails to start does not take the unseal with it — KMS disk unlocks
// must not depend on the overlay (invariant 4) — but it does turn
// /sealed into a 503: see hubManager.unsealWithMaster in main.

import (
	"context"
	"fmt"
	"log"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

// Manager owns the mesh side of an unseal. Created when the mesh is
// enabled (--mesh-port) and idle until the hub is unsealed.
type Manager struct {
	port       int
	subnet     netip.Prefix // mesh CIDR; the hub's address is derived, not configured
	listenHost string
	endpoint   string   // hub's public host:port, injected into node configs
	dnsZone    string   // "" = no mesh DNS
	devices    []Device // named devices whose identities are derived
	root       string   // talos/ directory

	// Start boots nebula from a rendered config. Defaults to
	// startMeshNebula; exported so tests can stub the socket away (the
	// deviceflow.Store.Now pattern).
	Start func(cfg []byte) (*nebstack.Service, error)

	// TunnelConfig serves GET /config on the overlay listener to admin
	// devices (set by main; nil disables the route).
	TunnelConfig http.Handler

	mu   sync.Mutex
	svc  *nebstack.Service // nil until unsealed
	zone map[string]netip.Addr
	err  error // last startup failure, surfaced by State()
}

// Device is one enrollable device: a derived identity plus the group
// its certificate will carry. Declared, not registered — the list says
// which names are allowed to enroll and what access they get, and the
// identity itself is a pure function of (master, name).
type Device struct {
	Name  string
	Group string // GroupAdmins or GroupMedia
}

func NewManager(port int, subnet netip.Prefix, listenHost, endpoint, dnsZone, root string, devices []Device) *Manager {
	return &Manager{
		port:       port,
		subnet:     subnet,
		listenHost: listenHost,
		endpoint:   endpoint,
		dnsZone:    dnsZone,
		devices:    devices,
		root:       root,
		Start:      startMeshNebula,
	}
}

// Endpoint is the hub's public host:port mesh members dial.
func (m *Manager) Endpoint() string { return m.endpoint }

// Subnet is the mesh overlay CIDR.
func (m *Manager) Subnet() netip.Prefix { return m.subnet }

// DNSZone is the zone the hub serves on the mesh ("" = no mesh DNS).
func (m *Manager) DNSZone() string { return m.dnsZone }

// machinesDir is where declared machines live under the talos tree.
func (m *Manager) machinesDir() string {
	return filepath.Join(m.root, "machines")
}

// State reports the live service, the zone, and the last startup error.
func (m *Manager) State() (*nebstack.Service, map[string]netip.Addr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.svc, m.zone, m.err
}

func (m *Manager) Up() bool {
	svc, _, _ := m.State()
	return svc != nil
}

// UnsealWithMaster renders the hub's config from the master, brings the
// mesh up, and starts mesh DNS and HTTP. Idempotent: nebula cannot be
// restarted in-process, so a second call is a no-op.
//
// Errors are returned for the caller to decide on; see the fan-out in
// main's hubManager.unsealWithMaster for why they do not fail the unseal.
func (m *Manager) UnsealWithMaster(master []byte) error {
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
		byMAC, err := machines.Load(m.machinesDir())
		if err != nil {
			return m.fail(fmt.Errorf("loading machines: %w", err))
		}
		if zone, err = buildMeshZone(master, m.subnet, byMAC, m.devices); err != nil {
			return m.fail(fmt.Errorf("building mesh zone: %w", err))
		}
	}

	blocklist, err := LoadBlocklist(m.root)
	if err != nil {
		return m.fail(fmt.Errorf("loading mesh blocklist: %w", err))
	}

	cfg, err := HubConfig(HubParams{
		Master:     master,
		Subnet:     m.subnet,
		ListenHost: m.listenHost,
		ListenPort: m.port,
		ServeDNS:   m.dnsZone != "",
		Blocklist:  blocklist,
	})
	if err != nil {
		return m.fail(fmt.Errorf("rendering mesh config: %w", err))
	}

	svc, err := m.Start(cfg)
	if err != nil {
		return m.fail(fmt.Errorf("starting mesh: %w", err))
	}
	m.svc, m.zone = svc, zone

	if svc != nil && zone != nil {
		if err := serveMeshDNS(svc, zone, m.dnsZone); err != nil {
			return m.fail(fmt.Errorf("starting mesh dns: %w", err))
		}
	}

	if svc != nil {
		if err := m.serveMeshHTTP(svc, master); err != nil {
			return m.fail(fmt.Errorf("starting mesh http: %w", err))
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
		ip, err := nebderive.DeviceIP(master, d.Name, m.subnet)
		if err != nil {
			continue
		}
		log.Printf("mesh device %q (%s): overlay address %s", d.Name, d.Group, ip)
	}
	return nil
}

// MemberRow is one /status mesh-table line: a derived member joined
// with its live tunnel state from the embedded nebula hostmap.
type MemberRow struct {
	Name, Group, Addr, Tunnel, Endpoint, Relays string
}

// Members lists every derived mesh member with live tunnel state.
// Offline members still appear: membership is derived from git +
// declarations, not from who happens to be connected. Nil until the
// zone exists (built at unseal), so a sealed hub shows nothing — the
// same rule the DNS zone follows.
func (m *Manager) Members() []MemberRow {
	svc, zone, _ := m.State()
	if zone == nil {
		return nil
	}
	var live []nebula.ControlHostInfo
	if svc != nil {
		live = svc.Peers()
	}
	return memberRows(zone, m.devices, m.endpoint, live)
}

// memberRows is the pure join: derived membership × live hostmap.
// Separated from the Manager so tests can fabricate hostmap entries
// without a running nebula.
func memberRows(zone map[string]netip.Addr, devices []Device, hubEndpoint string, live []nebula.ControlHostInfo) []MemberRow {
	groups := map[string]string{}
	for _, d := range devices {
		groups[d.Name] = d.Group
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

	var rows []MemberRow
	for _, n := range slices.Sorted(maps.Keys(zone)) {
		addr := zone[n]
		row := MemberRow{Name: n, Addr: addr.String(), Tunnel: "—", Endpoint: "—", Relays: "—"}
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
			row.Group = GroupMachines
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
func (m *Manager) fail(err error) error {
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

// ParseDevices splits the two device lists into one declared set,
// normalizing names the same way the derivation does so a stray capital
// cannot produce a different identity than the one an enrolled device
// holds.
//
// A name in both lists is an error rather than a precedence rule: the
// group is signed into the cert, so "which group did laptop get?" must
// have one answer, and guessing it silently is how a TV ends up with
// admin access.
func ParseDevices(admins, media string) ([]Device, error) {
	var out []Device
	seen := map[string]string{}
	for _, spec := range []struct {
		list  string
		group string
	}{{admins, GroupAdmins}, {media, GroupMedia}} {
		for _, name := range strings.Split(spec.list, ",") {
			n := nebderive.Normalize(name)
			if n == "" {
				continue
			}
			if prev, dup := seen[n]; dup {
				return nil, fmt.Errorf("device %q is declared in both the %s and %s groups", n, prev, spec.group)
			}
			seen[n] = spec.group
			out = append(out, Device{Name: n, Group: spec.group})
		}
	}
	return out, nil
}

// Device returns the declared device with this name. Enrollment refuses
// anything else: a name that is not declared has no group, and a cert
// without a group reaches nothing.
func (m *Manager) Device(name string) (Device, bool) {
	name = nebderive.Normalize(name)
	for _, d := range m.devices {
		if d.Name == name {
			return d, true
		}
	}
	return Device{}, false
}

// ResolveListenHost picks the address nebula binds.
//
// On fly the hostname is passed through verbatim: fly's UDP proxy only
// delivers to that address, and nebula resolves listen.host itself, so
// unlike wireguard-go there is no bind shim. Off fly the name does not
// exist, and passing it would fail the start, so fall back to the
// wildcard for local runs.
func ResolveListenHost() string {
	if !resolveFlyGlobalServices().IsUnspecified() {
		return nebFlyListenHost
	}
	return "0.0.0.0"
}

// resolveFlyGlobalServices returns the address of fly's UDP routing,
// or the unspecified address when not running on fly. The lookup is
// bounded: off-fly the name doesn't exist and some resolvers take ~20s
// to say so, which would stall every local mesh-enabled start.
func resolveFlyGlobalServices() netip.Addr {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", "fly-global-services")
	if err == nil {
		for _, ip := range ips {
			if a, ok := netip.AddrFromSlice(ip.To4()); ok && ip.To4() != nil {
				return a
			}
		}
	}
	return netip.IPv4Unspecified()
}
