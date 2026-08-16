package mobile

// The tunnel runner: nebula over the file descriptor Android's
// VpnService.Builder.establish() hands the app. Kotlin owns the fd's
// lifecycle (and the VpnService routes/DNS, from ConfigInfo); Go owns
// the nebula instance. overlay.NewFdDeviceFromConfig is upstream
// nebula's own hook for exactly this embedding — no fork, no netstack:
// unlike the hub (TUN-less by necessity, see nebstack), the app has a
// real tun and the whole point is routing other apps' packets
// (Jellyfin) through it.

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"

	"github.com/slackhq/nebula"
	nebconfig "github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/policyclient"
)

// tunnelBuildVersion is what the app reports to peers in handshakes.
const tunnelBuildVersion = "talos-config-mobile"

// Tunnel is a running nebula instance bound to a VpnService tun fd.
// gomobile binds this as a Java object; the VpnService holds it for
// the life of the session and calls Stop on revocation/teardown.
type Tunnel struct {
	control *nebula.Control
	dns     *dnsDevice

	// Live policy sync (task 6462fed4 phase 3). cfg is the running
	// instance's config object: ReloadConfigString on it makes nebula
	// rebuild the firewall in place (conntrack preserved, tunnel
	// untouched). cfgYAML tracks the currently-applied text so each
	// splice starts from what is actually running, policyEpoch the
	// last applied /policy epoch.
	polMu       sync.Mutex
	cfg         *nebconfig.C
	cfgYAML     string
	hubIP       netip.Addr
	policyEpoch string
}

// SetUpstreams replaces the underlay resolvers for non-mesh DNS.
// Kotlin calls this from a ConnectivityManager callback whenever the
// underlying network's link properties change — the resolvers captured
// at start go stale the moment the device roams wifi↔cellular. Same
// format as NewTunnel's upstreamDNS; empty/garbage input is ignored
// (the previous resolvers stay).
func (t *Tunnel) SetUpstreams(upstreamDNS string) {
	if t.dns == nil || strings.TrimSpace(upstreamDNS) == "" {
		return
	}
	t.dns.setUpstreams(parseUpstreams(upstreamDNS))
}

// NewTunnel starts nebula from a completed (key-spliced) config on the
// given tun file descriptor. The fd must already be configured by the
// VpnService builder with the values from ConfigInfo — including the
// magic DNS resolver (dnsIP), which the split-DNS shim wrapped around
// the device answers (see dnsshim.go).
//
// logSink: "" logs to stderr (logcat swallows it); a path appends to
// that file instead, so the app can show a debug log screen.
//
// upstreamDNS: comma-separated underlay resolvers ("ip" or "ip:port")
// for non-mesh queries — Kotlin reads them off the active network.
// Empty falls back to 1.1.1.1. protector marks underlay sockets as
// VPN-bypassing (VpnService.protect); nil skips protection (tests).
func NewTunnel(cfgYAML string, tunFd int, logSink string, upstreamDNS string, protector SocketProtector) (*Tunnel, error) {
	var c nebconfig.C
	if err := c.LoadString(cfgYAML); err != nil {
		return nil, fmt.Errorf("loading nebula config: %w", err)
	}

	var sink io.Writer = os.Stderr
	if logSink != "" {
		f, err := os.OpenFile(logSink, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening log sink: %w", err)
		}
		sink = f
	}
	logger := logging.NewLogger(sink)
	// nebula.Main deliberately does not apply logging config itself, so
	// an embedder that wants logging.level honored has to ask (same as
	// the hub's startMeshNebula).
	if err := logging.ApplyConfig(logger, &c); err != nil {
		return nil, fmt.Errorf("applying nebula logging config: %w", err)
	}

	// The hub's overlay address is where mesh-zone queries go; the
	// lighthouse host doubles as the resolver (same assumption as
	// ConfigInfo's hubIP).
	var hubIP netip.Addr
	if hosts := c.GetStringSlice("lighthouse.hosts", nil); len(hosts) > 0 {
		hubIP, _ = netip.ParseAddr(hosts[0])
	}
	if !hubIP.IsValid() {
		return nil, fmt.Errorf("config has no lighthouse host (needed as the mesh DNS target)")
	}
	upstreams := parseUpstreams(upstreamDNS)

	fd := tunFd
	inner := overlay.NewFdDeviceFromConfig(&fd)
	// nebula.Main runs the factory synchronously, so shim is set before
	// Main returns and the Tunnel below can hold it for SetUpstreams.
	var shim *dnsDevice
	deviceFactory := func(cfg *nebconfig.C, l *slog.Logger, vpnNetworks []netip.Prefix, routines int) (overlay.Device, error) {
		dev, err := inner(cfg, l, vpnNetworks, routines)
		if err != nil {
			return nil, err
		}
		shim, err = newDNSDevice(dev, hubIP, upstreams, protector)
		if err != nil {
			return nil, err
		}
		return shim, nil
	}
	control, err := nebula.Main(&c, false, tunnelBuildVersion, logger, deviceFactory)
	if err != nil {
		return nil, fmt.Errorf("nebula: %w", err)
	}
	if err := control.Start(); err != nil {
		control.Stop()
		return nil, fmt.Errorf("starting nebula: %w", err)
	}
	return &Tunnel{control: control, dns: shim, cfg: &c, cfgYAML: cfgYAML, hubIP: hubIP}, nil
}

// SyncPolicy polls the hub's GET /policy over the tunnel and, when the
// epoch changed since the last apply, splices the new inbound rules
// into the running config and hot-reloads nebula's firewall — no
// tunnel restart, no re-enrollment, identity untouched (policy is
// payload). Returns true when a new rule set was applied.
//
// Kotlin calls this on a timer. Failures are expected weather — a
// sealed hub (every fly deploy) makes /policy unreachable — so the
// caller should log-and-carry-on: the device keeps running on the
// rules it has, which is always safe.
func (t *Tunnel) SyncPolicy() (bool, error) {
	t.polMu.Lock()
	defer t.polMu.Unlock()
	return t.syncPolicy("http://" + t.hubIP.String())
}

// syncPolicy is SyncPolicy against an explicit base URL (tests point
// it at an httptest server; production at the hub's overlay address).
// Caller holds polMu.
func (t *Tunnel) syncPolicy(base string) (bool, error) {
	if t.cfg == nil {
		return false, errors.New("tunnel not started")
	}
	wire, err := policyclient.Fetch(policyclient.HTTPClient, base)
	if err != nil {
		return false, err
	}
	if wire.Epoch == t.policyEpoch {
		return false, nil
	}
	spliced, err := policyclient.SpliceInbound([]byte(t.cfgYAML), wire.Inbound)
	if err != nil {
		return false, fmt.Errorf("splicing policy: %w", err)
	}
	if err := t.cfg.ReloadConfigString(string(spliced)); err != nil {
		return false, fmt.Errorf("reloading nebula config: %w", err)
	}
	t.cfgYAML = string(spliced)
	t.policyEpoch = wire.Epoch
	return true, nil
}

// parseUpstreams turns Kotlin's comma-separated resolver list into
// addr:ports, defaulting bare addresses to :53 and an empty/garbage
// list to 1.1.1.1 — losing general DNS to a parse error would be the
// exact all-or-nothing failure the shim exists to avoid.
func parseUpstreams(s string) []netip.AddrPort {
	var out []netip.AddrPort
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ap, err := netip.ParseAddrPort(part); err == nil {
			out = append(out, ap)
			continue
		}
		if a, err := netip.ParseAddr(part); err == nil {
			out = append(out, netip.AddrPortFrom(a, dnsPort))
		}
	}
	if len(out) == 0 {
		out = append(out, netip.AddrPortFrom(netip.AddrFrom4([4]byte{1, 1, 1, 1}), dnsPort))
	}
	return out
}

// DebugJSON snapshots the split-DNS shim for the app's debug screen:
// static addressing (magic resolver, hub, zone), the live underlay
// upstreams, per-route counters, and the most recent DNS decisions
// (newest last). Read-only; call any time while the tunnel runs. The
// meshQueries/hubReplies gap is the sealed-hub tell (see dnsCounters).
func (t *Tunnel) DebugJSON() (string, error) {
	if t.dns == nil {
		return "", errors.New("no DNS shim (tunnel not started)")
	}
	return marshal(t.dns.debugSnapshot())
}

// Stop tears the tunnel down. Idempotent enough for a VpnService
// onDestroy: nebula's Stop signals shutdown and waits for the goroutines.
func (t *Tunnel) Stop() {
	if t.control != nil {
		t.control.Stop()
		t.control = nil
	}
}
