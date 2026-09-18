package actor

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/marnyg/talos-config/protocol/cert"
)

// Multi is one actor identity bound on several transports at once: an
// Endpoint that accepts from all of them and dials through whichever
// owns the hint. An actor is a keypair, not a socket — the same key
// may sit on an in-process wire for its siblings and on iroh for the
// world (talos-config-e8d: the hub's Issuer serves Enroll over
// MemoryNetwork and members over its iroh endpoint from one inbox).
//
// Dial tries the endpoints in the order given and moves on only from
// ErrUnreachable — the transport saying "no hint of mine, not my
// peer" — so order them cheapest first. Any other error is final.
type Multi struct {
	id  cert.ActorID
	eps []Endpoint

	accept chan accepted
	closed chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	loops  sync.WaitGroup
}

var _ Endpoint = (*Multi)(nil)

type accepted struct {
	s    Stream
	peer cert.ActorID
}

// NewMulti binds one identity over eps, which must all report the same
// ID. It starts one accept loop per endpoint; Close stops them and
// closes the endpoints.
func NewMulti(eps ...Endpoint) (*Multi, error) {
	if len(eps) == 0 {
		return nil, errors.New("actor: multi endpoint needs at least one endpoint")
	}
	id := eps[0].ID()
	for _, e := range eps[1:] {
		if e.ID() != id {
			return nil, fmt.Errorf("actor: multi endpoint over two identities: %s and %s", id, e.ID())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Multi{
		id:     id,
		eps:    append([]Endpoint(nil), eps...),
		accept: make(chan accepted),
		closed: make(chan struct{}),
		cancel: cancel,
	}
	for _, e := range m.eps {
		m.loops.Add(1)
		go m.acceptLoop(ctx, e)
	}
	return m, nil
}

// ID is the shared identity.
func (m *Multi) ID() cert.ActorID { return m.id }

// Endpoints is the union of every member's tags, in member order.
func (m *Multi) Endpoints() []string {
	var out []string
	for _, e := range m.eps {
		out = append(out, e.Endpoints()...)
	}
	return out
}

// Dial tries each endpoint in order; ErrUnreachable moves to the next.
// When none reaches id, the joined errors are returned (still
// ErrUnreachable under errors.Is).
func (m *Multi) Dial(ctx context.Context, id cert.ActorID, hints []string) (Stream, error) {
	select {
	case <-m.closed:
		return nil, ErrClosed
	default:
	}
	var errs []error
	for _, e := range m.eps {
		s, err := e.Dial(ctx, id, hints)
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, ErrUnreachable) {
			return nil, err
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// Accept returns the next stream from any member endpoint.
func (m *Multi) Accept(ctx context.Context) (Stream, cert.ActorID, error) {
	select {
	case a := <-m.accept:
		return a.s, a.peer, nil
	case <-m.closed:
		return nil, "", ErrClosed
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

func (m *Multi) acceptLoop(ctx context.Context, e Endpoint) {
	defer m.loops.Done()
	for {
		s, peer, err := e.Accept(ctx)
		if err != nil {
			return // member closed, or we are closing
		}
		select {
		case m.accept <- accepted{s: s, peer: peer}:
		case <-m.closed:
			_ = s.Close()
			return
		}
	}
}

// Close closes every member endpoint; pending Accepts return ErrClosed.
// The first member error is returned.
func (m *Multi) Close() error {
	var err error
	m.once.Do(func() {
		close(m.closed)
		m.cancel()
		for _, e := range m.eps {
			if cerr := e.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
		m.loops.Wait()
	})
	return err
}
