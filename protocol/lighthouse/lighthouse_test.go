package lighthouse_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/lighthouse"
	"github.com/marnyg/talos-config/protocol/postage"
)

const t0 int64 = 1_700_000_000
const hour int64 = 3600

type fakeClock struct{ now atomic.Int64 }

func (c *fakeClock) Now() int64      { return c.now.Load() }
func (c *fakeClock) Advance(d int64) { c.now.Add(d) }

type world struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc
	net    *actor.MemoryNetwork
	clk    *fakeClock
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clk := &fakeClock{}
	clk.now.Store(t0)
	return &world{t: t, ctx: ctx, cancel: cancel, net: actor.NewMemoryNetwork(), clk: clk}
}

func newSigner(t *testing.T) cert.EdSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return cert.NewEdSigner(priv)
}

func (w *world) actor(name string) (*actor.Actor, cert.EdSigner) {
	w.t.Helper()
	s := newSigner(w.t)
	ep, err := w.net.Bind(s.ActorID(), name)
	if err != nil {
		w.t.Fatal(err)
	}
	a := actor.New(s, ep)
	a.Clock = w.clk.Now
	return a, s
}

func (w *world) start(a *actor.Actor) {
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
	if _, err := a.PublishLocation(hour); err != nil {
		w.t.Fatal(err)
	}
}

// grant signs {iss: s, aud, can: verb, cav: {target, facet, delegable}}.
func grant(t *testing.T, s cert.Signer, verb cert.Verb, aud string, target cert.ActorID, facet string, delegable bool) cert.Cert {
	t.Helper()
	c, err := cert.Sign(cert.Cert{
		Aud: aud,
		Can: verb,
		Cav: cert.Caveats{Target: []cert.ActorID{target}, Facet: []string{facet}, Delegable: delegable},
		Iat: t0,
		Exp: t0 + 24*hour,
	}, s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func remoteCode(err error) string {
	var re *actor.RemoteError
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

// noStamp is a sender-side scheme that never pays.
type noStamp struct{}

func (noStamp) Solve(context.Context, string, []byte) (string, error) { return "", nil }
func (noStamp) Check(string, []byte, string) error                    { return errors.New("never") }

// network sets up: lighthouse L consenting publish and #lookup to
// founder F; F mints A and B publish-caps and lookup grants.
type network struct {
	w             *world
	L, A, B       *actor.Actor
	sF            cert.EdSigner
	sA, sB        cert.EdSigner
	fdA           cert.Cert // A's frontdoor consent
	lighthouse    *lighthouse.Lighthouse
	frontdoorHits atomic.Int64
}

func setup(t *testing.T) *network {
	w := newWorld(t)
	n := &network{w: w}
	n.L, _ = w.actor("L")
	n.sF = newSigner(t)
	n.A, n.sA = w.actor("A")
	n.B, n.sB = w.actor("B")
	lid := n.L.ID()

	// L founds nothing: it consents to F.
	n.L.Consents = []cert.Cert{
		grant(t, n.L.Signer, cert.VerbPublish, string(n.sF.ActorID()), lid, lighthouse.FacetPublish, true),
		grant(t, n.L.Signer, cert.VerbInvoke, string(n.sF.ActorID()), lid, lighthouse.FacetLookup, true),
	}
	n.lighthouse = lighthouse.New(n.L)

	// F's network bundle for A and B: publish-cap + lookup grant.
	for _, m := range []struct {
		a *actor.Actor
		s cert.EdSigner
	}{{n.A, n.sA}, {n.B, n.sB}} {
		m.a.Grant(lid, lighthouse.FacetPublish, grant(t, n.sF, cert.VerbPublish, string(m.s.ActorID()), lid, lighthouse.FacetPublish, false))
		m.a.Grant(lid, lighthouse.FacetLookup, grant(t, n.sF, cert.VerbInvoke, string(m.s.ActorID()), lid, lighthouse.FacetLookup, false))
	}

	// A opens a frontdoor at 8 bits of work.
	fd, err := n.A.Frontdoor(postage.Require(8), 24*hour)
	if err != nil {
		t.Fatal(err)
	}
	n.fdA = fd
	n.A.Consents = []cert.Cert{fd}
	n.A.AcceptTable[actor.FacetFrontdoor] = func(_ context.Context, inv *actor.Invocation) ([]byte, error) {
		n.frontdoorHits.Add(1)
		return inv.Envelope.Payload, nil
	}

	w.start(n.L)
	w.start(n.A)
	w.start(n.B)
	return n
}

func TestPublishLookupFrontdoor(t *testing.T) {
	n := setup(t)
	ctx := n.w.ctx

	// A publishes (location piggybacks; frontdoor rides the payload).
	if err := lighthouse.Publish(ctx, n.A, n.L.ID(), &n.fdA); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// B, who has never met A, looks A up.
	recs, err := lighthouse.Lookup(ctx, n.B, n.L.ID(), n.A.ID())
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	rec, ok := recs[n.A.ID()]
	if !ok {
		t.Fatalf("A not in lookup: %v", recs)
	}
	if got := rec.Loc.Cav.Endpoints; len(got) != 1 || got[0] != actor.MemTag+"A" {
		t.Fatalf("A's endpoints: %v", got)
	}
	if rec.Frontdoor == nil || rec.Frontdoor.Cav.Postage != postage.Require(8) {
		t.Fatalf("A's frontdoor: %+v", rec.Frontdoor)
	}
	if n.B.GetLocation(n.A.ID()) == nil {
		t.Fatal("lookup did not cache A's location in B")
	}

	// B adopts A's frontdoor as its "grant": the receiver-signed link
	// is dropped from the proof, its postage is paid.
	n.B.Grant(n.A.ID(), actor.FacetFrontdoor, *rec.Frontdoor)
	rep, err := n.B.Send(ctx, n.A.ID(), actor.FacetFrontdoor, []byte("hello stranger"))
	if err != nil {
		t.Fatalf("frontdoor send: %v", err)
	}
	if string(rep.Payload) != "hello stranger" || n.frontdoorHits.Load() != 1 {
		t.Fatalf("frontdoor reply %q hits %d", rep.Payload, n.frontdoorHits.Load())
	}

	// Unpaid: same chain, no stamp → postage, handler untouched.
	n.B.Postage = noStamp{}
	_, err = n.B.Send(ctx, n.A.ID(), actor.FacetFrontdoor, []byte("free?"))
	if remoteCode(err) != actor.StatusPostage {
		t.Fatalf("unstamped: want %s, got %v", actor.StatusPostage, err)
	}
	if n.frontdoorHits.Load() != 1 {
		t.Fatal("unstamped envelope reached the handler")
	}
	n.B.Postage = nil

	// Stamped but for another facet A never opened: no chain roots it.
	_, err = n.B.Send(ctx, n.A.ID(), "#renew", []byte("{}"))
	if remoteCode(err) != actor.StatusUnauthorized {
		t.Fatalf("other facet: want unauthorized, got %v", err)
	}
}

func TestPublishNeedsPublishCap(t *testing.T) {
	n := setup(t)
	ctx := n.w.ctx
	S, sS := n.w.actor("S")
	n.w.start(S)

	// No cap at all.
	if err := lighthouse.Publish(ctx, S, n.L.ID(), nil); remoteCode(err) != actor.StatusUnauthorized {
		t.Fatalf("stranger publish: want unauthorized, got %v", err)
	}
	// An INVOKE grant to #publish signed by F does not root: #publish
	// binds verb publish and the consent it prepends carries it.
	S.Grant(n.L.ID(), lighthouse.FacetPublish, grant(t, n.sF, cert.VerbInvoke, string(sS.ActorID()), n.L.ID(), lighthouse.FacetPublish, false))
	if err := lighthouse.Publish(ctx, S, n.L.ID(), nil); remoteCode(err) != actor.StatusUnauthorized {
		t.Fatalf("invoke-verb publish: want unauthorized, got %v", err)
	}
	// Having talked to L (and been refused) does not put S in the
	// directory: the directory is what was PUBLISHED, not what was seen.
	if recs := n.lighthouse.Records(); len(recs) != 0 {
		t.Fatalf("directory after refused publishes: %v", recs)
	}
	// Lookup without a grant is refused too.
	if _, err := lighthouse.Lookup(ctx, S, n.L.ID(), n.A.ID()); remoteCode(err) != actor.StatusUnauthorized {
		t.Fatalf("stranger lookup: want unauthorized, got %v", err)
	}
}

func TestPublishRejectsForeignAndMalformedRecords(t *testing.T) {
	n := setup(t)
	ctx := n.w.ctx

	// A publishing B's frontdoor: the record must be the signer's.
	fdB, err := n.B.Frontdoor(postage.Require(8), hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := lighthouse.Publish(ctx, n.A, n.L.ID(), &fdB); remoteCode(err) != actor.StatusError {
		t.Fatalf("foreign frontdoor: want error, got %v", err)
	}
	// A frontdoor without postage never exists (the mint refuses)...
	if _, err := n.A.Frontdoor("", hour); !errors.Is(err, actor.ErrNoPostage) {
		t.Fatalf("mint without postage: %v", err)
	}
	// ...and a hand-made one is refused at publish.
	bad, err := cert.Sign(cert.Cert{
		Aud: cert.AudAny, Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{n.A.ID()}, Facet: []string{actor.FacetFrontdoor}},
		Iat: t0, Exp: t0 + hour,
	}, n.sA)
	if err != nil {
		t.Fatal(err)
	}
	if err := lighthouse.Publish(ctx, n.A, n.L.ID(), &bad); remoteCode(err) != actor.StatusError {
		t.Fatalf("postage-less frontdoor: want error, got %v", err)
	}
	if len(n.lighthouse.Records()) != 0 {
		t.Fatal("a refused publish left a record")
	}
}

func TestRecordsExpire(t *testing.T) {
	n := setup(t)
	ctx := n.w.ctx
	if err := lighthouse.Publish(ctx, n.A, n.L.ID(), &n.fdA); err != nil {
		t.Fatal(err)
	}
	if len(n.lighthouse.Records()) != 1 {
		t.Fatal("A not recorded")
	}
	// A's location was published for one hour; past it, A is gone
	// from the directory (revocation is expiry, invariant 6).
	n.w.clk.Advance(hour + 1) // nobody re-published; expired own records are simply not piggybacked
	recs, err := lighthouse.Lookup(ctx, n.B, n.L.ID(), n.A.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("expired record served: %v", recs)
	}
	// A re-publishes on its beat with a fresh record and is back.
	if _, err := n.A.PublishLocation(hour); err != nil {
		t.Fatal(err)
	}
	if err := lighthouse.Publish(ctx, n.A, n.L.ID(), &n.fdA); err != nil {
		t.Fatal(err)
	}
	if recs, _ = lighthouse.Lookup(ctx, n.B, n.L.ID(), n.A.ID()); len(recs) != 1 {
		t.Fatalf("re-published record missing: %v", recs)
	}
}

func TestDirectoryCapRefusesNewPublishersOnly(t *testing.T) {
	n := setup(t)
	ctx := n.w.ctx
	n.lighthouse.MaxRecords = 1

	if err := lighthouse.Publish(ctx, n.A, n.L.ID(), &n.fdA); err != nil {
		t.Fatal(err)
	}
	// Full: B, a new publisher with a valid cap, is refused — no live
	// member is evicted to make room.
	err := lighthouse.Publish(ctx, n.B, n.L.ID(), nil)
	if remoteCode(err) != actor.StatusError || !strings.Contains(err.Error(), lighthouse.ErrDirectoryFull.Error()) {
		t.Fatalf("new publisher on a full directory: want %v, got %v", lighthouse.ErrDirectoryFull, err)
	}
	if recs := n.lighthouse.Records(); len(recs) != 1 || recs[n.A.ID()].Loc.Iss != n.A.ID() {
		t.Fatalf("directory after refused publish: %v", recs)
	}
	// A, already listed, re-publishes freely.
	n.w.clk.Advance(1)
	if _, err := n.A.PublishLocation(hour); err != nil {
		t.Fatal(err)
	}
	if err := lighthouse.Publish(ctx, n.A, n.L.ID(), &n.fdA); err != nil {
		t.Fatalf("re-publish by a listed member: %v", err)
	}
	// Once A's record expires its slot is free: B gets in.
	n.w.clk.Advance(hour + 1)
	if _, err := n.B.PublishLocation(hour); err != nil {
		t.Fatal(err)
	}
	if err := lighthouse.Publish(ctx, n.B, n.L.ID(), nil); err != nil {
		t.Fatalf("publish after expiry freed a slot: %v", err)
	}
	if recs := n.lighthouse.Records(); len(recs) != 1 || recs[n.B.ID()].Loc.Iss != n.B.ID() {
		t.Fatalf("directory after expiry: %v", recs)
	}
}

func TestFrontdoorMintRefusesNonPositiveTTL(t *testing.T) {
	n := setup(t)
	for _, ttl := range []int64{0, -1} {
		if _, err := n.A.Frontdoor(postage.DefaultRequire, ttl); !errors.Is(err, actor.ErrBadTTL) {
			t.Fatalf("ttl %d: want %v, got %v", ttl, actor.ErrBadTTL, err)
		}
	}
}

// An unstamped envelope to a facet whose every consent demands postage
// is refused before the mailbox (talos-config-jjti): the verdict is the
// same `postage`, but the loop never sees it — the sender's seq mark
// stays untouched. Once ANY consent to the facet admits someone for
// free, the shortcut is off and the fold decides as before.
func TestUnstampedIsRefusedBeforeTheMailbox(t *testing.T) {
	n := setup(t)
	ctx := n.w.ctx
	S, _ := n.w.actor("S")
	n.w.start(S)
	S.Grant(n.A.ID(), actor.FacetFrontdoor, n.fdA)
	S.UpdateLocation(n.A.ID(), n.A.CurrentLocation())
	S.Postage = noStamp{}

	// Frontdoor-only facet: refused in the transport goroutine.
	_, err := S.Send(ctx, n.A.ID(), actor.FacetFrontdoor, []byte("free?"))
	if remoteCode(err) != actor.StatusPostage {
		t.Fatalf("unstamped stranger: want %s, got %v", actor.StatusPostage, err)
	}
	if mark := n.A.HWM().Peek(S.ID(), n.A.ID()); mark != 0 {
		t.Fatalf("unstamped envelope reached the loop (mark %d)", mark)
	}
	if n.frontdoorHits.Load() != 0 {
		t.Fatal("unstamped envelope reached the handler")
	}

	// A also lets B in for free on the same facet: the facet is no
	// longer postage-only, so the stranger's unstamped envelope now
	// takes the full path — same verdict, mark advanced — and B's free
	// path works.
	free := grant(t, n.A.Signer, cert.VerbInvoke, string(n.B.ID()), n.A.ID(), actor.FacetFrontdoor, false)
	n.A.Hold(append(n.A.Consents, free), nil)
	_, err = S.Send(ctx, n.A.ID(), actor.FacetFrontdoor, []byte("free now?"))
	if remoteCode(err) != actor.StatusPostage {
		t.Fatalf("unstamped stranger on a mixed facet: want %s, got %v", actor.StatusPostage, err)
	}
	if n.A.HWM().Peek(S.ID(), n.A.ID()) == 0 {
		t.Fatal("mixed facet: the fold should have decided, not the shortcut")
	}
	n.B.Grant(n.A.ID(), actor.FacetFrontdoor, free)
	n.B.UpdateLocation(n.A.ID(), n.A.CurrentLocation())
	n.B.Postage = noStamp{}
	if _, err := n.B.Send(ctx, n.A.ID(), actor.FacetFrontdoor, []byte("member")); err != nil || n.frontdoorHits.Load() != 1 {
		t.Fatalf("free member path: err %v hits %d", err, n.frontdoorHits.Load())
	}
}
