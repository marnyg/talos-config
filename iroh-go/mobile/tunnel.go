// Package p0mobile is the Go core of the Mesh v3 P0.2 Android spike
// (docs/mesh-v3-p0.2-android.md). gomobile binds it into an AAR.
//
// Shape (the plan's Android/TV design in miniature):
//
//	VpnService tun fd → gvisor netstack → { UDP/53 to the fake resolver: fake-IP DNS;
//	                                        TCP to a fake IP: one iroh stream per flow }
//
// The whole fake-IP range and the resolver are the only VPN routes (Kotlin
// sets them), so the phone's other traffic never enters here and neither the
// iroh UDP socket nor the DNS forwarder can loop back into the tun. Only one
// peer exists in the spike: every *.mesh.internal name maps to it, and
// p0agent serve on the other end forwards ALPN mesh/http/v1 to Jellyfin.
//
// Not here (Phase 1): certs / authorize(), the git-derived name→NodeId map,
// more than one peer, IPv6.
package p0mobile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marnyg/talos-config/iroh-go/iroh"
)

// ALPN the spike dials under; p0agent serve maps it to Jellyfin's NodePort.
const ALPN = "mesh/http/v1"

// SocketProtector is implemented in Kotlin with VpnService.protect (same
// contract as the shipped app's mobile.SocketProtector). With split
// routing it is belt-and-braces for the DNS forwarder; kept so the spike
// also answers "does it still work if the routes are wider".
type SocketProtector interface {
	Protect(fd int32) bool
}

// Tunnel is one running instance. gomobile binds it as a Java object.
type Tunnel struct {
	ep    *iroh.Endpoint
	peer  *iroh.EndpointId
	relay string
	ns    *netstack
	dns   *fakeDNS

	mu   sync.Mutex
	conn *iroh.Connection

	started time.Time
	stats   stats
	closed  atomic.Bool
}

type stats struct {
	Flows       atomic.Int64 `json:"flows"`
	FlowsOpen   atomic.Int64 `json:"flowsOpen"`
	FlowErrors  atomic.Int64 `json:"flowErrors"`
	BytesIn     atomic.Int64 `json:"bytesIn"`  // peer → app
	BytesOut    atomic.Int64 `json:"bytesOut"` // app → peer
	Redials     atomic.Int64 `json:"redials"`
	DNSMesh     atomic.Int64 `json:"dnsMesh"`
	DNSUnderlay atomic.Int64 `json:"dnsUnderlay"`
	DNSFail     atomic.Int64 `json:"dnsFail"`
}

// Start binds an iroh endpoint (minimal preset, the given relay pinned,
// no n0 discovery), dials the peer once so the first stream is fast, and
// starts the netstack on tunFd. Kotlin owns the fd's lifecycle and must
// have established the tun with: address ResolverIP/32's network (any
// 198.18.x host), route FakeRange, dnsServer ResolverIP, MTU = mtu.
// upstreamDNS is "ip:port[,ip:port]" for non-mesh queries (the underlying
// network's resolvers). localAddrs is "ip[,ip]": the underlying network's
// IPv4 addresses from LinkProperties. iroh's own interface enumeration
// yields nothing inside an Android app (netlink RTM_GETLINK is restricted
// from API 30), so without these the endpoint advertises no direct
// addresses and every connection stays on the relay (P0.2 finding
// 2026-09-16: peer-direct=[] on the node side). Same fix Tailscale-Android
// uses: Java hands the interface addresses down. protector may be nil.
func Start(tunFd int, mtu int, relay, peerHex, upstreamDNS, localAddrs string, protector SocketProtector) (*Tunnel, error) {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if lvl := os.Getenv("P0_LOG"); lvl != "" {
		// A miss is LogLevel(0), outside the enum (Trace = 1), handed to the FFI unchecked.
		if l, ok := map[string]iroh.LogLevel{"trace": iroh.LogLevelTrace, "debug": iroh.LogLevelDebug, "info": iroh.LogLevelInfo, "warn": iroh.LogLevelWarn}[lvl]; ok {
			iroh.SetLogLevel(l)
		}
	}
	peer, err := iroh.EndpointIdFromString(peerHex)
	if err != nil {
		return nil, fmt.Errorf("peer id: %w", err)
	}
	mode, err := iroh.RelayModeCustomFromUrls([]string{relay})
	if err != nil {
		return nil, fmt.Errorf("relay mode: %w", err)
	}
	preset := iroh.PresetMinimal()
	ep, err := iroh.EndpointBind(iroh.EndpointOptions{Preset: &preset, RelayMode: &mode})
	if err != nil {
		return nil, fmt.Errorf("bind: %w", err)
	}
	advertiseLocal(ep, localAddrs)
	t0 := time.Now()
	ep.Online()
	log.Printf("id=%s online after %s (relay %s) direct=%v", ep.Id().String(), time.Since(t0).Round(time.Millisecond), relay, ep.Addr().DirectAddresses())

	t := &Tunnel{ep: ep, peer: peer, relay: relay, started: time.Now()}
	if _, err := t.getConn(); err != nil {
		ep.Destroy()
		return nil, fmt.Errorf("connect %s: %w", peerHex, err)
	}
	t.dns, err = newFakeDNS(upstreamDNS, protector, &t.stats)
	if err != nil {
		ep.Destroy()
		return nil, err
	}
	t.ns, err = newNetstack(tunFd, uint32(mtu), t.handleTCP, t.dns.handleUDP)
	if err != nil {
		ep.Destroy()
		return nil, fmt.Errorf("netstack: %w", err)
	}
	log.Printf("tunnel up: fd=%d mtu=%d peer=%s", tunFd, mtu, short(peer))
	return t, nil
}

// advertiseLocal adds ip:<bound UDP port> for each IPv4 in localAddrs as
// an external address of ep, so the peer learns where to punch on the LAN.
func advertiseLocal(ep *iroh.Endpoint, localAddrs string) {
	port := ""
	for _, s := range ep.BoundSockets() {
		if host, p, err := net.SplitHostPort(s); err == nil && net.ParseIP(host).To4() != nil {
			port = p
			break
		}
	}
	log.Printf("bound sockets %v, iroh-discovered direct addrs %v", ep.BoundSockets(), ep.Addr().DirectAddresses())
	if port == "" {
		log.Printf("no IPv4 socket bound; not advertising local addrs")
		return
	}
	for _, ip := range strings.Split(localAddrs, ",") {
		ip = strings.TrimSpace(ip)
		if ip == "" || net.ParseIP(ip) == nil || net.ParseIP(ip).To4() == nil {
			continue
		}
		addr := net.JoinHostPort(ip, port)
		if err := ep.AddExternalAddr(addr); err != nil {
			log.Printf("add external addr %s: %v", addr, err)
			continue
		}
		log.Printf("advertising %s", addr)
	}
}

// Stop tears the tunnel down. Idempotent. Kotlin calls it from
// onDestroy / onRevoke before closing the fd.
func (t *Tunnel) Stop() {
	if !t.closed.CompareAndSwap(false, true) {
		return
	}
	t.ns.close()
	t.dns.close()
	t.mu.Lock()
	if t.conn != nil {
		_ = t.conn.Close(0, []byte("bye"))
		t.conn.Destroy()
		t.conn = nil
	}
	t.mu.Unlock()
	_ = t.ep.Close()
	t.ep.Destroy()
	log.Printf("tunnel stopped")
}

// Id is this device's NodeId (hex).
func (t *Tunnel) Id() string { return t.ep.Id().String() }

// StatsJSON is a snapshot for the activity: counters, uptime, the current
// connection's paths (the "*ip:" vs "*relay:" evidence the plan asks for).
func (t *Tunnel) StatsJSON() string {
	type view struct {
		Id          string   `json:"id"`
		Peer        string   `json:"peer"`
		UptimeS     int64    `json:"uptimeS"`
		Paths       []string `json:"paths"`
		SelfDirect  []string `json:"selfDirect"` // what we advertise; empty ⇒ relay forever
		Connected   bool     `json:"connected"`
		Flows       int64    `json:"flows"`
		FlowsOpen   int64    `json:"flowsOpen"`
		FlowErrors  int64    `json:"flowErrors"`
		BytesIn     int64    `json:"bytesIn"`
		BytesOut    int64    `json:"bytesOut"`
		Redials     int64    `json:"redials"`
		DNSMesh     int64    `json:"dnsMesh"`
		DNSUnderlay int64    `json:"dnsUnderlay"`
		DNSFail     int64    `json:"dnsFail"`
		Names       []string `json:"names"`
	}
	v := view{
		Id: t.Id(), Peer: t.peer.String(), UptimeS: int64(time.Since(t.started).Seconds()),
		Flows: t.stats.Flows.Load(), FlowsOpen: t.stats.FlowsOpen.Load(), FlowErrors: t.stats.FlowErrors.Load(),
		BytesIn: t.stats.BytesIn.Load(), BytesOut: t.stats.BytesOut.Load(), Redials: t.stats.Redials.Load(),
		DNSMesh: t.stats.DNSMesh.Load(), DNSUnderlay: t.stats.DNSUnderlay.Load(), DNSFail: t.stats.DNSFail.Load(),
		Names: t.dns.names(), SelfDirect: t.ep.Addr().DirectAddresses(),
	}
	t.mu.Lock()
	if t.conn != nil && t.conn.CloseReason() == nil {
		v.Connected = true
		v.Paths = paths(t.conn)
	}
	t.mu.Unlock()
	b, _ := json.Marshal(v)
	return string(b)
}

// getConn returns the live connection to the peer, redialing when the
// previous one is gone (irohup's bridge pattern). Id + relay URL only:
// no direct addresses, so the first packets traverse the relay and any
// direct path afterwards is hole-punching.
func (t *Tunnel) getConn() (*iroh.Connection, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn != nil && t.conn.CloseReason() == nil {
		return t.conn, nil
	}
	if t.conn != nil {
		log.Printf("connection gone: %s — redialing", *t.conn.CloseReason())
		t.conn.Destroy()
		t.conn = nil
		t.stats.Redials.Add(1)
	}
	t0 := time.Now()
	c, err := t.ep.Connect(iroh.NewEndpointAddr(t.peer, &t.relay, nil), []byte(ALPN))
	if err != nil {
		return nil, err
	}
	log.Printf("connected to %s in %s paths=%v", short(t.peer), time.Since(t0).Round(time.Millisecond), paths(c))
	t.conn = c
	return c, nil
}

// dropConn forgets c if it is still the current connection.
func (t *Tunnel) dropConn(c *iroh.Connection) {
	t.mu.Lock()
	if t.conn == c {
		t.conn.Destroy()
		t.conn = nil
	}
	t.mu.Unlock()
}

// handleTCP runs one app flow (already accepted by the netstack) over one
// iroh stream. dst is the fake IP:port the app dialed; the spike ignores
// it beyond logging — p0agent's forward table decides the real target.
func (t *Tunnel) handleTCP(app tcpConn, dst netip.AddrPort) {
	t.stats.Flows.Add(1)
	t.stats.FlowsOpen.Add(1)
	defer t.stats.FlowsOpen.Add(-1)
	defer app.Close()

	c, err := t.getConn()
	if err != nil {
		log.Printf("flow %s: connect: %v", dst, err)
		t.stats.FlowErrors.Add(1)
		return
	}
	bi, err := c.OpenBi()
	if err != nil {
		// Stale connection (peer restarted, idle timeout): drop it so the
		// next flow redials, and give this one a second try.
		log.Printf("flow %s: open_bi: %v — retrying once", dst, err)
		t.dropConn(c)
		if c, err = t.getConn(); err == nil {
			bi, err = c.OpenBi()
		}
		if err != nil {
			log.Printf("flow %s: open_bi: %v", dst, err)
			t.stats.FlowErrors.Add(1)
			return
		}
	}
	defer bi.Destroy()
	t0 := time.Now()
	in, out := pipe(bi, app, &t.stats)
	log.Printf("flow %s done: %dB in, %dB out, %s, paths=%v", dst, in, out, time.Since(t0).Round(time.Millisecond), paths(c))
}

// tcpConn is what the netstack hands us for an accepted flow
// (*gonet.TCPConn satisfies it).
type tcpConn interface {
	net.Conn
	CloseWrite() error
}

// pipe copies app→send and recv→app until both directions are done.
// Half-closes propagate: TCP FIN → QUIC FIN, QUIC FIN → TCP CloseWrite.
// Same as p0agent's pipe; the 64 KiB chunks are what kept apid streams
// at line rate there.
func pipe(bi *iroh.BiStream, app tcpConn, st *stats) (fromStream, toStream int64) {
	send, recv := bi.Send(), bi.Recv()
	defer send.Destroy()
	defer recv.Destroy()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // stream -> app
		defer wg.Done()
		for {
			chunk, err := recv.Read(64 << 10)
			if err != nil {
				_ = app.Close()
				return
			}
			if len(chunk) == 0 { // FIN
				_ = app.CloseWrite()
				return
			}
			n, err := app.Write(chunk)
			fromStream += int64(n)
			st.BytesIn.Add(int64(n))
			if err != nil {
				_ = recv.Stop(0)
				return
			}
		}
	}()
	go func() { // app -> stream
		defer wg.Done()
		buf := make([]byte, 64<<10)
		for {
			n, err := app.Read(buf)
			if n > 0 {
				if werr := send.WriteAll(buf[:n]); werr != nil {
					_ = app.Close()
					return
				}
				toStream += int64(n)
				st.BytesOut.Add(int64(n))
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					_ = send.Finish()
				} else {
					_ = send.Reset(0)
				}
				return
			}
		}
	}()
	wg.Wait()
	return
}

func paths(conn *iroh.Connection) []string {
	var parts []string
	for _, p := range conn.Paths() {
		kind := "other"
		switch {
		case p.IsRelay:
			kind = "relay"
		case p.IsIp:
			kind = "ip"
		}
		sel := ""
		if p.IsSelected {
			sel = "*"
		}
		parts = append(parts, fmt.Sprintf("%s%s:%s", sel, kind, p.RemoteAddr))
	}
	return parts
}

func short(id *iroh.EndpointId) string {
	s := id.String()
	if len(s) > 10 {
		s = s[:10]
	}
	return s
}
