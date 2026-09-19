package irohtransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marnyg/talos-config/iroh-go/iroh"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Stream facets (talos-config-359.8.3.1; domain model "Facet"): the
// compatibility mode that lets an actor stand in front of a service
// that knows nothing of actors. A stream facet is identified by ALPN
// class at connect — coarse, visible in the ClientHello — and THE
// CONNECTION IS THE INVOCATION, checked once:
//
//	dialler                                     acceptor
//	  connect(alpn)  ───────────────────────►  AcceptConn
//	  first bi-stream: bundle (one msg, FIN) ►  Preamble → Authorize
//	  ◄──────────────────  "ok" | "refused: …"  Admit / Refuse
//	  every later bi-stream = one raw forward   Accept → pipe to target
//
// ALPN routes; it never authorizes (mesh-v3 §ALPN ↔ facet rule 2). The
// acceptor runs cert.Authorize over the preamble's bundle with its own
// accept table; this file carries bytes and knows nothing of certs
// beyond the peer's TLS-authenticated id.
//
// The preamble stream reuses the actor framing (one message per
// direction until FIN), so the reply is a real answer the dialler
// waits for before opening forwards — a refused caller learns why and
// never spends a stream.

// PreambleOK is the acceptor's reply on the preamble stream when the
// bundle was authorized.
const PreambleOK = "ok"

const preambleRefused = "refused: "

// ErrRefused: the acceptor answered the preamble with a refusal.
// Reason is the acceptor's text, verbatim.
type ErrRefused struct{ Reason string }

func (e *ErrRefused) Error() string { return "irohtransport: refused: " + e.Reason }

// Conn is one stream-facet connection, either side. On the accept side
// it is delivered by AcceptConn before the preamble is read; on the
// dial side DialConn returns it admitted.
type Conn struct {
	conn   *iroh.Connection
	peer   cert.ActorID
	alpn   string
	maxMsg uint32

	preOnce sync.Once
	pre     *Stream // the preamble stream (first bi-stream)
	preErr  error

	refusing atomic.Bool // Refuse owns the close; Close is a no-op after it
	closed   chan struct{}
	once     sync.Once
}

func newConn(c *iroh.Connection, peer cert.ActorID, alpn string, maxMsg uint32) *Conn {
	return &Conn{conn: c, peer: peer, alpn: alpn, maxMsg: maxMsg, closed: make(chan struct{})}
}

// Peer is the TLS-authenticated actor id at the other end.
func (c *Conn) Peer() cert.ActorID { return c.peer }

// ALPN is the negotiated ALPN class — the acceptor's AcceptTable key.
func (c *Conn) ALPN() string { return c.alpn }

// Alive reports whether the QUIC connection is still open.
func (c *Conn) Alive() bool {
	select {
	case <-c.closed:
		return false
	default:
		return c.conn.CloseReason() == nil
	}
}

// Preamble (accept side) reads the caller's bundle: the first
// bi-stream's whole send side. It must be followed by Admit or Refuse.
func (c *Conn) Preamble(ctx context.Context) ([]byte, error) {
	c.preOnce.Do(func() {
		bi, err := await(ctx, c.closed, c.conn.AcceptBi)
		if err != nil {
			c.preErr = fmt.Errorf("irohtransport: preamble: %w", err)
			return
		}
		c.pre = newStream(bi, c.maxMsg)
	})
	if c.preErr != nil {
		return nil, c.preErr
	}
	msg, err := c.pre.RecvMsg(ctx)
	if err != nil {
		return nil, fmt.Errorf("irohtransport: preamble: %w", err)
	}
	return msg, nil
}

// Admit (accept side) answers the preamble with PreambleOK; forwards
// may follow.
func (c *Conn) Admit(ctx context.Context) error {
	if c.pre == nil {
		return errors.New("irohtransport: Admit before Preamble")
	}
	err := c.pre.SendMsg(ctx, []byte(PreambleOK))
	_ = c.pre.Close()
	return err
}

// refuseGrace bounds how long a refused connection stays open for the
// reply to land; the dialler closes on reading it, this is the backstop.
const refuseGrace = 3 * time.Second

// Refuse (accept side) answers the preamble with the reason and closes
// the connection once the peer has read it (or refuseGrace passes).
// reason is sent verbatim; keep it a category, not a diagnostic (the
// caller is not trusted).
func (c *Conn) Refuse(ctx context.Context, reason string) error {
	if c.pre == nil {
		return errors.New("irohtransport: Refuse before Preamble")
	}
	if !c.refusing.CompareAndSwap(false, true) {
		return errors.New("irohtransport: Refuse twice")
	}
	err := c.pre.SendMsg(ctx, []byte(preambleRefused+reason))
	_ = c.pre.Close()
	go func() {
		done := make(chan struct{})
		go func() { c.conn.Closed(); close(done) }() // peer closed
		select {
		case <-done:
		case <-time.After(refuseGrace):
		}
		c.close(1, reason)
	}()
	return err
}

// Accept (accept side) blocks for the next forward stream the peer
// opens. It returns an error once the connection is gone.
func (c *Conn) Accept(ctx context.Context) (*Raw, error) {
	bi, err := await(ctx, c.closed, c.conn.AcceptBi)
	if err != nil {
		return nil, fmt.Errorf("irohtransport: accept: %w", err)
	}
	return newRaw(bi), nil
}

// Open (dial side) opens one forward stream on an admitted connection.
func (c *Conn) Open(ctx context.Context) (*Raw, error) {
	bi, err := await(ctx, c.closed, c.conn.OpenBi)
	if err != nil {
		return nil, fmt.Errorf("irohtransport: open: %w", err)
	}
	return newRaw(bi), nil
}

// Close closes the connection; open forwards fail. After Refuse it is
// a no-op: the refusal closes once the peer has read it.
func (c *Conn) Close() error {
	if c.refusing.Load() {
		return nil
	}
	c.close(0, "")
	return nil
}

func (c *Conn) close(code int64, reason string) {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close(code, []byte(reason))
		c.conn.Destroy()
	})
}

// AcceptConn blocks for the next inbound connection on one of
// Options.StreamALPNs. The preamble has not been read yet.
func (e *Endpoint) AcceptConn(ctx context.Context) (*Conn, error) {
	select {
	case c := <-e.acceptConn:
		return c, nil
	case <-e.closed:
		return nil, actor.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DialConn connects to actor id under alpn (hints as for Dial), sends
// preamble on the first bi-stream and waits for the acceptor's answer.
// It returns an admitted Conn, *ErrRefused, or a transport error. The
// caller owns the Conn; nothing is pooled.
func (e *Endpoint) DialConn(ctx context.Context, id cert.ActorID, hints []string, alpn string, preamble []byte) (*Conn, error) {
	select {
	case <-e.closed:
		return nil, actor.ErrClosed
	default:
	}
	if alpn == ALPN {
		return nil, fmt.Errorf("irohtransport: %q is the actor ALPN, not a stream facet", alpn)
	}
	eid, err := EndpointIDOf(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", actor.ErrUnreachable, err)
	}
	defer eid.Destroy()
	addr, err := e.resolve(id, eid, hints)
	if err != nil {
		return nil, err
	}
	defer addr.Destroy()
	conn, err := await(ctx, e.closed, func() (*iroh.Connection, error) {
		return e.ep.Connect(addr, []byte(alpn))
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, actor.ErrClosed) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s: %w", actor.ErrUnreachable, id, err)
	}
	if got := conn.RemoteId(); !got.Eq(eid) {
		peer, _ := ActorIDOf(got)
		conn.Destroy()
		return nil, fmt.Errorf("%w: got %s, wanted %s", actor.ErrPeerMismatch, peer, id)
	}
	c := newConn(conn, id, alpn, e.maxMsg)
	bi, err := await(ctx, c.closed, conn.OpenBi)
	if err != nil {
		c.close(0, "")
		return nil, fmt.Errorf("irohtransport: preamble open_bi: %w", err)
	}
	c.pre = newStream(bi, e.maxMsg)
	c.preOnce.Do(func() {})
	if err := c.pre.SendMsg(ctx, preamble); err != nil {
		c.close(0, "")
		return nil, err
	}
	reply, err := c.pre.RecvMsg(ctx)
	_ = c.pre.Close()
	if err != nil {
		c.close(0, "")
		if errors.Is(err, io.EOF) {
			return nil, &ErrRefused{Reason: "no answer"}
		}
		return nil, fmt.Errorf("irohtransport: preamble reply: %w", err)
	}
	switch s := string(reply); {
	case s == PreambleOK:
		return c, nil
	case strings.HasPrefix(s, preambleRefused):
		c.close(0, "")
		return nil, &ErrRefused{Reason: strings.TrimPrefix(s, preambleRefused)}
	default:
		c.close(0, "")
		return nil, fmt.Errorf("irohtransport: preamble reply %q", s)
	}
}

// Raw is one forward stream: a byte pipe with independent half-closes,
// the shape a TCP splice wants (net.TCPConn has the same CloseWrite).
// Reads block on the FFI side; Close from another goroutine unblocks
// them.
type Raw struct {
	bi   *iroh.BiStream
	send *iroh.SendStream
	recv *iroh.RecvStream

	mu       sync.Mutex
	finished bool // send side FINed: Close must not reset it (the FIN may be in flight)
	eof      bool // recv side saw FIN
	once     sync.Once
}

func newRaw(bi *iroh.BiStream) *Raw {
	return &Raw{bi: bi, send: bi.Send(), recv: bi.Recv()}
}

// Read returns the next chunk, at most len(p) bytes; io.EOF on the
// peer's FIN.
func (r *Raw) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	chunk, err := r.recv.Read(uint32(len(p)))
	if err != nil {
		return 0, fmt.Errorf("irohtransport: read: %w", err)
	}
	if len(chunk) == 0 {
		r.mu.Lock()
		r.eof = true
		r.mu.Unlock()
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}

// Write sends all of p.
func (r *Raw) Write(p []byte) (int, error) {
	if err := r.send.WriteAll(p); err != nil {
		return 0, fmt.Errorf("irohtransport: write: %w", err)
	}
	return len(p), nil
}

// CloseWrite FINs the send side; the peer's Read observes io.EOF.
func (r *Raw) CloseWrite() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return nil
	}
	r.finished = true
	return r.send.Finish()
}

// Close releases the stream. Directions not ended in order (CloseWrite
// / a read that reached io.EOF) are aborted: the peer sees a reset.
func (r *Raw) Close() error {
	r.once.Do(func() {
		r.mu.Lock()
		finished, eof := r.finished, r.eof
		r.finished = true
		r.mu.Unlock()
		if !eof {
			_ = r.recv.Stop(0)
		}
		if !finished {
			_ = r.send.Reset(0)
		}
		r.send.Destroy()
		r.recv.Destroy()
		r.bi.Destroy()
	})
	return nil
}
