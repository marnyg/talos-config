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
package facethttp

import (
	"context"
	"io"
	"net"
	"net/http"
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
	c := &Conn{ReadWriteCloser: rwc, identity: identity, peer: peer, facet: l.facet}
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
// Deadlines are accepted and ignored: the server's ReadHeaderTimeout
// sets one per request, and a QUIC stream has no socket to arm; the
// facet connection's lifetime bounds the stream's instead.
type Conn struct {
	io.ReadWriteCloser
	identity cert.Identity
	peer     cert.ActorID
	facet    string
}

// Identity is the admitted caller's identity.
func (c *Conn) Identity() cert.Identity { return c.identity }

// Peer is the caller's key (the QUIC peer).
func (c *Conn) Peer() cert.ActorID { return c.peer }

func (c *Conn) LocalAddr() net.Addr              { return Addr(c.facet) }
func (c *Conn) RemoteAddr() net.Addr             { return Addr(string(c.peer)) }
func (c *Conn) SetDeadline(time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(time.Time) error  { return nil }
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

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
