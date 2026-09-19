package irohtransport

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/marnyg/talos-config/iroh-go/iroh"
	"github.com/marnyg/talos-config/protocol/actor"
)

// DefaultMaxMsg bounds one message (one stream direction) when
// Options.MaxMsg is zero. Envelopes carry cert chains, not payloads of
// note; 1 MiB is generous.
const DefaultMaxMsg = 1 << 20

// Stream is one QUIC bidirectional stream carrying at most one message
// per direction (ruling §6). It implements actor.Stream.
//
// QUIC caveat: a dialled stream does not exist for the acceptor until
// its first frame, so the dialler must SendMsg before it can expect
// anything from RecvMsg (the runtime always does). The in-memory
// transport delivers the stream on Dial; this one on the first byte.
type Stream struct {
	bi     *iroh.BiStream
	maxMsg uint32

	mu       sync.Mutex
	finished bool // send side FINed (or reset)
	received bool // recv side drained
	closed   bool
}

var _ actor.Stream = (*Stream)(nil)

func newStream(bi *iroh.BiStream, maxMsg uint32) *Stream {
	return &Stream{bi: bi, maxMsg: maxMsg}
}

// SendMsg writes msg as the whole send side and FINs it. A second call
// returns actor.ErrStreamFinished. The write is blocking on the FFI
// side; ctx cancellation returns early but the write itself completes
// or fails on its own (the goroutine is released when it does).
func (s *Stream) SendMsg(ctx context.Context, msg []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return actor.ErrClosed
	}
	if s.finished {
		s.mu.Unlock()
		return actor.ErrStreamFinished
	}
	s.finished = true
	s.mu.Unlock()

	send := s.bi.Send()
	defer send.Destroy()
	_, err := await(ctx, nil, func() (struct{}, error) {
		if err := send.WriteAll(msg); err != nil {
			return struct{}{}, fmt.Errorf("irohtransport: write: %w", err)
		}
		if err := send.Finish(); err != nil {
			return struct{}{}, fmt.Errorf("irohtransport: finish: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

// RecvMsg reads the peer's whole send side until FIN. A second call, or
// a FIN with no bytes, returns io.EOF. (QUIC cannot tell an empty
// message from a bare FIN; envelopes are never empty, so empty = EOF,
// which is exactly what the runtime expects from a peer that closed
// without replying.)
func (s *Stream) RecvMsg(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, actor.ErrClosed
	}
	if s.received {
		s.mu.Unlock()
		return nil, io.EOF
	}
	s.received = true
	s.mu.Unlock()

	recv := s.bi.Recv()
	defer recv.Destroy()
	data, err := await(ctx, nil, func() ([]byte, error) {
		return recv.ReadToEnd(s.maxMsg)
	})
	if err != nil {
		return nil, fmt.Errorf("irohtransport: read: %w", err)
	}
	if len(data) == 0 {
		return nil, io.EOF
	}
	return data, nil
}

// Close releases the stream. If the send side was never finished it is
// FINed empty, so the peer's RecvMsg observes io.EOF. The connection
// the stream rides on is owned by the Endpoint and stays open.
func (s *Stream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	fin := !s.finished
	s.finished = true
	s.mu.Unlock()

	if fin {
		send := s.bi.Send()
		_ = send.Finish() // empty FIN; a failure here means the peer is gone anyway
		send.Destroy()
	}
	s.bi.Destroy()
	return nil
}

// await runs f on its own goroutine (the binding's async calls block
// the caller) and returns early on ctx or closed. A result that lands
// after the caller gave up is released (Destroy on the FFI handle — a
// late connection or stream would otherwise live until the finalizer
// runs, holding its peer's resources with it), never returned.
func await[T any](ctx context.Context, closed <-chan struct{}, f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	done := make(chan res, 1)
	go func() {
		v, err := f()
		done <- res{v, err}
	}()
	var zero T
	select {
	case r := <-done:
		return r.v, r.err
	case <-closed:
	case <-ctx.Done():
	}
	go func() {
		if r := <-done; r.err == nil {
			if d, ok := any(r.v).(interface{ Destroy() }); ok {
				d.Destroy()
			}
		}
	}()
	select {
	case <-closed:
		return zero, actor.ErrClosed
	default:
		return zero, ctx.Err()
	}
}
