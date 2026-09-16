package actor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/envelope"
)

// ---- fixtures -------------------------------------------------------------

const t0 int64 = 1_700_000_000

// fakeClock is a shared, settable local clock for every actor in a test.
type fakeClock struct{ now atomic.Int64 }

func newClock() *fakeClock {
	c := &fakeClock{}
	c.now.Store(t0)
	return c
}
func (c *fakeClock) Now() int64      { return c.now.Load() }
func (c *fakeClock) Advance(d int64) { c.now.Add(d) }

func newSigner(t *testing.T) cert.EdSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return cert.NewEdSigner(priv)
}

// issue signs an invoke cert from s to aud over targets/facets.
func issue(t *testing.T, s cert.Signer, aud string, targets []cert.ActorID, facets []string, delegable bool, iat, exp int64) cert.Cert {
	t.Helper()
	c, err := cert.Sign(cert.Cert{
		Aud: aud,
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: targets, Facet: facets, Delegable: delegable},
		Iat: iat,
		Exp: exp,
	}, s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func speakAs(t *testing.T, principal cert.Signer, hot cert.ActorID, verbs []string, iat, exp int64) cert.Cert {
	t.Helper()
	c, err := cert.Sign(cert.Cert{
		Aud: string(hot),
		Can: cert.VerbSpeakAs,
		Cav: cert.Caveats{Verbs: verbs},
		Iat: iat,
		Exp: exp,
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// world is one MemoryNetwork with actors that share a fake clock.
type world struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc
	net    *MemoryNetwork
	clk    *fakeClock
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	return &world{t: t, ctx: ctx, cancel: cancel, net: NewMemoryNetwork(), clk: newClock()}
}

// actor binds a new actor named name and starts Listen.
func (w *world) actor(name string) (*Actor, cert.EdSigner, *MemoryEndpoint) {
	w.t.Helper()
	s := newSigner(w.t)
	ep, err := w.net.Bind(s.ActorID(), name)
	if err != nil {
		w.t.Fatal(err)
	}
	a := New(s, ep)
	a.Clock = w.clk.Now
	return a, s, ep
}

func (w *world) start(a *Actor) {
	w.t.Helper()
	done := make(chan error, 1)
	go func() { done <- a.Listen(w.ctx) }()
	w.t.Cleanup(func() {
		w.cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				w.t.Errorf("Listen: %v", err)
			}
		case <-time.After(5 * time.Second):
			w.t.Error("Listen did not stop")
		}
	})
}

func echo(_ context.Context, inv *Invocation) ([]byte, error) {
	return inv.Envelope.Payload, nil
}

func wantRemote(t *testing.T, err error, code string) {
	t.Helper()
	var re *RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("want RemoteError %s, got %v", code, err)
	}
	if re.Code != code {
		t.Fatalf("want status %s, got %s (%s)", code, re.Code, re.Msg)
	}
}

// rawSend pushes an arbitrary signed envelope over ep to its target and
// returns the decoded status (bypassing Actor.Send's seq counter).
func rawSend(t *testing.T, ctx context.Context, ep *MemoryEndpoint, env envelope.Envelope) (Status, envelope.Reply) {
	t.Helper()
	wire, err := envelope.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ep.Dial(ctx, env.To.Target, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SendMsg(ctx, wire); err != nil {
		t.Fatal(err)
	}
	raw, err := s.RecvMsg(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := envelope.DecodeReply(raw)
	if err != nil {
		t.Fatal(err)
	}
	st, err := DecodeStatus(rep.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return st, rep
}

// ---- 1. two actors, real cert.VerifyChain, 2- and 3-link chains --------

func TestTwoActorsHandshake(t *testing.T) {
	w := newWorld(t)
	now := w.clk.Now()

	b, bs, _ := w.actor("b") // receiver
	a, as, _ := w.actor("a") // caller holding a grant from principal P
	c, _, _ := w.actor("c")  // third actor, re-delegated to by A
	p := newSigner(t)        // principal B consented to (no runtime needed)
	stranger, _, _ := w.actor("stranger")

	B := b.ID()
	b.AcceptTable["echo"] = echo
	b.AcceptTable["admin"] = func(context.Context, *Invocation) ([]byte, error) {
		return []byte("admin ok"), nil
	}

	// Link 1 (held by B, prepended by the verifier): B consents to P.
	consent := issue(t, bs, string(p.ActorID()), []cert.ActorID{B}, []string{"echo", "admin"}, true, now-30, now+3600)
	b.Consents = []cert.Cert{consent}
	// Link 2 (carried by A): P grants A, delegable.
	grantPA := issue(t, p, string(as.ActorID()), []cert.ActorID{B}, []string{"echo", "admin"}, true, now-20, now+3600)
	a.Grant(B, "echo", grantPA)
	a.Grant(B, "admin", grantPA)
	// Link 3 (carried by C): A re-delegates to C, attenuated to echo only.
	grantAC := issue(t, as, string(c.ID()), []cert.ActorID{B}, []string{"echo"}, false, now-10, now+3600)
	c.Grant(B, "echo", grantPA, grantAC)
	c.Grant(B, "admin", grantPA, grantAC)

	w.start(b)
	w.start(a)
	w.start(c)
	w.start(stranger)

	// 2-link chain: consent(B→P) · grant(P→A), signer A.
	rep, err := a.Send(w.ctx, B, "echo", []byte("hello"))
	if err != nil {
		t.Fatalf("A→B echo: %v", err)
	}
	if string(rep.Payload) != "hello" || rep.Status.Code != StatusOK || rep.Wire.From != B {
		t.Fatalf("bad reply: %+v", rep)
	}
	if rep, err := a.Send(w.ctx, B, "admin", nil); err != nil || string(rep.Payload) != "admin ok" {
		t.Fatalf("A→B admin: %v %+v", err, rep)
	}

	// 3-link chain: consent · grant(P→A) · grant(A→C), signer C.
	if rep, err := c.Send(w.ctx, B, "echo", []byte("from c")); err != nil || string(rep.Payload) != "from c" {
		t.Fatalf("C→B echo: %v %+v", err, rep)
	}
	// Attenuation holds: C's link dropped "admin".
	_, err = c.Send(w.ctx, B, "admin", nil)
	wantRemote(t, err, StatusUnauthorized)

	// No grant for the facet ⇒ empty caller chain ⇒ consent's aud (P) is
	// not A ⇒ unauthorized — before the accept table is even consulted.
	_, err = a.Send(w.ctx, B, "nope", nil)
	wantRemote(t, err, StatusUnauthorized)

	// A stranger with nothing rooted at B.
	_, err = stranger.Send(w.ctx, B, "echo", nil)
	wantRemote(t, err, StatusUnauthorized)

	// Unknown facet but a valid chain: A holds admin/echo only; register
	// the grant for a facet B does not serve.
	a.Grant(B, "missing", grantPA)
	_, err = a.Send(w.ctx, B, "missing", nil)
	wantRemote(t, err, StatusUnauthorized) // facet ∉ eff.Cav.Facet
	b.AcceptTable["echo"] = nil
	delete(b.AcceptTable, "echo")
	_, err = a.Send(w.ctx, B, "echo", nil)
	wantRemote(t, err, StatusUnknownFacet)

	// The low-water mark advanced from rooted certs (consent, grants),
	// never past local now.
	if lw := b.LowWater(); lw < consent.Iat || lw > now {
		t.Fatalf("low-water mark %d not in [%d,%d]", lw, consent.Iat, now)
	}
	if lw := b.LowWater(); lw != grantAC.Iat {
		t.Fatalf("low-water mark %d, want max rooted iat %d", lw, grantAC.Iat)
	}
}

// ---- 2. #renew ----------------------------------------------------------

func TestRenewHandler(t *testing.T) {
	w := newWorld(t)
	now := w.clk.Now()

	b, bs, _ := w.actor("b") // grantor
	a, as, _ := w.actor("a") // holder
	hot := newSigner(t)      // B's hot key
	other := newSigner(t)
	B, A := b.ID(), a.ID()

	// A may ask B to renew: an ordinary consent straight to A.
	b.Consents = []cert.Cert{issue(t, bs, string(A), []cert.ActorID{B}, []string{FacetRenew}, false, now-1, now+3600)}
	b.SpeakAs = []cert.Cert{speakAs(t, bs, hot.ActorID(), []string{"invoke"}, now-1, now+3600)}
	if _, err := b.PublishLocation(3600); err != nil {
		t.Fatal(err)
	}
	w.start(b)
	w.start(a)

	target := []cert.ActorID{other.ActorID()}
	held := issue(t, bs, string(A), target, []string{"f1", "f2"}, false, now-100, now+100)
	expired := issue(t, bs, string(A), target, []string{"f1"}, false, now-200, now-1)
	notMine := issue(t, as, string(A), target, []string{"f1"}, false, now-1, now+100)
	viaHot := issue(t, hot, string(A), target, []string{"f1"}, false, now-50, now+50)
	wrongAud := issue(t, bs, string(other.ActorID()), target, []string{"f1"}, false, now-1, now+100)

	narrow := held
	narrow.Cav.Facet = []string{"f1"}
	wider := held
	wider.Cav.Facet = []string{"f1", "f2", "f3"}
	moreDelegable := held
	moreDelegable.Cav.Delegable = true
	sameCav := held

	certs := []cert.Cert{held, expired, notMine, viaHot, wrongAud, held, held, held, held}
	want := []*cert.Cert{nil, nil, nil, nil, nil, &narrow, &wider, &moreDelegable, &sameCav}
	payload, err := EncodeRenewRequest(certs, want)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := a.Send(w.ctx, B, FacetRenew, payload)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if rep.Loc == nil || rep.Loc.Iss != B || rep.Loc.Cav.Endpoints[0] != "mem:b" {
		t.Fatalf("reply did not carry B's location: %+v", rep.Loc)
	}
	fresh, errs, err := DecodeRenewResponse(rep.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != len(certs) {
		t.Fatalf("got %d results, want %d", len(fresh), len(certs))
	}

	checkFresh := func(i int, old cert.Cert, facets []string, delegable bool) {
		t.Helper()
		if errs[i] != nil {
			t.Fatalf("item %d refused: %v", i, errs[i])
		}
		n := fresh[i]
		if n.Iss != B || n.Aud != old.Aud || n.Can != old.Can {
			t.Fatalf("item %d identity changed: %+v", i, n)
		}
		if n.Iat != now || n.Exp != now+(old.Exp-old.Iat) {
			t.Fatalf("item %d iat/exp = %d/%d, want %d/%d", i, n.Iat, n.Exp, now, now+(old.Exp-old.Iat))
		}
		if cert.Verify(n) != nil {
			t.Fatalf("item %d does not verify", i)
		}
		if fmt.Sprint(n.Cav.Facet) != fmt.Sprint(facets) || n.Cav.Delegable != delegable ||
			fmt.Sprint(n.Cav.Target) != fmt.Sprint(old.Cav.Target) {
			t.Fatalf("item %d caveats %+v", i, n.Cav)
		}
	}
	checkRefused := func(i int, why string) {
		t.Helper()
		if errs[i] == nil || errs[i].Error()[:len(why)] != why {
			t.Fatalf("item %d: want refusal %q, got %v (cert %+v)", i, why, errs[i], fresh[i])
		}
	}

	checkFresh(0, held, []string{"f1", "f2"}, false)
	checkRefused(1, RefuseExpired)
	checkRefused(2, RefuseNotMine)
	checkFresh(3, viaHot, []string{"f1"}, false) // hot-key issued, re-signed by B itself
	checkRefused(4, RefuseAud)
	checkFresh(5, held, []string{"f1"}, false) // narrower
	checkRefused(6, RefuseWider)
	checkRefused(7, RefuseWider) // delegable:false → true is wider
	checkFresh(8, held, []string{"f1", "f2"}, false)

	// #renew is an ordinary facet: without a chain to (B, #renew) the
	// holder of B-signed certs is still refused.
	c, _, _ := w.actor("c")
	w.start(c)
	_, err = c.Send(w.ctx, B, FacetRenew, payload)
	wantRemote(t, err, StatusUnauthorized)

	// RenewTTL overrides the original lifetime.
	b.RenewTTL = 42
	payload, _ = EncodeRenewRequest([]cert.Cert{held}, nil)
	rep, err = a.Send(w.ctx, B, FacetRenew, payload)
	if err != nil {
		t.Fatal(err)
	}
	fresh, errs, _ = DecodeRenewResponse(rep.Payload)
	if errs[0] != nil || fresh[0].Exp != now+42 {
		t.Fatalf("RenewTTL not honoured: %v %+v", errs[0], fresh[0])
	}

	// Speak-as expired ⇒ hot-key issuance no longer recognised.
	w.clk.Advance(3601)
	if b.issuedByMe(viaHot, nil, w.clk.Now()) {
		t.Fatal("hot-key cert recognised after its speak-as expired")
	}
}

// TestRenewViaHolderHotKey is talos-config-7ei: a holder whose cert
// names its cold principal A renews it from its hot key A_HOT. The
// renew handler binds old.aud to the invocation's signer exactly as
// VerifyChain rule 3 does — a live speak-as A→A_HOT in the proof with
// cav.verbs ∋ invoke — and re-issues with aud A unchanged. Without such
// a speak-as (none, or expired) the same cert is refused with RefuseAud,
// even though the hot key reaches #renew on its own consent.
func TestRenewViaHolderHotKey(t *testing.T) {
	w := newWorld(t)
	now := w.clk.Now()

	b, bs, _ := w.actor("b")      // grantor
	aCold := newSigner(t)         // the holder's cold principal A; never on the wire
	ahot, _, _ := w.actor("ahot") // A's hot key, the actor that actually calls
	A, AHOT, B := aCold.ActorID(), ahot.ID(), b.ID()

	// B consents to A (the principal) for #renew; A_HOT also holds a
	// direct consent, so it reaches #renew even with no speak-as in the
	// proof — that is what makes the RefuseAud cases reach the handler.
	b.Consents = []cert.Cert{
		issue(t, bs, string(A), []cert.ActorID{B}, []string{FacetRenew}, false, now-1, now+3600),
		issue(t, bs, string(AHOT), []cert.ActorID{B}, []string{FacetRenew}, false, now-1, now+3600),
	}
	w.start(b)
	w.start(ahot)

	held := issue(t, bs, string(A), []cert.ActorID{B}, []string{"f1"}, false, now-100, now+100)
	payload, err := EncodeRenewRequest([]cert.Cert{held}, nil)
	if err != nil {
		t.Fatal(err)
	}
	renew := func() (cert.Cert, error) {
		t.Helper()
		rep, err := ahot.Send(w.ctx, B, FacetRenew, payload)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		fresh, errs, err := DecodeRenewResponse(rep.Payload)
		if err != nil {
			t.Fatal(err)
		}
		return fresh[0], errs[0]
	}

	// no speak-as in the proof: A_HOT is authorized (own consent) but the
	// cert's aud A is not the caller and nobody says A_HOT speaks for A
	if _, rerr := renew(); rerr == nil || rerr.Error() != RefuseAud {
		t.Fatalf("no speak-as: want %q, got %v", RefuseAud, rerr)
	}
	// live speak-as A→A_HOT covering invoke ⇒ renewed, aud stays A
	ahot.SpeakAs = []cert.Cert{speakAs(t, aCold, AHOT, []string{"invoke"}, now-1, now+60)}
	fresh, rerr := renew()
	if rerr != nil {
		t.Fatalf("with speak-as: refused %v", rerr)
	}
	if fresh.Aud != string(A) || fresh.Iss != B || fresh.Iat != now || cert.Verify(fresh) != nil {
		t.Fatalf("renewed cert wrong: %+v", fresh)
	}
	// speak-as covering member only does not bind an invoke
	ahot.SpeakAs = []cert.Cert{speakAs(t, aCold, AHOT, []string{"member"}, now-1, now+60)}
	if _, rerr := renew(); rerr == nil || rerr.Error() != RefuseAud {
		t.Fatalf("member-only speak-as: want %q, got %v", RefuseAud, rerr)
	}
	// expired speak-as ⇒ refused again
	ahot.SpeakAs = []cert.Cert{speakAs(t, aCold, AHOT, []string{"invoke"}, now-1, now+60)}
	w.clk.Advance(61)
	if _, rerr := renew(); rerr == nil || rerr.Error() != RefuseAud {
		t.Fatalf("expired speak-as: want %q, got %v", RefuseAud, rerr)
	}
}

// TestRenewAcrossHotKeyRotation is the talos hub shape (ADR-0018 xfx,
// talos-config-359.8.1): wallet W empowers hub key A by speak-as; A
// mints node N a member cert and a #renew grant naming target W. The
// hub redeploys: key B, a fresh speak-as W→B, no memory of A. N's next
// beat at B must renew both certs with what it already holds — the
// chain links, A's speak-as, and B's id — and get certs signed by B.
// Negative cases: no speak-as W→A in the proof (B cannot resolve A);
// A's speak-as expired; A empowered by a different wallet; B's own
// speak-as not covering the cert's verb.
func TestRenewAcrossHotKeyRotation(t *testing.T) {
	w := newWorld(t)
	now := w.clk.Now()

	wallet := newSigner(t) // the sovereign; never on the wire
	W := wallet.ActorID()
	hubA := newSigner(t) // dead process; only its signatures survive
	hubB, hubBs, _ := w.actor("hubB")
	n, _, _ := w.actor("n")
	A, B, N := hubA.ActorID(), hubB.ID(), n.ID()

	saA := speakAs(t, wallet, A, []string{"member", "invoke"}, now-1000, now+3600)
	saB := speakAs(t, wallet, B, []string{"member", "invoke"}, now-1, now+3600)

	// what A minted for N before dying
	member, err := cert.Sign(cert.Cert{
		Aud: string(N), Can: cert.VerbMember,
		Cav: cert.Caveats{Name: "n", Groups: []string{"machines"}},
		Iat: now - 500, Exp: now + 1000,
	}, hubA)
	if err != nil {
		t.Fatal(err)
	}
	grant := issue(t, hubA, string(N), []cert.ActorID{W}, []string{FacetRenew}, false, now-500, now+1000)

	// B: answers for W (rule 4), consents to W for #renew as Owner#renew
	hubB.SpeakAs = []cert.Cert{saB}
	hubB.Consents = []cert.Cert{
		issue(t, hubBs, string(W), []cert.ActorID{W}, []string{FacetRenew}, true, now-1, now+3600),
	}
	w.start(hubB)
	w.start(n)

	payload, err := EncodeRenewRequest([]cert.Cert{member, grant}, nil)
	if err != nil {
		t.Fatal(err)
	}
	renew := func() ([]cert.Cert, []error, *RemoteError) {
		t.Helper()
		rep, err := n.Send(w.ctx, B, FacetRenew, payload)
		if err != nil {
			var re *RemoteError
			if errors.As(err, &re) {
				return nil, nil, re
			}
			t.Fatalf("send: %v", err)
		}
		fresh, errs, err := DecodeRenewResponse(rep.Payload)
		if err != nil {
			t.Fatal(err)
		}
		return fresh, errs, nil
	}

	// N holds the chain WITH A's speak-as: B resolves A→W and W→B.
	n.Grant(B, FacetRenew, grant, saA)
	fresh, errs, rerr := renew()
	if rerr != nil {
		t.Fatalf("rotation renew rejected at the chain: %v", rerr)
	}
	for i, e := range errs {
		if e != nil {
			t.Fatalf("item %d refused: %v", i, e)
		}
	}
	if fresh[0].Iss != B || fresh[0].Can != cert.VerbMember || fresh[0].Aud != string(N) || fresh[0].Cav.Name != "n" || fresh[0].Iat != now {
		t.Fatalf("member not re-issued by B: %+v", fresh[0])
	}
	if fresh[1].Iss != B || fresh[1].Can != cert.VerbInvoke || fresh[1].Exp != now+1500 {
		t.Fatalf("grant not re-issued by B with its own lifetime: %+v", fresh[1])
	}

	// Without A's speak-as the chain itself does not link W→A.
	n.Grant(B, FacetRenew, grant)
	if _, _, rerr := renew(); rerr == nil {
		t.Fatal("chain without W→A speak-as accepted")
	}

	// A stranger wallet's speak-as to A links nothing: B answers for W only.
	stranger := newSigner(t)
	n.Grant(B, FacetRenew, grant, speakAs(t, stranger, A, []string{"member", "invoke"}, now-1, now+3600))
	if _, _, rerr := renew(); rerr == nil {
		t.Fatal("stranger's speak-as bridged A to B")
	}

	// B's speak-as covering invoke only: the chain still verifies (being
	// addressed as W is free) but B may not SIGN a member cert as W.
	n.Grant(B, FacetRenew, grant, saA)
	hubB.SpeakAs = []cert.Cert{speakAs(t, wallet, B, []string{"invoke"}, now-1, now+3600)}
	_, errs, rerr = renew()
	if rerr != nil {
		t.Fatalf("invoke-only speak-as: chain rejected: %v", rerr)
	}
	if errs[0] == nil || errs[0].Error() != RefuseNotMine {
		t.Fatalf("member under invoke-only speak-as: want %q, got %v", RefuseNotMine, errs[0])
	}
	if errs[1] != nil {
		t.Fatalf("invoke grant under invoke-only speak-as refused: %v", errs[1])
	}
	hubB.SpeakAs = []cert.Cert{saB}

	// A's speak-as expired: the chain no longer links W→A.
	w.clk.Advance(3601)
	if _, _, rerr := renew(); rerr == nil {
		t.Fatal("expired W→A speak-as accepted")
	}
}

// ---- 3. seq high-water mark -----------------------------------------------

func TestSequenceValidation(t *testing.T) {
	w := newWorld(t)
	now := w.clk.Now()
	b, bs, _ := w.actor("b")
	a, _, aep := w.actor("a")
	B, A := b.ID(), a.ID()
	b.AcceptTable["echo"] = echo
	consent := issue(t, bs, string(A), []cert.ActorID{B}, []string{"echo"}, false, now-1, now+3600)
	b.Consents = []cert.Cert{consent}
	w.start(b)
	w.start(a)

	for i := 1; i <= 3; i++ {
		if _, err := a.Send(w.ctx, B, "echo", []byte{byte(i)}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if got := b.HWM().Peek(A, B); got != int64(i) {
			t.Fatalf("hwm after %d = %d", i, got)
		}
	}

	mk := func(seq int64) envelope.Envelope {
		e, err := envelope.Sign(envelope.Envelope{
			To: envelope.Address{Target: B, Facet: "echo"}, Seq: seq, Payload: []byte("x"),
		}, a.Signer)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	// Replay of a spent number, at and below the mark.
	for _, seq := range []int64{3, 2, 0, -1} {
		if st, _ := rawSend(t, w.ctx, aep, mk(seq)); st.Code != StatusReplay {
			t.Fatalf("seq %d: want replay, got %s", seq, st.Code)
		}
	}
	// A gap is fine and moves the mark.
	if st, _ := rawSend(t, w.ctx, aep, mk(10)); st.Code != StatusOK {
		t.Fatalf("seq 10: %s %s", st.Code, st.Msg)
	}
	if st, _ := rawSend(t, w.ctx, aep, mk(4)); st.Code != StatusReplay {
		t.Fatalf("seq 4 after 10: want replay, got %s", st.Code)
	}
	// A's own counter (4) is now behind the numbers it burned: replay.
	_, err := a.Send(w.ctx, B, "echo", nil)
	wantRemote(t, err, StatusReplay)
	if got := b.HWM().Peek(A, B); got != 10 {
		t.Fatalf("hwm = %d, want 10", got)
	}
	// Per-(from,to) independence: another sender starts at 1.
	c, _, _ := w.actor("c")
	b.Consents = append(b.Consents, issue(t, bs, string(c.ID()), []cert.ActorID{B}, []string{"echo"}, false, now-1, now+3600))
	w.start(c)
	if _, err := c.Send(w.ctx, B, "echo", nil); err != nil {
		t.Fatal(err)
	}
	if got := b.HWM().Peek(c.ID(), B); got != 1 {
		t.Fatalf("hwm(c) = %d", got)
	}
}

// ---- 4. location piggyback + cache ----------------------------------------

func TestLocationCaching(t *testing.T) {
	w := newWorld(t)
	now := w.clk.Now()
	b, bs, _ := w.actor("b")
	a, _, _ := w.actor("a")
	c, cs, _ := w.actor("c")
	d, ds, dep := w.actor("d") // sends hand-crafted envelopes with bad locs
	B, A := b.ID(), a.ID()
	b.AcceptTable["echo"] = echo
	b.Consents = []cert.Cert{
		issue(t, bs, string(A), []cert.ActorID{B}, []string{"echo"}, false, now-1, now+3600),
		issue(t, bs, string(d.ID()), []cert.ActorID{B}, []string{"echo"}, false, now-1, now+3600),
	}
	if _, err := a.PublishLocation(3600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.PublishLocation(3600); err != nil {
		t.Fatal(err)
	}
	w.start(b)
	w.start(a)
	w.start(c)
	w.start(d)

	if b.GetLocation(A) != nil || a.GetLocation(B) != nil {
		t.Fatal("caches should start empty")
	}
	rep, err := a.Send(w.ctx, B, "echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Envelope carried A's record → B cached it; reply carried B's → A cached it.
	if loc := b.GetLocation(A); loc == nil || loc.Cav.Endpoints[0] != "mem:a" {
		t.Fatalf("B did not cache A's location: %+v", loc)
	}
	if loc := a.GetLocation(B); loc == nil || loc.Cav.Endpoints[0] != "mem:b" || rep.Loc == nil {
		t.Fatalf("A did not cache B's location: %+v", loc)
	}
	aLoc := *b.GetLocation(A)

	// Bad loc rejects the whole envelope (fail closed) and leaves the cache alone.
	var rawSeq int64
	badLoc := func(loc cert.Cert) Status {
		rawSeq++
		e, err := envelope.Sign(envelope.Envelope{
			To: envelope.Address{Target: B, Facet: "echo"}, Seq: rawSeq, Loc: &loc,
		}, ds)
		if err != nil {
			t.Fatal(err)
		}
		st, _ := rawSend(t, w.ctx, dep, e)
		return st
	}
	forged, _ := cert.Sign(cert.Cert{Aud: cert.AudAny, Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Endpoints: []string{"mem:c"}}, Iat: now, Exp: now + 3600}, cs) // issued by C, carried by D
	if st := badLoc(forged); st.Code != StatusBadLoc {
		t.Fatalf("forged loc: %s", st.Code)
	}
	stale, _ := cert.Sign(cert.Cert{Aud: cert.AudAny, Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Endpoints: []string{"mem:x"}}, Iat: now - 7200, Exp: now - 1}, ds)
	if st := badLoc(stale); st.Code != StatusBadLoc {
		t.Fatalf("stale loc: %s", st.Code)
	}
	if b.GetLocation(d.ID()) != nil {
		t.Fatal("a rejected loc must not be cached")
	}
	if got := *b.GetLocation(A); got.Iat != aLoc.Iat || got.Cav.Endpoints[0] != "mem:a" {
		t.Fatalf("cache changed by a rejected loc: %+v", got)
	}
	// The rejected envelopes still spent D's seq numbers (HWM rule).
	if b.HWM().Peek(d.ID(), B) != rawSeq {
		t.Fatalf("hwm(d) = %d", b.HWM().Peek(d.ID(), B))
	}

	// A newer record replaces the cached one on the next message.
	w.clk.Advance(10)
	if _, err := a.PublishLocation(3600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Send(w.ctx, B, "echo", nil); err != nil {
		t.Fatal(err)
	}
	if got := b.GetLocation(A); got.Iat != now+10 {
		t.Fatalf("newer record not cached: iat %d", got.Iat)
	}

	// Cached hints drive Dial: a record pointing at another actor's
	// endpoint is refused by the transport (peer mismatch), fail closed.
	lie, _ := cert.Sign(cert.Cert{Aud: cert.AudAny, Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Endpoints: []string{"mem:c"}}, Iat: now + 11, Exp: now + 3600}, bs)
	if err := a.UpdateLocation(B, &lie); err != nil {
		t.Fatal(err)
	}
	_, err = a.Send(w.ctx, B, "echo", nil)
	if !errors.Is(err, ErrPeerMismatch) {
		t.Fatalf("want ErrPeerMismatch, got %v", err)
	}
	// UpdateLocation applies the same rule locally.
	if err := a.UpdateLocation(B, &forged); !errors.Is(err, ErrBadLocation) {
		t.Fatalf("UpdateLocation accepted a foreign record: %v", err)
	}

	// Expiry under the effective clock evicts.
	w.clk.Advance(4000)
	if b.GetLocation(A) != nil {
		t.Fatal("expired record still served")
	}
	_ = c
}

// ---- 5. serial mailbox: handlers never overlap; bounded, drop on full ----

// senders binds n caller actors, each consented by b for facet, and
// starts them.
func (w *world) senders(b *Actor, bs cert.Signer, facet string, n int) []*Actor {
	w.t.Helper()
	now := w.clk.Now()
	out := make([]*Actor, n)
	for i := range out {
		a, _, _ := w.actor(fmt.Sprintf("s%d", i))
		b.Consents = append(b.Consents, issue(w.t, bs, string(a.ID()), []cert.ActorID{b.ID()}, []string{facet}, false, now-1, now+3600))
		out[i] = a
	}
	for _, a := range out {
		w.start(a)
	}
	return out
}

func TestSerialMailbox(t *testing.T) {
	w := newWorld(t)
	b, bs, _ := w.actor("b")
	B := b.ID()
	const senders, each = 8, 10
	callers := w.senders(b, bs, "count", senders)

	var count int // deliberately unsynchronised: -race proves seriality
	var inFlight atomic.Int32
	b.AcceptTable["count"] = func(context.Context, *Invocation) ([]byte, error) {
		if inFlight.Add(1) != 1 {
			t.Error("handler ran concurrently")
		}
		count++
		time.Sleep(200 * time.Microsecond)
		inFlight.Add(-1)
		return nil, nil
	}
	w.start(b)

	var wg sync.WaitGroup
	for _, a := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				if _, err := a.Send(w.ctx, B, "count", nil); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if count != senders*each {
		t.Fatalf("count = %d, want %d", count, senders*each)
	}
	for _, a := range callers {
		if got := b.HWM().Peek(a.ID(), B); got != each {
			t.Fatalf("hwm(%s) = %d, want %d", a.ID(), got, each)
		}
	}
}

// TestSendsPerEdgeAreOrdered pins the sender-side rule that makes the
// strict high-water mark workable: concurrent Sends from ONE actor to
// ONE receiver never overtake each other, so none is refused as replay.
func TestSendsPerEdgeAreOrdered(t *testing.T) {
	w := newWorld(t)
	now := w.clk.Now()
	b, bs, _ := w.actor("b")
	a, _, _ := w.actor("a")
	B, A := b.ID(), a.ID()
	b.Consents = []cert.Cert{issue(t, bs, string(A), []cert.ActorID{B}, []string{"echo"}, false, now-1, now+3600)}
	b.AcceptTable["echo"] = echo
	w.start(b)
	w.start(a)

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Send(w.ctx, B, "echo", nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := b.HWM().Peek(A, B); got != n {
		t.Fatalf("hwm = %d, want %d", got, n)
	}
}

func TestMailboxDropOnFull(t *testing.T) {
	w := newWorld(t)
	b, bs, _ := w.actor("b")
	B := b.ID()
	b.Mailbox = 1
	const n = 6
	callers := w.senders(b, bs, "slow", n+1)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	b.AcceptTable["slow"] = func(context.Context, *Invocation) ([]byte, error) {
		started <- struct{}{}
		<-release
		return []byte("done"), nil
	}
	w.start(b)

	type res struct {
		rep *Reply
		err error
	}
	results := make(chan res, 16)
	// #1 occupies the loop.
	go func() { r, err := callers[0].Send(w.ctx, B, "slow", nil); results <- res{r, err} }()
	<-started

	// N more from distinct senders: one fits in the mailbox (depth 1),
	// the rest are dropped — the sender sees a closed stream, no reply
	// (invariant 12).
	for _, a := range callers[1:] {
		go func() { r, err := a.Send(w.ctx, B, "slow", nil); results <- res{r, err} }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for b.Dropped() != n-1 {
		if time.Now().After(deadline) {
			t.Fatalf("dropped = %d, want %d", b.Dropped(), n-1)
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	<-started // #2 runs after #1

	ok, dropped := 0, 0
	for i := 0; i < n+1; i++ {
		r := <-results
		switch {
		case r.err == nil && string(r.rep.Payload) == "done":
			ok++
		case errors.Is(r.err, ErrNoReply):
			dropped++
		default:
			t.Fatalf("unexpected: %+v %v", r.rep, r.err)
		}
	}
	if ok != 2 || dropped != n-1 {
		t.Fatalf("ok=%d dropped=%d", ok, dropped)
	}
}

// ---- 6. cheap rejects on the transport goroutine ------------------------

func TestTransportGoroutineRejects(t *testing.T) {
	w := newWorld(t)
	b, _, _ := w.actor("b")
	a, as, aep := w.actor("a")
	c, _, _ := w.actor("c")
	w.start(b)

	// Wrong target: signed for C, delivered to B.
	e, _ := envelope.Sign(envelope.Envelope{To: envelope.Address{Target: c.ID(), Facet: "x"}, Seq: 1}, as)
	wire, _ := envelope.Encode(e)
	s, err := aep.Dial(w.ctx, b.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SendMsg(w.ctx, wire)
	raw, err := s.RecvMsg(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := envelope.DecodeReply(raw)
	if st, _ := DecodeStatus(rep.Payload); st.Code != StatusWrongTarget {
		t.Fatalf("want wrong-target, got %s", st.Code)
	}
	s.Close()

	// Tampered signature.
	e, _ = envelope.Sign(envelope.Envelope{To: envelope.Address{Target: b.ID(), Facet: "x"}, Seq: 1}, as)
	e.Payload = []byte("tampered")
	if st, _ := rawSend(t, w.ctx, aep, e); st.Code != StatusBadSig {
		t.Fatalf("want bad-sig, got %s", st.Code)
	}
	if b.HWM().Peek(a.ID(), b.ID()) != 0 {
		t.Fatal("cheap rejects must not touch the high-water mark")
	}

	// Undecodable bytes: stream closed, no reply.
	s, _ = aep.Dial(w.ctx, b.ID(), nil)
	_ = s.SendMsg(w.ctx, []byte("{not an envelope"))
	if _, err := s.RecvMsg(w.ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
	_ = a
}

// ---- 7. in-memory transport contract ----------------------------------

func TestMemoryTransport(t *testing.T) {
	ctx := context.Background()
	net := NewMemoryNetwork()
	x, y := newSigner(t), newSigner(t)
	ex, err := net.Bind(x.ActorID(), "x")
	if err != nil {
		t.Fatal(err)
	}
	ey, err := net.Bind(y.ActorID(), "y")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := net.Bind(x.ActorID(), "x2"); err == nil {
		t.Fatal("double bind of an id accepted")
	}
	if ex.Tag() != "mem:x" || ex.Endpoints()[0] != "mem:x" || ex.ID() != x.ActorID() {
		t.Fatalf("endpoint identity: %s %v", ex.Tag(), ex.Endpoints())
	}

	// Unknown actor, wrong hint, mismatched hint.
	if _, err := ex.Dial(ctx, newSigner(t).ActorID(), []string{"iroh:whatever", "mem:nope"}); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("want unreachable, got %v", err)
	}
	if _, err := ex.Dial(ctx, y.ActorID(), []string{"mem:x"}); !errors.Is(err, ErrPeerMismatch) {
		t.Fatalf("want peer mismatch, got %v", err)
	}

	// One message per direction, then EOF; peer identity on Accept.
	go func() {
		s, peer, err := ey.Accept(ctx)
		if err != nil || peer != x.ActorID() {
			t.Errorf("accept: %v peer=%s", err, peer)
			return
		}
		defer s.Close()
		msg, _ := s.RecvMsg(ctx)
		if _, err := s.RecvMsg(ctx); !errors.Is(err, io.EOF) {
			t.Errorf("second recv: want EOF, got %v", err)
		}
		_ = s.SendMsg(ctx, append([]byte("re:"), msg...))
		if err := s.SendMsg(ctx, []byte("again")); !errors.Is(err, ErrStreamFinished) {
			t.Errorf("second send: %v", err)
		}
	}()
	s, err := ex.Dial(ctx, y.ActorID(), []string{"mem:y"})
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

	// Close without sending FINs empty: peer sees EOF.
	go func() {
		s, _, _ := ey.Accept(ctx)
		s.Close()
	}()
	s, _ = ex.Dial(ctx, y.ActorID(), nil)
	if _, err := s.RecvMsg(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after peer close, got %v", err)
	}

	// Closed endpoint: Accept and Dial fail.
	ey.Close()
	if _, _, err := ey.Accept(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("accept on closed: %v", err)
	}
	if _, err := ex.Dial(ctx, y.ActorID(), nil); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("dial unbound: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := ex.Accept(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("accept ctx: %v", err)
	}
}
