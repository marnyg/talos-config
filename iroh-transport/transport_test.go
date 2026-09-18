package irohtransport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// bindLoopback binds a fresh identity on 127.0.0.1 and closes it with
// the test.
func bindLoopback(t *testing.T, relay string) (*Endpoint, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Bind(priv, Options{BindAddr: "127.0.0.1:0", Relay: relay})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e, priv
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestIrohTransportContract is TestMemoryTransport from protocol/actor
// replayed over two iroh endpoints: identity, tags, unreachable, one
// message per direction then EOF, empty FIN on Close, closed endpoint.
func TestIrohTransportContract(t *testing.T) {
	ctx := testCtx(t)
	ex, px := bindLoopback(t, "")
	ey, py := bindLoopback(t, "")
	x, y := cert.NewEdSigner(px).ActorID(), cert.NewEdSigner(py).ActorID()

	if ex.ID() != x || ey.ID() != y {
		t.Fatalf("endpoint ids %s %s, want %s %s", ex.ID(), ey.ID(), x, y)
	}
	tags := ey.Endpoints()
	if len(tags) == 0 || !strings.HasPrefix(tags[0], TagUDP+"127.0.0.1:") {
		t.Fatalf("Endpoints() = %v, want one iroh:udp=127.0.0.1:* tag", tags)
	}
	for _, tag := range tags {
		if strings.HasPrefix(tag, TagRelay) {
			t.Fatalf("relay tag %q with RelayMode::Disabled", tag)
		}
	}

	// No iroh hint and no prior contact ⇒ unreachable, fast.
	if _, err := ex.Dial(ctx, y, []string{"mem:y", "other:thing"}); !errors.Is(err, actor.ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
	// Non-ed actor ⇒ unreachable + ErrNotEdActor.
	eth := cert.ActorID("eth:0x1234567890abcdef1234567890abcdef12345678")
	if _, err := ex.Dial(ctx, eth, tags); !errors.Is(err, actor.ErrUnreachable) || !errors.Is(err, ErrNotEdActor) {
		t.Fatalf("eth dial: %v", err)
	}

	// One message per direction, then EOF; peer identity on Accept.
	srv := make(chan error, 1)
	go func() {
		s, peer, err := ey.Accept(ctx)
		if err != nil {
			srv <- err
			return
		}
		defer s.Close()
		if peer != x {
			srv <- errors.New("accept peer " + string(peer) + " want " + string(x))
			return
		}
		msg, err := s.RecvMsg(ctx)
		if err != nil {
			srv <- err
			return
		}
		if _, err := s.RecvMsg(ctx); !errors.Is(err, io.EOF) {
			srv <- errors.New("second recv: want EOF")
			return
		}
		if err := s.SendMsg(ctx, append([]byte("re:"), msg...)); err != nil {
			srv <- err
			return
		}
		if err := s.SendMsg(ctx, []byte("again")); !errors.Is(err, actor.ErrStreamFinished) {
			srv <- errors.New("second send: want ErrStreamFinished")
			return
		}
		srv <- nil
	}()
	s, err := ex.Dial(ctx, y, tags)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendMsg(ctx, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecvMsg(ctx)
	if err != nil || string(got) != "re:hi" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := s.RecvMsg(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("after peer FIN: %v", err)
	}
	s.Close()
	if err := <-srv; err != nil {
		t.Fatal(err)
	}

	// Second dial reuses the pooled connection (no hints needed now:
	// iroh remembers the peer too, but the pool is hit first). The
	// acceptor closes without replying (what the runtime does on an
	// undecodable envelope): its Close FINs empty, the dialler sees EOF.
	// Note the dialler sends first: a QUIC bi-stream does not exist for
	// the acceptor until its first frame, unlike the in-memory transport.
	go func() {
		s, _, err := ey.Accept(ctx)
		if err != nil {
			return
		}
		_, _ = s.RecvMsg(ctx)
		s.Close()
	}()
	s, err = ex.Dial(ctx, y, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendMsg(ctx, []byte("{not an envelope")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecvMsg(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after peer close, got %v", err)
	}
	s.Close()

	// Closed endpoint: Accept and Dial fail; a dial to it is unreachable.
	if err := ey.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ey.Accept(ctx); !errors.Is(err, actor.ErrClosed) {
		t.Fatalf("accept on closed: %v", err)
	}
	if _, err := ey.Dial(ctx, x, ex.Endpoints()); !errors.Is(err, actor.ErrClosed) {
		t.Fatalf("dial from closed: %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := ex.Dial(short, y, tags); err == nil {
		t.Fatal("dial to a closed endpoint succeeded")
	} else {
		t.Logf("dial closed peer: %v", err)
	}
	cctx, ccancel := context.WithCancel(ctx)
	ccancel()
	if _, _, err := ex.Accept(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("accept ctx: %v", err)
	}
}

// TestConcurrentStreamsOnePeer: many dials to one peer ride one pooled
// connection and all complete (the accept side loops AcceptBi).
func TestConcurrentStreamsOnePeer(t *testing.T) {
	ctx := testCtx(t)
	ex, _ := bindLoopback(t, "")
	ey, _ := bindLoopback(t, "")
	tags := ey.Endpoints()
	const n = 16

	go func() {
		for {
			s, _, err := ey.Accept(ctx)
			if err != nil {
				return
			}
			go func() {
				defer s.Close()
				m, err := s.RecvMsg(ctx)
				if err != nil {
					return
				}
				_ = s.SendMsg(ctx, m)
			}()
		}
	}()
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			s, err := ex.Dial(ctx, ey.ID(), tags)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			want := []byte{byte(i), 'x'}
			if err := s.SendMsg(ctx, want); err != nil {
				errs <- err
				return
			}
			got, err := s.RecvMsg(ctx)
			if err != nil || string(got) != string(want) {
				errs <- errors.New("echo mismatch")
				return
			}
			errs <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	ex.mu.Lock()
	pooled := len(ex.conns)
	ex.mu.Unlock()
	if pooled != 1 {
		t.Fatalf("pooled connections = %d, want 1", pooled)
	}
}

// TestAdvertiseRelay: the hub's shape (talos-config-e8d). The receiver
// homes on the relay at one name (loopback, as the hub does with its
// relay child) and advertises another (the public hostname); the
// caller, homed at the advertised name, dials with only that hint. The
// relay forwards by EndpointId, so two names for one server meet.
// Needs IROH_RELAY_BIN or iroh-relay on PATH; skipped otherwise.
func TestAdvertiseRelay(t *testing.T) {
	ctx := testCtx(t)
	home := startRelay(t) // http://127.0.0.1:<port>
	public := strings.Replace(home, "127.0.0.1", "localhost", 1)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := Bind(priv, Options{BindAddr: "127.0.0.1:0", Relay: home, AdvertiseRelay: public})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	member, _ := bindLoopback(t, public)
	if err := hub.Online(ctx); err != nil {
		t.Fatal(err)
	}
	if err := member.Online(ctx); err != nil {
		t.Fatal(err)
	}

	tags := hub.Endpoints()
	if len(tags) == 0 || tags[0] != TagRelay+public {
		t.Fatalf("Endpoints() = %v, want %s first", tags, TagRelay+public)
	}
	for _, tag := range tags[1:] {
		if strings.HasPrefix(tag, TagRelay) {
			t.Fatalf("second relay tag %q", tag)
		}
	}

	srv := make(chan error, 1)
	go func() {
		s, peer, err := hub.Accept(ctx)
		if err != nil {
			srv <- err
			return
		}
		defer s.Close()
		if peer != member.ID() {
			srv <- fmt.Errorf("peer %s, want %s", peer, member.ID())
			return
		}
		msg, err := s.RecvMsg(ctx)
		if err != nil {
			srv <- err
			return
		}
		srv <- s.SendMsg(ctx, append([]byte("re:"), msg...))
	}()
	s, err := member.Dial(ctx, hub.ID(), []string{TagRelay + public})
	if err != nil {
		t.Fatalf("dial through the advertised name: %v", err)
	}
	defer s.Close()
	if err := s.SendMsg(ctx, []byte("beat")); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecvMsg(ctx)
	if err != nil || string(got) != "re:beat" {
		t.Fatalf("reply %q %v", got, err)
	}
	if err := <-srv; err != nil {
		t.Fatal(err)
	}
}
