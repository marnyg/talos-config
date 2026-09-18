package actor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// TestMultiEndpoint: one identity on two wires. Two MemoryNetworks
// stand in for "in-process" and "iroh": the hub is bound on both under
// one key, a sibling lives only on the first, a member only on the
// second. The hub accepts from either and dials each through the
// endpoint that owns the hint; a peer on neither is ErrUnreachable.
func TestMultiEndpoint(t *testing.T) {
	ctx := context.Background()
	inproc, wan := NewMemoryNetwork(), NewMemoryNetwork()
	hub, sib, mem := newSigner(t), newSigner(t), newSigner(t)

	hubIn, err := inproc.Bind(hub.ActorID(), "hub")
	if err != nil {
		t.Fatal(err)
	}
	hubWan, err := wan.Bind(hub.ActorID(), "hub")
	if err != nil {
		t.Fatal(err)
	}
	sibEp, _ := inproc.Bind(sib.ActorID(), "sib")
	memEp, _ := wan.Bind(mem.ActorID(), "mem")

	if _, err := NewMulti(); err == nil {
		t.Fatal("empty multi accepted")
	}
	if _, err := NewMulti(hubIn, sibEp); err == nil {
		t.Fatal("multi over two identities accepted")
	}
	m, err := NewMulti(hubIn, hubWan)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID() != hub.ActorID() {
		t.Fatalf("id %s", m.ID())
	}
	if got := m.Endpoints(); len(got) != 2 || got[0] != "mem:hub" || got[1] != "mem:hub" {
		t.Fatalf("endpoints %v", got)
	}

	// Inbound from both wires lands on one Accept.
	echo := func(s Stream, peer cert.ActorID) {
		defer s.Close()
		msg, _ := s.RecvMsg(ctx)
		_ = s.SendMsg(ctx, append([]byte(string(peer)+":"), msg...))
	}
	go func() {
		for range 2 {
			s, peer, err := m.Accept(ctx)
			if err != nil {
				t.Errorf("accept: %v", err)
				return
			}
			go echo(s, peer)
		}
	}()
	for _, c := range []struct {
		from Endpoint
		id   cert.ActorID
	}{{sibEp, sib.ActorID()}, {memEp, mem.ActorID()}} {
		s, err := c.from.Dial(ctx, hub.ActorID(), []string{"mem:hub"})
		if err != nil {
			t.Fatal(err)
		}
		_ = s.SendMsg(ctx, []byte("hi"))
		got, err := s.RecvMsg(ctx)
		if err != nil || string(got) != string(c.id)+":hi" {
			t.Fatalf("reply %q %v", got, err)
		}
		s.Close()
	}

	// Outbound: the hub reaches the member only through the second
	// wire (the first says unreachable and is skipped), the sibling
	// through the first. A hint naming another actor's endpoint is a
	// mismatch — final, not skipped.
	serve := func(ep Endpoint) {
		s, _, err := ep.Accept(ctx)
		if err != nil {
			return
		}
		defer s.Close()
		_, _ = s.RecvMsg(ctx)
		_ = s.SendMsg(ctx, []byte("ok"))
	}
	for _, c := range []struct {
		ep Endpoint
		id cert.ActorID
	}{{memEp, mem.ActorID()}, {sibEp, sib.ActorID()}} {
		go serve(c.ep)
		s, err := m.Dial(ctx, c.id, nil)
		if err != nil {
			t.Fatalf("dial %s: %v", c.id, err)
		}
		_ = s.SendMsg(ctx, []byte("ping"))
		if got, err := s.RecvMsg(ctx); err != nil || string(got) != "ok" {
			t.Fatalf("reply %q %v", got, err)
		}
		s.Close()
	}
	if _, err := m.Dial(ctx, newSigner(t).ActorID(), []string{"iroh:relay=x"}); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("want unreachable, got %v", err)
	}
	if _, err := m.Dial(ctx, mem.ActorID(), []string{"mem:sib"}); !errors.Is(err, ErrPeerMismatch) {
		t.Fatalf("want peer mismatch, got %v", err)
	}

	// Close closes every member; Accept and Dial fail closed.
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Accept(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("accept after close: %v", err)
	}
	if _, err := m.Dial(ctx, sib.ActorID(), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("dial after close: %v", err)
	}
	if _, _, err := hubWan.Accept(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("member not closed: %v", err)
	}
	if _, err := sibEp.Dial(ctx, hub.ActorID(), nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("hub still bound after close: %v", err)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, _, err := memEp.Accept(cctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unrelated endpoint affected: %v", err)
	}
}

// TestMultiEndpointRunsAnActor: the runtime itself over a Multi — the
// two-actor handshake from either wire lands in one mailbox.
func TestMultiEndpointRunsAnActor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inproc, wan := NewMemoryNetwork(), NewMemoryNetwork()
	hub, sib, mem := newSigner(t), newSigner(t), newSigner(t)
	hubIn, _ := inproc.Bind(hub.ActorID(), "hub")
	hubWan, _ := wan.Bind(hub.ActorID(), "hub")
	m, err := NewMulti(hubIn, hubWan)
	if err != nil {
		t.Fatal(err)
	}
	sibEp, _ := inproc.Bind(sib.ActorID(), "sib")
	memEp, _ := wan.Bind(mem.ActorID(), "mem")

	now := time.Now().Unix()
	h := New(hub, m)
	h.AcceptTable["#echo"] = func(_ context.Context, inv *Invocation) ([]byte, error) {
		return append([]byte(string(inv.From)+":"), inv.Envelope.Payload...), nil
	}
	consent := func(to cert.ActorID) cert.Cert {
		return issue(t, hub, string(to), []cert.ActorID{hub.ActorID()}, []string{"#echo"}, false, now, now+3600)
	}
	h.Consents = []cert.Cert{consent(sib.ActorID()), consent(mem.ActorID())}
	go h.Listen(ctx)

	for _, c := range []struct {
		s  cert.EdSigner
		ep Endpoint
	}{{sib, sibEp}, {mem, memEp}} {
		a := New(c.s, c.ep)
		if err := a.UpdateLocation(hub.ActorID(), mustLoc(t, hub, now, "mem:hub")); err != nil {
			t.Fatal(err)
		}
		r, err := a.Send(ctx, hub.ActorID(), "#echo", []byte("x"))
		if err != nil {
			t.Fatalf("%s: %v", c.s.ActorID(), err)
		}
		if string(r.Payload) != string(c.s.ActorID())+":x" {
			t.Fatalf("payload %q", r.Payload)
		}
	}
}

func mustLoc(t *testing.T, s cert.Signer, now int64, eps ...string) *cert.Cert {
	t.Helper()
	loc, err := cert.Sign(cert.Cert{
		Aud: cert.AudAny,
		Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Endpoints: eps},
		Iat: now,
		Exp: now + 3600,
	}, s)
	if err != nil {
		t.Fatal(err)
	}
	return &loc
}
