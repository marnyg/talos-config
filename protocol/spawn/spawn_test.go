package spawn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
	"github.com/marnyg/talos-config/protocol/spawn"
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

func (w *world) actor(name string) *actor.Actor {
	w.t.Helper()
	s := newSigner(w.t)
	ep, err := w.net.Bind(s.ActorID(), name)
	if err != nil {
		w.t.Fatal(err)
	}
	a := actor.New(s, ep)
	a.Clock = w.clk.Now
	return a
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

// consent signs {iss: s, aud, invoke, cav: {target: [s], facet}}.
func consent(t *testing.T, a *actor.Actor, aud cert.ActorID, facet string) cert.Cert {
	t.Helper()
	c, err := cert.Sign(cert.Cert{
		Aud: string(aud),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{a.ID()}, Facet: []string{facet}, Delegable: true},
		Iat: t0,
		Exp: t0 + 24*hour,
	}, a.Signer)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func remoteErr(t *testing.T, err error) *actor.RemoteError {
	t.Helper()
	var re *actor.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("want *RemoteError, got %v", err)
	}
	return re
}

// hatch is one child's outcome as the fake driver saw it.
type hatch struct {
	a   *actor.Actor
	kit spawn.Kit
	err error
}

// fakeDriver is the M4.1 stand-in for a provisioner: an actor with a
// #spawn handler that "starts the container" by running a child actor
// in-process — a new key, a bound endpoint, a published location,
// Born(intro) — and answers {lease}. It reads the params only to hand
// them to the child, as a real provisioner would via env.
type fakeDriver struct {
	w       *world
	a       *actor.Actor
	leases  atomic.Int64
	hatched chan hatch
	// facets the child consents to its parent over; refuse makes
	// #spawn fail instead of starting anything.
	facets []string
	refuse bool
	// born, when set, replaces the child's Born call (a driver that
	// runs something other than an honest child).
	born func(child *actor.Actor, in spawn.Intro) (spawn.Kit, error)
}

func newFakeDriver(w *world, parent cert.ActorID) *fakeDriver {
	d := &fakeDriver{w: w, a: w.actor("prov"), hatched: make(chan hatch, 8), facets: []string{"app"}}
	d.a.Consents = []cert.Cert{consent(w.t, d.a, parent, spawn.FacetSpawn)}
	d.a.AcceptTable[spawn.FacetSpawn] = d.spawn
	return d
}

func (d *fakeDriver) spawn(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	if d.refuse {
		return nil, errors.New("no capacity")
	}
	var req spawn.SpawnRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(req.Image, "img@sha256:") {
		return nil, errors.New("image not by digest")
	}
	n := d.leases.Add(1)
	go d.run(req.Params)
	return json.Marshal(spawn.SpawnReply{Lease: "lease-" + string(rune('0'+n))})
}

func (d *fakeDriver) run(params []byte) {
	in, err := spawn.DecodeIntro(params)
	if err != nil {
		d.hatched <- hatch{err: err}
		return
	}
	child := d.w.actor("child-" + in.Nonce[:6])
	child.Postage = postage.Default
	d.w.start(child)
	var kit spawn.Kit
	if d.born != nil {
		kit, err = d.born(child, in)
	} else {
		kit, err = spawn.Born(d.w.ctx, child, in, d.facets, hour)
	}
	d.hatched <- hatch{a: child, kit: kit, err: err}
}

func (d *fakeDriver) wait(t *testing.T) hatch {
	t.Helper()
	select {
	case h := <-d.hatched:
		return h
	case <-time.After(5 * time.Second):
		t.Fatal("child did not hatch")
		return hatch{}
	}
}

type rig struct {
	w      *world
	parent *actor.Actor
	sp     *spawn.Spawner
	drv    *fakeDriver
	spec   spawn.Spec
}

func setup(t *testing.T) *rig {
	w := newWorld(t)
	p := w.actor("parent")
	sp := spawn.New(p)
	sp.Postage = postage.Require(8)
	sp.Window = 10 * 60
	drv := newFakeDriver(w, p.ID())
	sp.Provisioner = drv.a.ID()
	// The parent knows where its provisioner is (a configured
	// correspondent); the chain to #spawn is the provisioner's consent,
	// presented empty.
	w.start(drv.a)
	w.start(p)
	if err := p.UpdateLocation(drv.a.ID(), drv.a.CurrentLocation()); err != nil {
		t.Fatal(err)
	}
	return &rig{w: w, parent: p, sp: sp, drv: drv, spec: spawn.Spec{Image: "img@sha256:abc"}}
}

func (r *rig) waitPromise(t *testing.T, pr *spawn.Promise) spawn.Birth {
	t.Helper()
	ctx, cancel := context.WithTimeout(r.w.ctx, 5*time.Second)
	defer cancel()
	b, err := pr.Wait(ctx)
	if err != nil {
		t.Fatalf("promise: %v", err)
	}
	return b
}

// TestSpawnBirth is ADR-0008's happy path: #spawn → child boots →
// stamped #birth {nonce} → kit → promise {id, location, lease}. Then
// the kit is proven usable: the child renews its mandated chain at
// P#renew, and P calls the child's app facet with an empty chain under
// the consent the child minted itself.
func TestSpawnBirth(t *testing.T) {
	r := setup(t)
	pr, err := r.sp.Spawn(r.w.ctx, r.spec)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	h := r.drv.wait(t)
	if h.err != nil {
		t.Fatalf("born: %v", h.err)
	}
	b := r.waitPromise(t, pr)
	if b.ID != h.a.ID() {
		t.Fatalf("promise id %s, want child %s", b.ID, h.a.ID())
	}
	if b.Lease.Provisioner != r.drv.a.ID() || b.Lease.ID != "lease-1" {
		t.Fatalf("lease %+v", b.Lease)
	}
	if b.Location == nil || b.Location.Iss != b.ID {
		t.Fatalf("promise location %+v", b.Location)
	}
	if got := r.parent.GetLocation(b.ID); got == nil {
		t.Fatal("parent did not cache the child's piggybacked location")
	}
	if got := h.a.GetLocation(r.parent.ID()); got == nil {
		t.Fatal("child did not cache the parent's location from the intro")
	}
	if r.sp.Pending() != 1 {
		t.Fatalf("pending after birth = %d, want 1 (kept for re-knock until the window)", r.sp.Pending())
	}

	// The kit mandates exactly the #renew chain.
	if len(h.kit.Grants) != 1 || len(h.kit.Grants[0]) != 1 {
		t.Fatalf("kit grants %+v", h.kit.Grants)
	}
	renew := h.kit.Grants[0][0]
	if renew.Iss != r.parent.ID() || renew.Aud != string(b.ID) || renew.Cav.Delegable ||
		renew.Exp != t0+r.sp.Window {
		t.Fatalf("renew chain %+v", renew)
	}
	if _, ok := h.a.Grants[actor.GrantKey{Target: r.parent.ID(), Facet: actor.FacetRenew}]; !ok {
		t.Fatal("child did not install the renew chain")
	}

	// One beat: the child renews its chain at the parent. The clock
	// moves first — under a frozen clock the re-issue is byte-identical
	// to the kit cert and proves nothing.
	r.w.clk.Advance(r.sp.Window / 4)
	payload, _ := actor.EncodeRenewRequest([]cert.Cert{renew}, nil)
	rep, err := h.a.Send(r.w.ctx, r.parent.ID(), actor.FacetRenew, payload)
	if err != nil {
		t.Fatalf("child renew: %v", err)
	}
	fresh, errs, err := actor.DecodeRenewResponse(rep.Payload)
	if err != nil || errs[0] != nil || fresh[0].Aud != string(b.ID) {
		t.Fatalf("renew result: %v %v %+v", err, errs, fresh)
	}
	if string(fresh[0].Sig) == string(renew.Sig) {
		t.Fatal("test premise: the renewed cert differs from the kit cert")
	}
	// Second beat on the FRESH cert (6sax): the kit chain is P's own
	// consent, and P re-installed it as it re-issued it.
	h.a.Grant(r.parent.ID(), actor.FacetRenew, fresh[0])
	r.w.clk.Advance(r.sp.Window / 4)
	payload, _ = actor.EncodeRenewRequest([]cert.Cert{fresh[0]}, nil)
	if _, err := h.a.Send(r.w.ctx, r.parent.ID(), actor.FacetRenew, payload); err != nil {
		t.Fatalf("child's second beat: %v", err)
	}

	// P→C authority is the child's own consent: the parent sends with
	// an empty chain.
	h.a.AcceptTable["app"] = func(_ context.Context, inv *actor.Invocation) ([]byte, error) {
		return []byte(`"hi ` + string(inv.From) + `"`), nil
	}
	rep, err = r.parent.Send(r.w.ctx, b.ID, "app", []byte(`{}`))
	if err != nil {
		t.Fatalf("parent → child app: %v", err)
	}
	if !strings.Contains(string(rep.Payload), string(r.parent.ID())) {
		t.Fatalf("app reply %s", rep.Payload)
	}
	// ...and a facet the child did not consent to is refused.
	_, err = r.parent.Send(r.w.ctx, b.ID, "other", []byte(`{}`))
	if remoteErr(t, err).Code != actor.StatusUnauthorized {
		t.Fatalf("unconsented facet: %v", err)
	}
}

// TestNonceBindsToFirstKey: the same key re-knocking gets the kit
// re-issued verbatim; another key presenting the nonce is refused; an
// unknown nonce is refused; an unstamped knock never reaches the
// mailbox.
func TestNonceBindsToFirstKey(t *testing.T) {
	r := setup(t)
	var intro spawn.Intro
	got := make(chan struct{})
	r.drv.born = func(child *actor.Actor, in spawn.Intro) (spawn.Kit, error) {
		intro = in
		close(got)
		return spawn.Born(r.w.ctx, child, in, nil, 0)
	}
	pr, err := r.sp.Spawn(r.w.ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	h := r.drv.wait(t)
	if h.err != nil {
		t.Fatal(h.err)
	}
	<-got
	b := r.waitPromise(t, pr)

	knock := func(a *actor.Actor, nonce string) (*actor.Reply, error) {
		a.Grant(r.parent.ID(), spawn.FacetBirth, intro.Consent)
		payload, _ := json.Marshal(spawn.BirthRequest{Nonce: nonce})
		return a.Send(r.w.ctx, r.parent.ID(), spawn.FacetBirth, payload)
	}

	// Same key: idempotent, byte-identical kit.
	first, _ := spawn.EncodeKit(h.kit)
	rep, err := knock(h.a, intro.Nonce)
	if err != nil {
		t.Fatalf("re-knock: %v", err)
	}
	if string(rep.Payload) != string(first) {
		t.Fatalf("re-knock kit differs:\n%s\n%s", rep.Payload, first)
	}
	if r.parent.GetLocation(b.ID) == nil {
		t.Fatal("child location lost")
	}

	// Another key with the same nonce inside the window.
	imp := r.w.actor("impostor")
	r.w.start(imp)
	_, err = knock(imp, intro.Nonce)
	if re := remoteErr(t, err); re.Code != actor.StatusError || re.Msg != spawn.RefuseNonceBound {
		t.Fatalf("impostor: %v", err)
	}
	// Unknown nonce under a valid consent.
	_, err = knock(imp, "deadbeef")
	if re := remoteErr(t, err); re.Code != actor.StatusError || re.Msg != spawn.RefuseUnknownNonce {
		t.Fatalf("unknown nonce: %v", err)
	}
	// A second spawn's nonce presented under the FIRST spawn's consent:
	// every live birth consent grants the same thing (the right to
	// knock); the nonce alone selects the spawn. Under a frozen clock
	// the two consents are even byte-identical — there is no per-spawn
	// caveat by design.
	got2 := make(chan spawn.Intro, 1)
	r.drv.born = func(_ *actor.Actor, in spawn.Intro) (spawn.Kit, error) {
		got2 <- in
		return spawn.Kit{}, errors.New("held back")
	}
	pr2, err := r.sp.Spawn(r.w.ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	h2 := r.drv.wait(t)
	intro2 := <-got2
	if string(intro2.Consent.Sig) != string(intro.Consent.Sig) {
		t.Fatal("test premise: same-second birth consents are identical")
	}
	if _, err = knock(h2.a, intro2.Nonce); err != nil { // knock installs spawn 1's consent
		t.Fatalf("nonce 2 under consent 1: %v", err)
	}
	if b2 := r.waitPromise(t, pr2); b2.ID != h2.a.ID() {
		t.Fatalf("spawn 2 bound to %s, want %s", b2.ID, h2.a.ID())
	}

	// Unstamped: refused in the transport goroutine (StatusPostage).
	imp.Postage = noStamp{}
	_, err = knock(imp, intro.Nonce)
	if remoteErr(t, err).Code != actor.StatusPostage {
		t.Fatalf("unstamped: %v", err)
	}
}

// noStamp never pays.
type noStamp struct{}

func (noStamp) Solve(context.Context, string, []byte) (string, error) { return "", nil }
func (noStamp) Check(string, []byte, string) error                    { return errors.New("never") }

// TestBirthWindow: a child that never knocks. Sweep at the window fails
// the promise with ErrBirthWindow, drops the record and the consent; a
// late knock finds no root.
func TestBirthWindow(t *testing.T) {
	r := setup(t)
	var intro spawn.Intro
	r.drv.born = func(_ *actor.Actor, in spawn.Intro) (spawn.Kit, error) {
		intro = in
		return spawn.Kit{}, errors.New("stalled")
	}
	pr, err := r.sp.Spawn(r.w.ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	h := r.drv.wait(t)
	consents, _ := r.parent.Authority()
	if len(consents) != 1 || consents[0].Cav.Facet[0] != spawn.FacetBirth {
		t.Fatalf("birth consent not installed: %+v", consents)
	}
	select {
	case <-pr.Done():
		t.Fatal("promise resolved without a birth")
	default:
	}

	r.w.clk.Advance(r.sp.Window)
	r.sp.Sweep()
	if _, err := pr.Wait(r.w.ctx); !errors.Is(err, spawn.ErrBirthWindow) {
		t.Fatalf("promise after window: %v", err)
	}
	if r.sp.Pending() != 0 {
		t.Fatalf("pending after sweep = %d", r.sp.Pending())
	}
	if consents, _ := r.parent.Authority(); len(consents) != 0 {
		t.Fatalf("birth consent not dropped: %+v", consents)
	}

	// The late child: its consent has expired — nothing roots.
	h.a.Grant(r.parent.ID(), spawn.FacetBirth, intro.Consent)
	payload, _ := json.Marshal(spawn.BirthRequest{Nonce: intro.Nonce})
	_, err = h.a.Send(r.w.ctx, r.parent.ID(), spawn.FacetBirth, payload)
	if remoteErr(t, err).Code != actor.StatusUnauthorized {
		t.Fatalf("late knock: %v", err)
	}
}

// TestSpawnRefused: the provisioner says no — Spawn returns the
// refusal, the record and consent are gone. A twin spawn from the same
// second holds a byte-identical consent; the refusal must not unroot it.
func TestSpawnRefused(t *testing.T) {
	r := setup(t)
	r.drv.born = func(_ *actor.Actor, in spawn.Intro) (spawn.Kit, error) { return spawn.Kit{}, errors.New("held") }
	twin, err := r.sp.Spawn(r.w.ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	r.drv.wait(t)

	r.drv.refuse = true
	_, err = r.sp.Spawn(r.w.ctx, r.spec)
	if re := remoteErr(t, err); re.Code != actor.StatusError || re.Msg != "no capacity" {
		t.Fatalf("refusal: %v", err)
	}
	if r.sp.Pending() != 1 {
		t.Fatalf("pending = %d, want the twin only", r.sp.Pending())
	}
	if consents, _ := r.parent.Authority(); len(consents) != 1 {
		t.Fatalf("twin's consent not kept: %+v", consents)
	}
	select {
	case <-twin.Done():
		t.Fatal("twin promise settled by the sibling's refusal")
	default:
	}

	// The twin lapses at the window like any other; then the table and
	// the consents are empty.
	r.w.clk.Advance(r.sp.Window)
	r.sp.Sweep()
	if _, err := twin.Wait(r.w.ctx); !errors.Is(err, spawn.ErrBirthWindow) {
		t.Fatalf("twin: %v", err)
	}
	if consents, _ := r.parent.Authority(); len(consents) != 0 || r.sp.Pending() != 0 {
		t.Fatalf("after sweep: %d pending, consents %+v", r.sp.Pending(), consents)
	}
	// No location, no provisioner: refused before anything is minted.
	r.sp.Provisioner = ""
	if _, err := r.sp.Spawn(r.w.ctx, r.spec); !errors.Is(err, spawn.ErrNoProvisioner) {
		t.Fatalf("no provisioner: %v", err)
	}
	q := r.w.actor("quiet")
	if _, err := spawn.New(q).Spawn(r.w.ctx, spawn.Spec{Provisioner: r.drv.a.ID()}); !errors.Is(err, spawn.ErrNoLocation) {
		t.Fatalf("no location: %v", err)
	}
}

// TestOutfit: the parent's choice beyond the mandate rides in the kit
// and is installed under every (target, facet) its last link names,
// with the locations of the actors it names.
func TestOutfit(t *testing.T) {
	r := setup(t)
	sib := r.w.actor("sibling")
	r.w.start(sib)
	// The parent holds a delegable consent from the sibling and
	// forwards a link to the child.
	sibConsent := consent(t, sib, r.parent.ID(), "work")
	sib.Consents = []cert.Cert{sibConsent}
	r.spec.Outfit = func(child cert.ActorID, now int64) (spawn.Kit, error) {
		link, err := cert.Sign(cert.Cert{
			Aud: string(child),
			Can: cert.VerbInvoke,
			Cav: cert.Caveats{Target: []cert.ActorID{sib.ID()}, Facet: []string{"work"}},
			Iat: now, Exp: now + hour,
		}, r.parent.Signer)
		if err != nil {
			return spawn.Kit{}, err
		}
		return spawn.Kit{Grants: [][]cert.Cert{{sibConsent, link}}, Locations: []cert.Cert{*sib.CurrentLocation()}}, nil
	}
	pr, err := r.sp.Spawn(r.w.ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	h := r.drv.wait(t)
	if h.err != nil {
		t.Fatal(h.err)
	}
	r.waitPromise(t, pr)
	if len(h.kit.Grants) != 2 || len(h.kit.Locations) != 1 {
		t.Fatalf("kit %+v", h.kit)
	}
	sib.AcceptTable["work"] = func(context.Context, *actor.Invocation) ([]byte, error) { return []byte(`1`), nil }
	if _, err := h.a.Send(r.w.ctx, sib.ID(), "work", nil); err != nil {
		t.Fatalf("child → sibling on the outfitted chain: %v", err)
	}
}

// TestCheckIntroAndKit pins the two shape rules the child applies.
func TestCheckIntroAndKit(t *testing.T) {
	r := setup(t)
	p := r.parent
	good, err := r.sp.BirthConsent(60)
	if err != nil {
		t.Fatal(err)
	}
	in := spawn.Intro{Parent: p.ID(), Location: *p.CurrentLocation(), Consent: good, Nonce: "n"}
	if err := spawn.CheckIntro(in, t0); err != nil {
		t.Fatalf("good intro: %v", err)
	}
	raw, _ := spawn.EncodeIntro(in)
	back, err := spawn.DecodeIntro(raw)
	if err != nil || back.Nonce != "n" || back.Consent.Exp != good.Exp {
		t.Fatalf("intro round trip: %v %+v", err, back)
	}

	bad := func(name string, mut func(*spawn.Intro)) {
		t.Helper()
		x := in
		mut(&x)
		if err := spawn.CheckIntro(x, t0); !errors.Is(err, spawn.ErrBadIntro) {
			t.Fatalf("%s: err = %v, want ErrBadIntro", name, err)
		}
	}
	other := r.w.actor("other")
	bad("no nonce", func(x *spawn.Intro) { x.Nonce = "" })
	bad("foreign location", func(x *spawn.Intro) { x.Location = *r.drv.a.CurrentLocation() })
	bad("consent by another", func(x *spawn.Intro) { x.Consent = consent(t, other, "*", spawn.FacetBirth) })
	bad("frontdoor, not birth", func(x *spawn.Intro) {
		fd, _ := p.Frontdoor(postage.Require(8), 60)
		x.Consent = fd
	})
	bad("no postage", func(x *spawn.Intro) { x.Consent = consent(t, p, cert.AudAny, spawn.FacetBirth) })
	bad("expired", func(x *spawn.Intro) {
		c := good
		c.Exp = t0
		x.Consent = c
	})
	if _, err := r.sp.BirthConsent(0); !errors.Is(err, actor.ErrBadTTL) {
		t.Fatalf("zero window: %v", err)
	}

	child := other.ID()
	renew, _ := cert.Sign(cert.Cert{Aud: string(child), Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{p.ID()}, Facet: []string{actor.FacetRenew}}, Iat: t0, Exp: t0 + 60}, p.Signer)
	if err := spawn.CheckKit(spawn.Kit{Grants: [][]cert.Cert{{renew}}}, p.ID(), child, t0); err != nil {
		t.Fatalf("good kit: %v", err)
	}
	if err := spawn.CheckKit(spawn.Kit{}, p.ID(), child, t0); !errors.Is(err, spawn.ErrNoRenewChain) {
		t.Fatalf("empty kit: %v", err)
	}
	notMe := renew
	notMe.Aud = string(p.ID())
	notMe, _ = cert.Sign(notMe, p.Signer)
	if err := spawn.CheckKit(spawn.Kit{Grants: [][]cert.Cert{{notMe}}}, p.ID(), child, t0); !errors.Is(err, spawn.ErrBadKit) {
		t.Fatalf("chain to someone else: %v", err)
	}
	forged := renew
	forged.Exp++
	if err := spawn.CheckKit(spawn.Kit{Grants: [][]cert.Cert{{forged}}}, p.ID(), child, t0); !errors.Is(err, spawn.ErrBadKit) {
		t.Fatalf("forged link: %v", err)
	}
	if err := spawn.CheckKit(spawn.Kit{Grants: [][]cert.Cert{{renew}}}, p.ID(), child, t0+60); !errors.Is(err, spawn.ErrBadKit) {
		t.Fatalf("expired link: %v", err)
	}
	// A kit whose only chain is to the wrong facet lacks the mandate.
	app := renew
	app.Cav.Facet = []string{"app"}
	app, _ = cert.Sign(app, p.Signer)
	if err := spawn.CheckKit(spawn.Kit{Grants: [][]cert.Cert{{app}}}, p.ID(), child, t0); !errors.Is(err, spawn.ErrNoRenewChain) {
		t.Fatalf("no renew: %v", err)
	}
	// Born refuses a bad intro before sending anything.
	x := in
	x.Nonce = ""
	if _, err := spawn.Born(r.w.ctx, other, x, nil, 0); !errors.Is(err, spawn.ErrBadIntro) {
		t.Fatalf("Born bad intro: %v", err)
	}
}
