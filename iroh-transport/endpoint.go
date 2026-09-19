package irohtransport

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/marnyg/talos-config/iroh-go/iroh"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

const (
	// ALPN is the one fixed ALPN every actor-facet stream uses. Facets
	// live inside the encrypted envelope (ADR-0001), not in the
	// ClientHello, so there is exactly one class.
	ALPN = "sovereign-actor/v1"

	// TagRelay prefixes a relay URL endpoint tag: "iroh:relay=https://…".
	TagRelay = "iroh:relay="
	// TagUDP prefixes a direct socket endpoint tag: "iroh:udp=ip:port".
	TagUDP = "iroh:udp="
)

// Options configures Bind. The zero value is PresetMinimal, no relay,
// all interfaces on an ephemeral port.
type Options struct {
	// BindAddr is the UDP socket to bind; "" ⇒ "0.0.0.0:0".
	BindAddr string
	// Relay is the home relay URL to use (RelayMode::Custom); "" ⇒
	// RelayMode::Disabled. PresetMinimal never adds n0's relays.
	Relay string
	// AdvertiseRelay, when set, is the relay URL Endpoints() reports
	// instead of Relay: the address PEERS reach the same relay server
	// at. The hub homes on its relay child over loopback but is dialled
	// through the public hostname fly terminates TLS on (ADR-0022); the
	// relay forwards by EndpointId, so the two names meet at one server.
	// "" ⇒ report Relay (or what iroh learned).
	AdvertiseRelay string
	// MaxMsg bounds one message; 0 ⇒ DefaultMaxMsg.
	MaxMsg uint32
	// StreamALPNs are the stream-facet ALPN classes this endpoint also
	// advertises (streamfacet.go). Connections under them are delivered
	// whole to AcceptConn, never to Accept. ALPN itself is not allowed.
	StreamALPNs []string
	// OnConnLost, when set, is called once each time a pooled dial-side
	// connection to peer ends for any reason but this endpoint's own
	// Close: the peer closed it, or it idled out — iroh keep-alives every
	// connection (5 s), so a pooled connection outlives its streams and
	// its loss is evidence that the peer is gone (~30 s after a peer
	// process dies). Called from the watcher goroutine; must not block.
	OnConnLost func(peer cert.ActorID)
}

// Endpoint is an iroh Endpoint bound to one actor identity. It
// implements actor.Endpoint.
type Endpoint struct {
	ep        *iroh.Endpoint
	id        cert.ActorID
	relay     string
	advertise string
	maxMsg    uint32
	streams   map[string]bool // StreamALPNs
	onLost    func(cert.ActorID)

	accept     chan accepted
	acceptConn chan *Conn
	closed     chan struct{}
	once       sync.Once
	loops      sync.WaitGroup

	mu    sync.Mutex
	conns map[cert.ActorID]*iroh.Connection // dial-side pool, one per peer
}

var _ actor.Endpoint = (*Endpoint)(nil)

type accepted struct {
	s    *Stream
	peer cert.ActorID
}

// Bind creates an iroh endpoint whose node key is priv, so its iroh id
// and its actor id are the same Ed25519 key.
func Bind(priv ed25519.PrivateKey, o Options) (*Endpoint, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("irohtransport: private key is %d bytes, want %d", len(priv), ed25519.PrivateKeySize)
	}
	id, err := ActorIDFromPublicKey(priv.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, err
	}
	bindAddr := o.BindAddr
	if bindAddr == "" {
		bindAddr = "0.0.0.0:0"
	}
	maxMsg := o.MaxMsg
	if maxMsg == 0 {
		maxMsg = DefaultMaxMsg
	}
	var mode *iroh.RelayMode
	if o.Relay == "" {
		mode = iroh.RelayModeDisabled()
	} else {
		mode, err = iroh.RelayModeCustomFromUrls([]string{o.Relay})
		if err != nil {
			return nil, fmt.Errorf("irohtransport: relay mode: %w", err)
		}
	}
	preset := iroh.PresetMinimal() // crypto provider only: no n0 DNS, pkarr, or relays
	seed := priv.Seed()            // iroh's SecretKey is the 32-byte Ed25519 seed
	alpns := [][]byte{[]byte(ALPN)}
	streams := make(map[string]bool, len(o.StreamALPNs))
	for _, a := range o.StreamALPNs {
		if a == ALPN || a == "" || streams[a] {
			return nil, fmt.Errorf("irohtransport: stream ALPN %q is empty, duplicate, or the actor ALPN", a)
		}
		streams[a] = true
		alpns = append(alpns, []byte(a))
	}
	ep, err := iroh.EndpointBind(iroh.EndpointOptions{
		Preset:    &preset,
		BindAddr:  &bindAddr,
		SecretKey: &seed,
		Alpns:     &alpns,
		RelayMode: &mode,
	})
	if err != nil {
		return nil, fmt.Errorf("irohtransport: bind: %w", err)
	}
	// The whole identity story rests on this equality; check it once.
	if got, err := ActorIDOf(ep.Id()); err != nil || got != id {
		_ = ep.Close()
		return nil, fmt.Errorf("irohtransport: iroh id %s != actor id %s (%v)", got, id, err)
	}
	e := &Endpoint{
		ep:         ep,
		id:         id,
		relay:      o.Relay,
		advertise:  o.AdvertiseRelay,
		maxMsg:     maxMsg,
		streams:    streams,
		onLost:     o.OnConnLost,
		accept:     make(chan accepted),
		acceptConn: make(chan *Conn),
		closed:     make(chan struct{}),
		conns:      make(map[cert.ActorID]*iroh.Connection),
	}
	e.loops.Add(1)
	go e.acceptLoop()
	return e, nil
}

// ID is the actor id this endpoint authenticates as.
func (e *Endpoint) ID() cert.ActorID { return e.id }

// Raw exposes the underlying iroh endpoint (diagnostics, Online, …).
func (e *Endpoint) Raw() *iroh.Endpoint { return e.ep }

// Online blocks until the endpoint has a home relay (relay path only)
// or ctx ends. With RelayMode::Disabled it returns once the socket is
// bound, i.e. immediately.
func (e *Endpoint) Online(ctx context.Context) error {
	_, err := await(ctx, e.closed, func() (struct{}, error) {
		e.ep.Online()
		return struct{}{}, nil
	})
	return err
}

// Endpoints returns the transport-tagged strings peers can Dial with:
// "iroh:relay=<url>" for the home relay (if any; AdvertiseRelay when
// set) and "iroh:udp=<ip:port>" for every dialable bound or discovered
// socket.
func (e *Endpoint) Endpoints() []string {
	var out []string
	addr := e.ep.Addr()
	defer addr.Destroy()
	switch r := addr.RelayUrl(); {
	case e.advertise != "" && (e.relay != "" || (r != nil && *r != "")):
		out = append(out, TagRelay+e.advertise)
	case r != nil && *r != "":
		out = append(out, TagRelay+*r)
	case e.relay != "":
		out = append(out, TagRelay+e.relay)
	}
	seen := map[string]bool{}
	for _, s := range append(e.ep.BoundSockets(), addr.DirectAddresses()...) {
		if seen[s] || !dialable(s) {
			continue
		}
		seen[s] = true
		out = append(out, TagUDP+s)
	}
	return out
}

// dialable rejects unspecified ("0.0.0.0:p", "[::]:p") sockets.
func dialable(s string) bool {
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && !ip.IsUnspecified()
}

// Dial opens a stream to actor id. iroh tags among the hints become the
// EndpointAddr; other tags are ignored. Streams to one peer share a
// pooled QUIC connection, evicted as soon as it closes (watchConn); a
// pooled connection found dead on open is replaced once.
func (e *Endpoint) Dial(ctx context.Context, id cert.ActorID, hints []string) (actor.Stream, error) {
	select {
	case <-e.closed:
		return nil, actor.ErrClosed
	default:
	}
	eid, err := EndpointIDOf(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", actor.ErrUnreachable, err)
	}
	defer eid.Destroy()

	if conn := e.pooled(id); conn != nil {
		if bi, err := await(ctx, e.closed, conn.OpenBi); err == nil {
			return newStream(bi, e.maxMsg), nil
		} else if ctx.Err() != nil || errors.Is(err, actor.ErrClosed) {
			return nil, err
		}
		e.evict(id, conn) // stale (idle-timed-out or peer gone): redial
	}

	addr, err := e.resolve(id, eid, hints)
	if err != nil {
		return nil, err
	}
	defer addr.Destroy()
	conn, err := await(ctx, e.closed, func() (*iroh.Connection, error) {
		return e.ep.Connect(addr, []byte(ALPN))
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, actor.ErrClosed) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s: %w", actor.ErrUnreachable, id, err)
	}
	// iroh's TLS already pinned the remote to eid; this is belt and braces.
	if got := conn.RemoteId(); !got.Eq(eid) {
		peer, _ := ActorIDOf(got)
		conn.Destroy()
		return nil, fmt.Errorf("%w: got %s, wanted %s", actor.ErrPeerMismatch, peer, id)
	}
	e.pool(id, conn)
	bi, err := await(ctx, e.closed, conn.OpenBi)
	if err != nil {
		return nil, fmt.Errorf("irohtransport: open_bi to %s: %w", id, err)
	}
	return newStream(bi, e.maxMsg), nil
}

// resolve turns hints into an EndpointAddr for eid, or falls back to
// what iroh already knows about the peer.
func (e *Endpoint) resolve(id cert.ActorID, eid *iroh.EndpointId, hints []string) (*iroh.EndpointAddr, error) {
	var relay *string
	var addrs []string
	for _, h := range hints {
		if v, ok := strings.CutPrefix(h, TagRelay); ok {
			if relay == nil && v != "" {
				relay = &v
			}
		} else if v, ok := strings.CutPrefix(h, TagUDP); ok {
			if dialable(v) {
				addrs = append(addrs, v)
			}
		}
		// other tags belong to other transports
	}
	if relay != nil || len(addrs) > 0 {
		return iroh.NewEndpointAddr(eid, relay, addrs), nil
	}
	if known := e.ep.RemoteAddr(eid); known != nil && *known != nil {
		return *known, nil
	}
	return nil, fmt.Errorf("%w: %s (no iroh: hint)", actor.ErrUnreachable, id)
}

func (e *Endpoint) pooled(id cert.ActorID) *iroh.Connection {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conns[id]
}

func (e *Endpoint) pool(id cert.ActorID, c *iroh.Connection) {
	e.mu.Lock()
	// A concurrent Dial may have raced us; the loser's connection stays
	// alive while its streams do and is dropped by the finalizer.
	e.conns[id] = c
	e.mu.Unlock()
	e.loops.Add(1)
	go e.watchConn(id, c)
}

// watchConn evicts c from the pool the moment it closes — peer's close,
// idle timeout after the peer vanished, or our own Close — and, unless
// the endpoint itself is closing, reports the loss to OnConnLost. The
// next Dial to id then redials instead of finding the corpse at
// OpenBi; the caller learns a peer is gone without dialing at all.
func (e *Endpoint) watchConn(id cert.ActorID, c *iroh.Connection) {
	defer e.loops.Done()
	c.Closed()
	e.evict(id, c)
	select {
	case <-e.closed:
		return
	default:
	}
	if e.onLost != nil {
		e.onLost(id)
	}
}

func (e *Endpoint) evict(id cert.ActorID, c *iroh.Connection) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conns[id] == c {
		delete(e.conns, id)
	}
}

// Accept blocks for the next inbound stream and the TLS-authenticated
// actor id of the peer that opened it.
func (e *Endpoint) Accept(ctx context.Context) (actor.Stream, cert.ActorID, error) {
	select {
	case a := <-e.accept:
		return a.s, a.peer, nil
	case <-e.closed:
		return nil, "", actor.ErrClosed
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

// acceptLoop pulls incoming connections until the endpoint closes and
// runs one stream loop per connection.
func (e *Endpoint) acceptLoop() {
	defer e.loops.Done()
	for {
		inc := e.ep.AcceptNext()
		if inc == nil || *inc == nil {
			return // endpoint closed
		}
		e.loops.Add(1)
		go e.serveConn(*inc)
	}
}

// serveConn finishes the handshake for one incoming connection and
// feeds its bidirectional streams to Accept until it ends — or, on a
// stream-facet ALPN, hands the whole connection to AcceptConn.
func (e *Endpoint) serveConn(inc *iroh.Incoming) {
	defer e.loops.Done()
	defer inc.Destroy()
	accepting, err := inc.Accept()
	if err != nil {
		return
	}
	defer accepting.Destroy()
	alpnBytes, err := accepting.Alpn()
	if err != nil {
		return
	}
	alpn := string(alpnBytes)
	if alpn != ALPN && !e.streams[alpn] {
		return // not ours; dropping the Accepting aborts the handshake
	}
	conn, err := accepting.Connect()
	if err != nil {
		return
	}
	peer, err := ActorIDOf(conn.RemoteId())
	if err != nil {
		conn.Destroy()
		return
	}
	if alpn != ALPN {
		c := newConn(conn, peer, alpn, e.maxMsg)
		select {
		case e.acceptConn <- c: // the consumer owns it from here
		case <-e.closed:
			c.close(0, "")
		}
		return
	}
	defer conn.Destroy()
	for {
		bi, err := conn.AcceptBi()
		if err != nil {
			return // connection closed (peer, idle timeout, or our Close)
		}
		s := newStream(bi, e.maxMsg)
		select {
		case e.accept <- accepted{s: s, peer: peer}:
		case <-e.closed:
			_ = s.Close()
			return
		}
	}
}

// Close shuts the iroh endpoint down: pending Accepts return
// actor.ErrClosed, open connections are closed, in-flight streams fail.
func (e *Endpoint) Close() error {
	var err error
	e.once.Do(func() {
		close(e.closed)
		err = e.ep.Close()
		e.loops.Wait()
		e.mu.Lock()
		for id, c := range e.conns {
			c.Destroy()
			delete(e.conns, id)
		}
		e.mu.Unlock()
		e.ep.Destroy()
	})
	return err
}
