//go:build iroh && linux

package mobile

// The tunnel: the member (nodeagent) plus the presentation (meshtun)
// on the fd Android's VpnService.Builder.establish() hands the app.
// Kotlin owns the fd's creation and the route/address/DNS plumbing
// (the constants in mobile.go); Go owns everything on it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/fakeip"
	"github.com/marnyg/talos-config/config-server/meshtun"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
)

// SocketProtector is implemented in Kotlin with VpnService.protect:
// the DNS forwarder's underlay sockets are marked VPN-bypassing so a
// query can never loop back into the tun. With split routing it is
// belt-and-braces; kept so the app also works if the routes are ever
// wider.
type SocketProtector interface {
	Protect(fd int32) bool
}

// Tunnel is one running session. gomobile binds it as a Java object;
// the VpnService holds it for the life of the session and calls Stop
// on revocation/teardown.
type Tunnel struct {
	agent   *nodeagent.Agent
	tun     *meshtun.Tun
	link    interface{ Close() }
	cancel  context.CancelFunc
	log     *log.Logger
	logFile *os.File
	relay   string
	started time.Time
	closed  atomic.Bool

	mu       sync.Mutex
	fatalErr string
	done     chan struct{}
}

// Start runs the member on tunFd: loads the key + Kit under stateDir
// (Enroll first — a tunnel with no Kit is an error, not a wait), binds
// the NodeId on iroh homed at relay ("" ⇒ hub), starts the beat loop,
// and serves the fake-IP presentation on the fd. Kotlin must have
// established the tun with address TunIP/32, route FakeRange/
// FakePrefixLen, DNS server ResolverIP and MTU = mtu, and detached
// the fd: Go reads it until Stop, Kotlin closes it after.
//
// upstreamDNS is "ip[:port][,…]": the underlying network's resolvers,
// where every non-mesh query goes (Android sends all DNS to the VPN's
// server). localAddrs is "ip[,ip]": the underlay's IPv4 addresses from
// LinkProperties, advertised as direct addresses so a LAN peer can
// punch (belt-and-braces to iroh's own enumeration, P0.2 finding).
// logSink "" logs to stderr (logcat swallows it); a path appends there.
func Start(stateDir, hub, relay string, tunFd, mtu int, upstreamDNS, localAddrs, logSink string, protector SocketProtector) (*Tunnel, error) {
	if !Enrolled(stateDir) {
		return nil, errors.New("not enrolled")
	}
	hub = strings.TrimRight(hub, "/")
	if relay == "" {
		relay = hub
	}
	if mtu <= 0 {
		mtu = DefaultMTU
	}
	var sink io.Writer = os.Stderr
	var lf *os.File
	if logSink != "" {
		f, err := os.OpenFile(logSink, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening log sink: %w", err)
		}
		sink, lf = f, f
	}
	logger := log.New(sink, "", log.Ltime)
	log.SetOutput(sink) // fakeip's and the transport's package-level lines
	log.SetFlags(log.Ltime)
	irohtransport.SetLogLevel(os.Getenv("P0_LOG")) // trace|debug|info|warn

	a, err := nodeagent.Start(nodeagent.Options{
		Config: nodeagent.Config{Hub: hub, Relay: relay},
		State:  nodeagent.State{Dir: stateDir},
		Log:    logger,
	})
	if err != nil {
		closeFile(lf)
		return nil, fmt.Errorf("start: %w", err)
	}
	advertiseLocal(a, localAddrs, logger)

	link, err := fakeip.NewFDLink(tunFd, uint32(mtu))
	if err != nil {
		_ = a.Close()
		closeFile(lf)
		return nil, err
	}
	var control func(network, address string, c syscall.RawConn) error
	if protector != nil {
		control = func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) { protector.Protect(int32(fd)) })
		}
	}
	mt, err := meshtun.Start(meshtun.Options{Agent: a, Link: link, Upstreams: upstreamDNS, DialControl: control, Log: logger})
	if err != nil {
		_ = a.Close()
		closeFile(lf)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Tunnel{agent: a, tun: mt, link: link, cancel: cancel, log: logger, logFile: lf, relay: relay, started: time.Now(), done: make(chan struct{})}
	go func() {
		defer close(t.done)
		if err := a.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Printf("fatal: %v", err)
			t.mu.Lock()
			t.fatalErr = err.Error()
			t.mu.Unlock()
		}
	}()
	logger.Printf("tunnel up: fd=%d mtu=%d node=%s hub=%s relay=%s", tunFd, mtu, a.ID(), hub, relay)
	return t, nil
}

// advertiseLocal adds ip:<bound UDP port> for each IPv4 in localAddrs
// as an external address of the endpoint, so a LAN peer learns where
// to punch.
func advertiseLocal(a *nodeagent.Agent, localAddrs string, logger *log.Logger) {
	ep := a.Endpoint().Raw()
	port := ""
	for _, s := range ep.BoundSockets() {
		if host, p, err := net.SplitHostPort(s); err == nil && net.ParseIP(host).To4() != nil {
			port = p
			break
		}
	}
	if port == "" {
		return
	}
	for _, ip := range strings.Split(localAddrs, ",") {
		ip = strings.TrimSpace(ip)
		if p := net.ParseIP(ip); p == nil || p.To4() == nil || p.IsLoopback() {
			continue
		}
		addr := net.JoinHostPort(ip, port)
		if err := ep.AddExternalAddr(addr); err != nil {
			logger.Printf("add external addr %s: %v", addr, err)
			continue
		}
		logger.Printf("advertising %s", addr)
	}
}

// SetUpstreams replaces the underlay resolvers (same format as Start's
// upstreamDNS). Kotlin calls it from a ConnectivityManager callback
// whenever the underlying network's link properties change.
func (t *Tunnel) SetUpstreams(upstreamDNS string) {
	if t.closed.Load() {
		return
	}
	t.tun.Resolver.SetUpstreams(upstreamDNS)
}

// NetworkChanged is the app's word that the underlay moved (wifi ↔
// cellular, a new link): the new addresses are advertised, the hub
// HTTPS pool is dropped, and a beat is kicked so the member's
// reach-me-at and its view of the plane are fresh. iroh-ffi's own
// network monitor is dead inside an Android app (P2.4 finding .2: no
// ndk_context), so this is the redial trigger for both planes.
func (t *Tunnel) NetworkChanged(localAddrs string) {
	if t.closed.Load() {
		return
	}
	advertiseLocal(t.agent, localAddrs, t.log)
	t.agent.NetworkChanged()
}

// Stop tears the tunnel down. Idempotent. Kotlin calls it from
// onDestroy / onRevoke, then closes the fd.
func (t *Tunnel) Stop() {
	if !t.closed.CompareAndSwap(false, true) {
		return
	}
	t.cancel()
	t.link.Close()
	t.tun.Close()
	<-t.done
	_ = t.agent.Close()
	t.log.Printf("tunnel stopped")
	log.SetOutput(os.Stderr)
	closeFile(t.logFile)
}

// NodeID is this device's NodeId.
func (t *Tunnel) NodeID() string { return string(t.agent.ID()) }

// StatusJSON is a snapshot for the app: identity, beats, counters,
// the resolver's table, and the last fatal error if the member loop
// died (a denied enrollment, a dead token — not transient weather,
// which the loop retries itself).
func (t *Tunnel) StatusJSON() string {
	type view struct {
		Node       string   `json:"node"`
		Name       string   `json:"name"`
		Groups     []string `json:"groups"`
		Relay      string   `json:"relay"`
		UptimeS    int64    `json:"uptimeS"`
		Beats      int64    `json:"beats"`
		Attempts   int64    `json:"beatAttempts"`
		Names      int      `json:"namesKnown"`
		Flows      int64    `json:"flows"`
		FlowsOpen  int64    `json:"flowsOpen"`
		FlowErrors int64    `json:"flowErrors"`
		BytesIn    int64    `json:"bytesIn"`
		BytesOut   int64    `json:"bytesOut"`
		DNSMesh    int64    `json:"dnsMesh"`
		DNSForward int64    `json:"dnsForward"`
		DNSFailed  int64    `json:"dnsFailed"`
		DNSRefused int64    `json:"dnsRefused"`
		Upstreams  []string `json:"upstreams"`
		Table      []string `json:"table"`
		Paths      []string `json:"paths"`
		Endpoints  []string `json:"endpoints"`
		Fatal      string   `json:"fatal,omitempty"`
	}
	v := view{Node: t.NodeID(), Relay: t.relay, UptimeS: int64(time.Since(t.started).Seconds())}
	if kit := t.agent.Kit(); kit != nil {
		v.Name, v.Groups = kit.Member.Cav.Name, kit.Member.Cav.Groups
	}
	if b := t.agent.Bundle(); b != nil {
		v.Names = len(b.NameMap)
	}
	v.Beats, v.Attempts = t.agent.Beats(), t.agent.BeatAttempts()
	st := &t.tun.Stats
	v.Flows, v.FlowsOpen, v.FlowErrors = st.Flows.Load(), st.FlowsOpen.Load(), st.FlowErrors.Load()
	v.BytesIn, v.BytesOut = st.BytesIn.Load(), st.BytesOut.Load()
	rs := &t.tun.Resolver.Stats
	v.DNSMesh, v.DNSForward, v.DNSFailed, v.DNSRefused = rs.Mesh.Load(), rs.Forward.Load(), rs.Failed.Load(), rs.Refused.Load()
	for _, u := range t.tun.Resolver.Upstreams() {
		v.Upstreams = append(v.Upstreams, u.String())
	}
	v.Table = t.tun.Resolver.Names()
	// "*ip:…" = LAN-direct, "*relay:…" = through the hub's relay
	// (ADR-0006's ceiling). The status screen's headline number.
	v.Paths = t.tun.Pool.Paths()
	v.Endpoints = t.agent.Endpoint().Endpoints()
	t.mu.Lock()
	v.Fatal = t.fatalErr
	t.mu.Unlock()
	b, _ := json.Marshal(v)
	return string(b)
}

// NamesJSON is the plane as this member sees it — the last name map,
// one row per name: [{"name","kind","node","online"}], sorted by name.
// Replaces the v2 app's /hosts list. Empty before the first beat.
func (t *Tunnel) NamesJSON() string {
	type row struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Node   string `json:"node"`
		Online bool   `json:"online"`
	}
	var rows []row
	b := t.agent.Bundle()
	if b != nil {
		now := time.Now().Unix()
		byName := map[string]bool{}
		for _, e := range b.NameMap {
			n := e.Member.Cav.Name
			if byName[n] {
				continue
			}
			byName[n] = true
			entries, _ := t.agent.Resolve(n)
			rows = append(rows, row{
				Name:   n,
				Kind:   string(nodeagent.KindOf(n, entries)),
				Node:   e.Member.Aud,
				Online: len(entries) > 0 && e.Location != nil && e.Location.Exp > now,
			})
		}
	}
	rows = append(rows, row{Name: nodeagent.HubName, Kind: "hub", Online: b != nil})
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	out, _ := json.Marshal(rows)
	return string(out)
}

func closeFile(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
