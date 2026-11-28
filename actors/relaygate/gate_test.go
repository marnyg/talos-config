package relaygate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
)

// world is one relay R with one member P, on a clock the test moves.
type world struct {
	t      *testing.T
	now    int64
	relay  cert.EdSigner
	parent cert.EdSigner
	gate   *Gate
}

func newKey(t *testing.T) cert.EdSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return cert.NewEdSigner(priv)
}

// link signs one `relay` cert naming target, issued at iat.
func link(t *testing.T, iss cert.EdSigner, aud string, target cert.ActorID, cav cert.Caveats, iat, exp int64) cert.Cert {
	t.Helper()
	cav.Target = []cert.ActorID{target}
	cav.Facet = []string{Facet}
	c, err := cert.Sign(cert.Cert{Aud: aud, Can: cert.VerbRelay, Cav: cav, Iat: iat, Exp: exp}, iss)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// newWorld: R consents to P for a day, delegable. extra consents are
// held beside it.
func newWorld(t *testing.T, extra ...func(w *world) cert.Cert) *world {
	t.Helper()
	w := &world{t: t, now: 1_000_000, relay: newKey(t), parent: newKey(t)}
	consents := []cert.Cert{w.consent(w.parent.ActorID(), w.now+86_400)}
	for _, f := range extra {
		consents = append(consents, f(w))
	}
	// New judges members at construction: the clock must be set first.
	clock := func() int64 { return w.now }
	w.gate = newAt(w.relay.ActorID(), consents, clock)
	return w
}

func (w *world) consent(aud cert.ActorID, exp int64) cert.Cert {
	return link(w.t, w.relay, string(aud), w.relay.ActorID(), cert.Caveats{Delegable: true}, w.now-1, exp)
}

// toChild is P's link to a child key, valid ttl seconds from now.
func (w *world) toChild(child cert.ActorID, ttl int64) cert.Cert {
	return link(w.t, w.parent, string(child), w.relay.ActorID(), cert.Caveats{}, w.now, w.now+ttl)
}

// birth is P's wildcard link, as an intro carries it.
func (w *world) birth(bits int, ttl int64) cert.Cert {
	return link(w.t, w.parent, "*", w.relay.ActorID(), cert.Caveats{Postage: postage.Require(bits)}, w.now, w.now+ttl)
}

func (w *world) admitted(id cert.ActorID) bool {
	_, ok := w.gate.Admitted(id)
	return ok
}

func TestGateMemberPresentsNothing(t *testing.T) {
	w := newWorld(t)
	if !w.admitted(w.parent.ActorID()) {
		t.Fatal("member: not admitted")
	}
	if !w.admitted(w.relay.ActorID()) {
		t.Fatal("the relay's own actor: not admitted")
	}
	if w.admitted(newKey(t).ActorID()) {
		t.Fatal("stranger: admitted")
	}
	if w.gate.Members() != 1 {
		t.Fatalf("Members() = %d, want 1", w.gate.Members())
	}
	w.now += 86_400
	if w.admitted(w.parent.ActorID()) {
		t.Fatal("member after its consent expired: admitted")
	}
	if !w.admitted(w.relay.ActorID()) {
		t.Fatal("the relay's own actor does not expire")
	}
}

// A consent on another verb, to a group, or one the relay did not sign
// makes nobody a member.
func TestGateOnlyItsOwnRelayConsentsMakeMembers(t *testing.T) {
	invoked, forged := newKey(t), newKey(t)
	w := newWorld(t,
		func(w *world) cert.Cert {
			c, err := cert.Sign(cert.Cert{Aud: string(invoked.ActorID()), Can: cert.VerbInvoke,
				Cav: cert.Caveats{Target: []cert.ActorID{w.relay.ActorID()}, Facet: []string{Facet}},
				Iat: w.now - 1, Exp: w.now + 600}, w.relay)
			if err != nil {
				t.Fatal(err)
			}
			return c
		},
		func(w *world) cert.Cert {
			// signed by the parent, not the relay
			return link(t, w.parent, string(forged.ActorID()), w.relay.ActorID(), cert.Caveats{}, w.now-1, w.now+600)
		},
	)
	if w.admitted(invoked.ActorID()) {
		t.Fatal("an invoke consent admitted its aud to the relay")
	}
	if w.admitted(forged.ActorID()) {
		t.Fatal("a consent the relay did not sign made a member")
	}
	if w.gate.Members() != 1 {
		t.Fatalf("Members() = %d, want 1", w.gate.Members())
	}
}

func TestGateChild(t *testing.T) {
	w := newWorld(t)
	child, other := newKey(t), newKey(t)
	chain := []cert.Cert{w.toChild(child.ActorID(), 600)}

	if w.admitted(child.ActorID()) {
		t.Fatal("child before presenting: admitted")
	}
	exp, err := w.gate.Present(Presentation{ID: child.ActorID(), Chain: chain})
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if exp != w.now+600 {
		t.Fatalf("exp = %d, want the link's %d", exp, w.now+600)
	}
	if until, ok := w.gate.Admitted(child.ActorID()); !ok || until != exp {
		t.Fatalf("Admitted = %d, %v; want %d, true", until, ok, exp)
	}
	// The chain admits its own key only.
	if _, err := w.gate.Present(Presentation{ID: other.ActorID(), Chain: chain}); !errors.Is(err, ErrRefused) {
		t.Fatalf("child's chain for another key: err = %v, want ErrRefused", err)
	}
	if w.admitted(other.ActorID()) {
		t.Fatal("another key admitted on the child's chain")
	}
	w.now += 600
	if w.admitted(child.ActorID()) {
		t.Fatal("child at its expiry: admitted")
	}
	if _, err := w.gate.Present(Presentation{ID: child.ActorID(), Chain: chain}); !errors.Is(err, ErrRefused) {
		t.Fatalf("expired chain: err = %v, want ErrRefused", err)
	}
}

func TestGateNewborn(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	newborn, other := newKey(t), newKey(t)
	chain := []cert.Cert{w.birth(12, 120)}
	relay := w.relay.ActorID()

	if _, err := w.gate.Present(Presentation{ID: newborn.ActorID(), Chain: chain}); !errors.Is(err, ErrRefused) {
		t.Fatalf("no stamp: err = %v, want ErrRefused", err)
	}
	p, err := Prepare(ctx, nil, relay, newborn.ActorID(), chain)
	if err != nil {
		t.Fatal(err)
	}
	if p.Stamp == "" {
		t.Fatal("Prepare solved no stamp for a postage-bearing link")
	}
	// The stamp is for one key: the same link and stamp do not admit
	// another. (12 bits: a false pass is 1 in 4096.)
	if _, err := w.gate.Present(Presentation{ID: other.ActorID(), Chain: chain, Stamp: p.Stamp}); !errors.Is(err, ErrRefused) {
		t.Fatalf("newborn's stamp for another key: err = %v, want ErrRefused", err)
	}
	// Nor at another relay.
	elsewhere, err := Prepare(ctx, nil, newKey(t).ActorID(), newborn.ActorID(), chain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.gate.Present(elsewhere); !errors.Is(err, ErrRefused) {
		t.Fatalf("stamp solved for another relay: err = %v, want ErrRefused", err)
	}
	exp, err := w.gate.Present(p)
	if err != nil {
		t.Fatalf("newborn: %v", err)
	}
	if exp != w.now+120 {
		t.Fatalf("exp = %d, want the birth window's end %d", exp, w.now+120)
	}
	// A second newborn pays its own stamp on the same link.
	if p2, err := Prepare(ctx, nil, relay, other.ActorID(), chain); err != nil {
		t.Fatal(err)
	} else if _, err := w.gate.Present(p2); err != nil {
		t.Fatalf("second newborn on the same link: %v", err)
	}
	// Then the child swaps the bearer link for its own: admitted past
	// the birth window.
	kit := []cert.Cert{w.toChild(newborn.ActorID(), 900)}
	if _, err := w.gate.Present(Presentation{ID: newborn.ActorID(), Chain: kit}); err != nil {
		t.Fatalf("kit link: %v", err)
	}
	w.now += 121
	if !w.admitted(newborn.ActorID()) {
		t.Fatal("after the birth window, holding a kit link: not admitted")
	}
	if w.admitted(other.ActorID()) {
		t.Fatal("after the birth window, on the wildcard link alone: admitted")
	}
}

// A wildcard link without postage binds nobody (cert rule 3); Prepare
// has nothing to solve and the gate refuses.
func TestGateWildcardNeedsPostage(t *testing.T) {
	w := newWorld(t)
	free := link(t, w.parent, "*", w.relay.ActorID(), cert.Caveats{}, w.now, w.now+120)
	id := newKey(t).ActorID()
	p, err := Prepare(context.Background(), nil, w.relay.ActorID(), id, []cert.Cert{free})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.gate.Present(p); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}

func TestGateLaterPresentationNeverShortens(t *testing.T) {
	w := newWorld(t)
	child := newKey(t).ActorID()
	long := Presentation{ID: child, Chain: []cert.Cert{w.toChild(child, 600)}}
	short := Presentation{ID: child, Chain: []cert.Cert{w.toChild(child, 60)}}
	if _, err := w.gate.Present(long); err != nil {
		t.Fatal(err)
	}
	exp, err := w.gate.Present(short)
	if err != nil {
		t.Fatal(err)
	}
	if exp != w.now+600 {
		t.Fatalf("exp after a shorter presentation = %d, want %d", exp, w.now+600)
	}
}

func TestGateTableRefusesWhenFullNeverEvicts(t *testing.T) {
	w := newWorld(t)
	w.gate.MaxEntries = 2
	present := func(ttl int64) (cert.ActorID, error) {
		id := newKey(t).ActorID()
		_, err := w.gate.Present(Presentation{ID: id, Chain: []cert.Cert{w.toChild(id, ttl)}})
		return id, err
	}
	a, err := present(60)
	if err != nil {
		t.Fatal(err)
	}
	b, err := present(600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := present(600); !errors.Is(err, ErrFull) {
		t.Fatalf("third entry: err = %v, want ErrFull", err)
	}
	if !w.admitted(a) || !w.admitted(b) {
		t.Fatal("a full table pushed a live entry out")
	}
	// A held id renews in a full table.
	if _, err := w.gate.Present(Presentation{ID: b, Chain: []cert.Cert{w.toChild(b, 900)}}); err != nil {
		t.Fatalf("renewal in a full table: %v", err)
	}
	// Room appears only as entries expire.
	w.now += 61
	if _, err := present(600); err != nil {
		t.Fatalf("after one entry expired: %v", err)
	}
	if w.gate.Shown() != 2 {
		t.Fatalf("Shown() = %d, want 2", w.gate.Shown())
	}
}

// Protocol invariant 9, as far as the verifier takes it for this verb:
// a local clock rolled back past the gate's own consents resurrects
// nothing that had expired before they were issued. (It does NOT learn
// from presented links — see Gate.now.)
func TestGateClockRollback(t *testing.T) {
	w := newWorld(t) // consent iat = now-1
	stale := newKey(t).ActorID()
	old := []cert.Cert{link(t, w.parent, string(stale), w.relay.ActorID(), cert.Caveats{}, w.now-500, w.now-50)}

	w.now -= 100 // the machine's clock goes back to before `old` expired
	if _, err := w.gate.Present(Presentation{ID: stale, Chain: old}); !errors.Is(err, ErrRefused) {
		t.Fatalf("expired chain after a clock rollback: err = %v, want ErrRefused", err)
	}
	if !w.admitted(w.parent.ActorID()) {
		t.Fatal("member under a rolled-back clock: not admitted")
	}
}

func TestPresentationWire(t *testing.T) {
	w := newWorld(t)
	child := newKey(t).ActorID()
	in := Presentation{ID: child, Chain: []cert.Cert{w.toChild(child, 600)}, Stamp: "ab"}
	data, err := EncodePresentation(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodePresentation(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Stamp != in.Stamp || len(out.Chain) != 1 {
		t.Fatalf("round trip: %+v", out)
	}
	if _, err := w.gate.Present(out); err != nil {
		t.Fatalf("decoded presentation: %v", err)
	}

	many := Presentation{ID: child}
	for range MaxChain + 1 {
		many.Chain = append(many.Chain, in.Chain[0])
	}
	data, err = EncodePresentation(many)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePresentation(data); !errors.Is(err, ErrBadPresentation) {
		t.Fatalf("%d links: err = %v, want ErrBadPresentation", MaxChain+1, err)
	}
	if _, err := w.gate.Present(many); !errors.Is(err, ErrBadPresentation) {
		t.Fatalf("Present with %d links: err = %v, want ErrBadPresentation", MaxChain+1, err)
	}
	for _, bad := range []string{"", "{", `{"id":"ed:00","chain":["x"]}`} {
		if _, err := DecodePresentation([]byte(bad)); !errors.Is(err, ErrBadPresentation) {
			t.Fatalf("DecodePresentation(%q): err = %v, want ErrBadPresentation", bad, err)
		}
	}
	if _, err := w.gate.Present(Presentation{ID: "eth:0x0000000000000000000000000000000000000001"}); !errors.Is(err, ErrBadPresentation) {
		t.Fatalf("a non-ed id: err = %v, want ErrBadPresentation", err)
	}
}
