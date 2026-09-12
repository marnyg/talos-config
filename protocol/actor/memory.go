package actor

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/marnyg/talos-config/protocol/cert"
)

// MemTag is the endpoint tag prefix of the in-memory transport:
// "mem:<name>".
const MemTag = "mem:"

// MemoryNetwork is one in-process "wire": a registry of named endpoints
// that can dial each other with goroutines and channels. It is the
// reference Transport for tests and for two actors in one process; no
// socket is involved.
type MemoryNetwork struct {
	mu     sync.Mutex
	byName map[string]*MemoryEndpoint
	byID   map[cert.ActorID]*MemoryEndpoint
}

// NewMemoryNetwork returns an empty network.
func NewMemoryNetwork() *MemoryNetwork {
	return &MemoryNetwork{
		byName: make(map[string]*MemoryEndpoint),
		byID:   make(map[cert.ActorID]*MemoryEndpoint),
	}
}

// Bind creates an endpoint for actor id reachable as "mem:<name>". A
// name or id already bound is an error.
func (n *MemoryNetwork) Bind(id cert.ActorID, name string) (*MemoryEndpoint, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if name == "" || strings.ContainsAny(name, " :") {
		return nil, fmt.Errorf("actor: bad memory endpoint name %q", name)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, dup := n.byName[name]; dup {
		return nil, fmt.Errorf("actor: memory endpoint %q already bound", name)
	}
	if _, dup := n.byID[id]; dup {
		return nil, fmt.Errorf("actor: actor %s already bound on this network", id)
	}
	e := &MemoryEndpoint{
		net:    n,
		id:     id,
		name:   name,
		accept: make(chan pendingStream),
		closed: make(chan struct{}),
	}
	n.byName[name] = e
	n.byID[id] = e
	return e, nil
}

func (n *MemoryNetwork) lookup(id cert.ActorID, hints []string) (*MemoryEndpoint, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, h := range hints {
		name, ok := strings.CutPrefix(h, MemTag)
		if !ok {
			continue // another transport's tag
		}
		e, ok := n.byName[name]
		if !ok {
			continue // stale hint
		}
		if e.id != id {
			// Mirrors an authenticated transport: the endpoint a hint
			// names must prove it is the actor we meant to reach.
			return nil, fmt.Errorf("%w: %s is %s, wanted %s", ErrPeerMismatch, h, e.id, id)
		}
		return e, nil
	}
	if e, ok := n.byID[id]; ok {
		return e, nil // transport-native discovery by id
	}
	return nil, fmt.Errorf("%w: %s", ErrUnreachable, id)
}

func (n *MemoryNetwork) unbind(e *MemoryEndpoint) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.byName[e.name] == e {
		delete(n.byName, e.name)
	}
	if n.byID[e.id] == e {
		delete(n.byID, e.id)
	}
}

// pendingStream is a dialled stream waiting in the callee's accept
// queue, with the dialler's identity.
type pendingStream struct {
	s    *MemoryStream
	peer cert.ActorID
}

// MemoryEndpoint is one actor's attachment to a MemoryNetwork. It
// implements Endpoint.
type MemoryEndpoint struct {
	net    *MemoryNetwork
	id     cert.ActorID
	name   string
	accept chan pendingStream
	closed chan struct{}
	once   sync.Once
}

var _ Endpoint = (*MemoryEndpoint)(nil)

// ID returns the actor id the endpoint is bound to.
func (e *MemoryEndpoint) ID() cert.ActorID { return e.id }

// Tag returns the endpoint's "mem:<name>" string.
func (e *MemoryEndpoint) Tag() string { return MemTag + e.name }

// Endpoints returns the one tag peers can dial.
func (e *MemoryEndpoint) Endpoints() []string { return []string{e.Tag()} }

// Dial opens a stream to actor id: hints tagged "mem:" are tried first,
// then the network's own id table. The callee sees e.ID() as the peer.
func (e *MemoryEndpoint) Dial(ctx context.Context, id cert.ActorID, hints []string) (Stream, error) {
	select {
	case <-e.closed:
		return nil, ErrClosed
	default:
	}
	target, err := e.net.lookup(id, hints)
	if err != nil {
		return nil, err
	}
	local, remote := newStreamPair()
	select {
	case target.accept <- pendingStream{s: remote, peer: e.id}:
		return local, nil
	case <-target.closed:
		return nil, fmt.Errorf("%w: %s", ErrUnreachable, id)
	case <-e.closed:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Accept blocks for the next dialled stream.
func (e *MemoryEndpoint) Accept(ctx context.Context) (Stream, cert.ActorID, error) {
	select {
	case p := <-e.accept:
		return p.s, p.peer, nil
	case <-e.closed:
		return nil, "", ErrClosed
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

// Close unbinds the endpoint. Pending and future Accepts/Dials fail.
func (e *MemoryEndpoint) Close() error {
	e.once.Do(func() {
		close(e.closed)
		e.net.unbind(e)
	})
	return nil
}

// MemoryStream is one side of an in-memory bidirectional stream. Each
// direction is a channel of capacity one that the sender closes after
// its single message (FIN), so the receiver's RecvMsg sees exactly the
// ruling-§6 contract: one message, then EOF.
type MemoryStream struct {
	out chan []byte // this side → peer; closed on FIN
	in  chan []byte // peer → this side; closed by the peer's FIN

	mu       sync.Mutex
	finished bool // out has been closed
	closed   chan struct{}
	once     sync.Once
}

var _ Stream = (*MemoryStream)(nil)

func newStreamPair() (*MemoryStream, *MemoryStream) {
	ab := make(chan []byte, 1)
	ba := make(chan []byte, 1)
	a := &MemoryStream{out: ab, in: ba, closed: make(chan struct{})}
	b := &MemoryStream{out: ba, in: ab, closed: make(chan struct{})}
	return a, b
}

// SendMsg delivers msg as the whole send side and FINs it.
func (s *MemoryStream) SendMsg(ctx context.Context, msg []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.closed:
		return ErrClosed
	default:
	}
	if s.finished {
		return ErrStreamFinished
	}
	// Capacity one and a single send per side: this never blocks, but
	// keep the ctx select so a future bounded variant stays correct.
	select {
	case s.out <- append([]byte(nil), msg...):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.finished = true
	close(s.out)
	return nil
}

// RecvMsg returns the peer's message, or io.EOF once the peer has
// FINed with nothing (or nothing more) to deliver.
func (s *MemoryStream) RecvMsg(ctx context.Context) ([]byte, error) {
	select {
	case msg, ok := <-s.in:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-s.closed:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close FINs the send side if it is still open and releases the stream.
func (s *MemoryStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		if !s.finished {
			s.finished = true
			close(s.out)
		}
		close(s.closed)
		s.mu.Unlock()
	})
	return nil
}
