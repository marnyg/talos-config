// Package facethttp serves HTTP over admitted stream-facet streams: the
// receiver terminates the facet in-process (the hub's hub-http, the
// gateway's ingress-http) and every admitted stream is one HTTP
// connection whose request context carries the identity the facet
// attributed to it. The listener is fed, not bound: the accept loop
// that runs cert.Authorize pushes streams in; one http.Server drains.
//
// Nothing here decides anything. Identity is what the caller of Push
// says it is — the one place the decision was made (Authorize over the
// caller's bundle, rooted in the receiver's own consent). Past this
// package the identity is ambient (a context value, a header): where
// capability discipline ends at the actor that terminates the stream
// (invariants, structural trade-offs).
//
// The package is protocol-level — it imports only protocol/cert and
// stdlib — because a stream facet carrying HTTP is protocol vocabulary,
// not a deployment detail: today's consumers are the hub's hub-http and
// the gateway's ingress-http, and the child's event uploader
// (talos-config-e6pu.2) under actors/ cannot import config-server, so
// it lives here (decision talos-config-k5hb).
package facethttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// Listener is a net.Listener fed by admitted streams.
type Listener struct {
	facet  string
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

// NewListener returns a listener whose Addr names facet.
func NewListener(facet string) *Listener {
	return &Listener{facet: facet, conns: make(chan net.Conn), closed: make(chan struct{})}
}

// Push hands one admitted stream to the server as an HTTP connection
// attributed to identity. It reports false when the listener is closed
// or ctx ends first; the caller still owns (and closes) rwc then.
func (l *Listener) Push(ctx context.Context, rwc io.ReadWriteCloser, identity cert.Identity, peer cert.ActorID) bool {
	c := newConn(rwc, identity, peer, l.facet)
	select {
	case l.conns <- c:
		return true
	case <-l.closed:
		return false
	case <-ctx.Done():
		return false
	}
}

// Accept implements net.Listener.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close implements net.Listener; idempotent.
func (l *Listener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

// Addr implements net.Listener: the facet's name, no port.
func (l *Listener) Addr() net.Addr { return Addr(l.facet) }

// Conn adapts one admitted stream to net.Conn for http.Server.
//
// Reads go through a pump: one goroutine reads the stream into a
// bounded buffer and Read serves from it, so a read deadline can
// interrupt Read without touching the stream. http.Server needs that
// for one thing it cannot do otherwise — Hijack. The server keeps a
// background Read in flight on every idle connection (to notice the
// peer leaving), and Hijack aborts it with SetReadDeadline(past) and
// waits for it to return; with deadlines ignored it waited for the
// peer instead, so a 101 Switching Protocols (any WebSocket through
// the gateway) sat unsent until the browser gave up (talos-config-o8wd).
// The pump keeps the underlying Read running across the hijack; bytes
// that land afterwards go to whoever reads next.
//
// A deadline cannot bound the stream itself: a QUIC stream has no
// socket to arm, and irohtransport.Raw.Close cannot interrupt a Read
// blocked in the FFI (iroh-ffi serialises read/stop on one lock). So
// the server's ReadHeaderTimeout returns the Read to the server but
// the pump goroutine stays until the peer goes away or the facet
// connection's lifetime ends it (gateway.DefaultConnMaxAge;
// talos-config-vh6e). Write deadlines are accepted and ignored: a
// write is not interruptible either, and nothing in the server path
// sets one.
type Conn struct {
	rwc      io.ReadWriteCloser
	identity cert.Identity
	peer     cert.ActorID
	facet    string

	mu       sync.Mutex
	cond     *sync.Cond
	buf      []byte      // delivered by the pump, not yet read
	err      error       // the pump's terminal error, after buf drains
	deadline time.Time   // read deadline; zero = none
	timer    *time.Timer // wakes readers at deadline
	closed   bool
	pumping  bool
}

// pumpBuf bounds what the pump reads ahead of the consumer; past it
// the pump waits, and QUIC flow control holds the peer.
const pumpBuf = 256 << 10

func newConn(rwc io.ReadWriteCloser, identity cert.Identity, peer cert.ActorID, facet string) *Conn {
	c := &Conn{rwc: rwc, identity: identity, peer: peer, facet: facet}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Identity is the admitted caller's identity.
func (c *Conn) Identity() cert.Identity { return c.identity }

// Peer is the caller's key (the QUIC peer).
func (c *Conn) Peer() cert.ActorID { return c.peer }

func (c *Conn) LocalAddr() net.Addr  { return Addr(c.facet) }
func (c *Conn) RemoteAddr() net.Addr { return Addr(string(c.peer)) }

// pump runs on its own goroutine, started by the first Read.
func (c *Conn) pump() {
	chunk := make([]byte, 32<<10)
	for {
		n, err := c.rwc.Read(chunk)
		c.mu.Lock()
		if n > 0 {
			c.buf = append(c.buf, chunk[:n]...)
		}
		if err != nil {
			c.err = err
			c.cond.Broadcast()
			c.mu.Unlock()
			return
		}
		c.cond.Broadcast()
		for len(c.buf) >= pumpBuf && !c.closed {
			c.cond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
}

// Read implements net.Conn. It returns buffered bytes first, then the
// pump's error (io.EOF on the peer's FIN), os.ErrDeadlineExceeded past
// the read deadline, net.ErrClosed after Close.
func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pumping && !c.closed {
		c.pumping = true
		go c.pump()
	}
	for {
		if len(c.buf) > 0 {
			n := copy(p, c.buf)
			c.buf = c.buf[n:]
			if len(c.buf) == 0 {
				c.buf = nil // let the pump's backing array go
			}
			c.cond.Broadcast() // the pump may be waiting on pumpBuf
			return n, nil
		}
		if c.closed {
			return 0, net.ErrClosed
		}
		if c.err != nil {
			return 0, c.err
		}
		if !c.deadline.IsZero() && !time.Now().Before(c.deadline) {
			return 0, os.ErrDeadlineExceeded
		}
		c.cond.Wait()
	}
}

// Write implements net.Conn: straight to the stream.
func (c *Conn) Write(p []byte) (int, error) { return c.rwc.Write(p) }

// Close closes the stream and wakes every Read with net.ErrClosed.
// The send side is ended in order first (FIN) when the stream can,
// so a response written just before Close reaches the peer: closing
// an iroh stream with its send side open resets it, and a reset
// discards what is still in flight. The server closes right after
// the handler on a Connection: close request — and used to get away
// with it only because abortPendingRead blocked until the peer had
// read everything.
func (c *Conn) Close() error {
	c.mu.Lock()
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	if cw, ok := c.rwc.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	return c.rwc.Close()
}

// SetDeadline sets the read deadline (write deadlines are ignored).
func (c *Conn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

// SetReadDeadline implements net.Conn: a Read in flight or started
// later returns os.ErrDeadlineExceeded once t has passed; a zero t
// clears it. The stream itself is untouched (see Conn).
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if t.IsZero() {
		return nil
	}
	if d := time.Until(t); d > 0 {
		c.timer = time.AfterFunc(d, func() {
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
		})
		return nil
	}
	c.cond.Broadcast()
	return nil
}

// SetWriteDeadline is accepted and ignored (see Conn).
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

// Dialed adapts one stream the CALLER opened on a stream facet to
// net.Conn for an http.Transport (or a gRPC dialer): the mirror of
// Conn on the accept side. Deadlines are accepted and ignored for
// the same reason (see Conn). local names this side (a facet or
// member name), peer the receiver.
func Dialed(rwc io.ReadWriteCloser, local string, peer cert.ActorID) net.Conn {
	return &dialedConn{ReadWriteCloser: rwc, local: local, peer: peer}
}

type dialedConn struct {
	io.ReadWriteCloser
	local string
	peer  cert.ActorID
}

func (c *dialedConn) LocalAddr() net.Addr              { return Addr(c.local) }
func (c *dialedConn) RemoteAddr() net.Addr             { return Addr(string(c.peer)) }
func (c *dialedConn) SetDeadline(time.Time) error      { return nil }
func (c *dialedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *dialedConn) SetWriteDeadline(time.Time) error { return nil }

// Addr is the net.Addr of a stream endpoint: a name, no port.
type Addr string

func (a Addr) Network() string { return "talos-mesh" }
func (a Addr) String() string  { return string(a) }

// Server returns an http.Server for h over a fresh Listener for facet.
// Each connection's request context carries the identity and peer the
// facet attributed to it (IdentityFrom, PeerFrom).
func Server(facet string, h http.Handler) (*http.Server, *Listener) {
	ln := NewListener(facet)
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if sc, ok := c.(*Conn); ok {
				ctx = context.WithValue(ctx, identityKey{}, sc.identity)
				ctx = context.WithValue(ctx, peerKey{}, sc.peer)
			}
			return ctx
		},
	}
	return srv, ln
}

type identityKey struct{}
type peerKey struct{}

// IdentityFrom returns the caller identity the facet attributed to this
// request's connection, or false off a facet (a plain listener, tests).
func IdentityFrom(ctx context.Context) (cert.Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(cert.Identity)
	return id, ok
}

// PeerFrom returns the caller's key for this request's connection, or
// false off a facet.
func PeerFrom(ctx context.Context) (cert.ActorID, bool) {
	id, ok := ctx.Value(peerKey{}).(cert.ActorID)
	return id, ok
}
