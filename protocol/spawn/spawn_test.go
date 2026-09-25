package spawn_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/internal/actortest"
	"github.com/marnyg/talos-config/protocol/postage"
	"github.com/marnyg/talos-config/protocol/provisioner"
	"github.com/marnyg/talos-config/protocol/spawn"
)

// image is a digest-form image name (the only shape a provisioner takes).
var image = "img@sha256:" + strings.Repeat("a", 64)

const t0 int64 = 1_700_000_000
const hour int64 = 3600

// world is the shared memory-network rig (internal/actortest).
type world = actortest.World

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

// fakeDriver is the protocol tests' provisioner.Driver behind a REAL
// provisioner actor (ADR-0009's fake-driver acceptance): Start "runs
// the container" by running a child actor in-process — a new key, a
// bound endpoint, a published location, Born(intro) — reading the
// params only to hand them to the child, as a real driver would via
// env. Extend and Kill are recorded.
type fakeDriver struct {
	w       *world
	a       *actor.Actor // the provisioner actor
	p       *provisioner.Provisioner
	hatched chan hatch
	// facets the child consents to its parent over; refuse makes
	// Start fail instead of starting anything.
	facets []string
	refuse bool
	// born, when set, replaces the child's Born call (a driver that
	// runs something other than an honest child).
	born func(child *actor.Actor, in spawn.Intro) (spawn.Kit, error)

	mu        sync.Mutex
	extendErr error
	extends   []int64
	kills     []provisioner.Handle
}

func newFakeDriver(w *world, parent cert.ActorID) *fakeDriver {
	d := &fakeDriver{w: w, a: w.Actor("prov"), hatched: make(chan hatch, 8), facets: []string{"app"}}
	// The provisioner's consent to its customer: one delegable root over
	// all three facets (v0: the parent's key, ADR-0009 open item).
	c, err := cert.Sign(cert.Cert{
		Aud: string(parent),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{d.a.ID()}, Facet: []string{spawn.FacetSpawn, spawn.FacetExtend, spawn.FacetKill}},
		Iat: t0,
		Exp: t0 + 24*hour,
	}, d.a.Signer)
	if err != nil {
		w.T.Fatal(err)
	}
	d.a.Consents = []cert.Cert{c}
	d.p = provisioner.New(d.a, d)
	return d
}

func (d *fakeDriver) Start(_ context.Context, spec provisioner.StartSpec) (provisioner.Handle, error) {
	if d.refuse {
		return "", errors.New("no capacity")
	}
	go d.run(spec.Params)
	return provisioner.Handle("ctr-" + spec.Lease), nil
}

func (d *fakeDriver) Extend(_ context.Context, _ provisioner.Handle, until int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.extendErr != nil {
		return d.extendErr
	}
	d.extends = append(d.extends, until)
	return nil
}

func (d *fakeDriver) Kill(_ context.Context, h provisioner.Handle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kills = append(d.kills, h)
	return nil
}

// List: nothing survives a restart of the in-process fake.
func (d *fakeDriver) List(context.Context) ([]provisioner.Running, error) { return nil, nil }

func (d *fakeDriver) seen() (extends []int64, kills []provisioner.Handle) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int64(nil), d.extends...), append([]provisioner.Handle(nil), d.kills...)
}

func (d *fakeDriver) setExtendErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.extendErr = err
}

func (d *fakeDriver) run(params []byte) {
	in, err := spawn.DecodeIntro(params)
	if err != nil {
		d.hatched <- hatch{err: err}
		return
	}
	child := d.w.Actor("child-" + in.Nonce[:6])
	child.Postage = postage.Default
	d.w.Start(child)
	var kit spawn.Kit
	if d.born != nil {
		kit, err = d.born(child, in)
	} else {
		kit, err = spawn.Born(d.w.Ctx, child, in, d.facets, hour)
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
	w := actortest.New(t, t0)
	p := w.Actor("parent")
	sp := spawn.New(p)
	sp.Postage = postage.Require(8)
	sp.Window = 10 * 60
	drv := newFakeDriver(w, p.ID())
	sp.Provisioner = drv.a.ID()
	// The parent knows where its provisioner is (a configured
	// correspondent); the chain to #spawn is the provisioner's consent,
	// presented empty.
	w.Start(drv.a)
	w.Start(p)
	if err := p.UpdateLocation(drv.a.ID(), drv.a.CurrentLocation()); err != nil {
		t.Fatal(err)
	}
	return &rig{w: w, parent: p, sp: sp, drv: drv, spec: spawn.Spec{Image: image}}
}

// logSink captures the spawner's slog records.
type logSink struct {
	mu   sync.Mutex
	msgs []string
}

func (s *logSink) Enabled(context.Context, slog.Level) bool { return true }
func (s *logSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, r.Message)
	return nil
}
func (s *logSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *logSink) WithGroup(string) slog.Handler      { return s }
func (s *logSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

func (r *rig) waitPromise(t *testing.T, pr *spawn.Promise) spawn.Birth {
	t.Helper()
	ctx, cancel := context.WithTimeout(r.w.Ctx, 5*time.Second)
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
	pr, err := r.sp.Spawn(r.w.Ctx, r.spec)
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
	if b.Lease.Provisioner != r.drv.a.ID() || b.Lease.ID == "" {
		t.Fatalf("lease %+v", b.Lease)
	}
	l, ok := r.drv.p.Lease(b.Lease.ID)
	if !ok || l.State != provisioner.StateRunning || l.Owner != r.parent.ID() || l.Until != t0+r.sp.Window || l.Image != image {
		t.Fatalf("provisioner lease %+v (ok=%v)", l, ok)
	}
	if _, until, ok := r.sp.Child(b.ID); !ok || until != t0+r.sp.Window {
		t.Fatalf("born table: ok=%v until=%d", ok, until)
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
	r.w.Clock.Advance(r.sp.Window / 4)
	payload, _ := actor.EncodeRenewRequest([]cert.Cert{renew}, nil)
	rep, err := h.a.Send(r.w.Ctx, r.parent.ID(), actor.FacetRenew, payload)
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
	// The lease rode the beat (ADR-0009): the decorator sent #extend
	// {until: fresh.exp} before the reply came back, so by now the
	// provisioner's deadline IS the fresh cert's exp.
	if l, _ := r.drv.p.Lease(b.Lease.ID); l.Until != fresh[0].Exp {
		t.Fatalf("lease until %d after renew, want fresh.exp %d", l.Until, fresh[0].Exp)
	}
	if ext, _ := r.drv.seen(); len(ext) != 1 || ext[0] != fresh[0].Exp {
		t.Fatalf("driver extends %v, want [%d]", ext, fresh[0].Exp)
	}
	// Second beat on the FRESH cert (6sax): the kit chain is P's own
	// consent, and P re-installed it as it re-issued it.
	h.a.Grant(r.parent.ID(), actor.FacetRenew, fresh[0])
	r.w.Clock.Advance(r.sp.Window / 4)
	payload, _ = actor.EncodeRenewRequest([]cert.Cert{fresh[0]}, nil)
	if _, err := h.a.Send(r.w.Ctx, r.parent.ID(), actor.FacetRenew, payload); err != nil {
		t.Fatalf("child's second beat: %v", err)
	}

	// P→C authority is the child's own consent: the parent sends with
	// an empty chain.
	h.a.AcceptTable["app"] = func(_ context.Context, inv *actor.Invocation) ([]byte, error) {
		return []byte(`"hi ` + string(inv.From) + `"`), nil
	}
	rep, err = r.parent.Send(r.w.Ctx, b.ID, "app", []byte(`{}`))
	if err != nil {
		t.Fatalf("parent → child app: %v", err)
	}
	if !strings.Contains(string(rep.Payload), string(r.parent.ID())) {
		t.Fatalf("app reply %s", rep.Payload)
	}
	// ...and a facet the child did not consent to is refused.
	_, err = r.parent.Send(r.w.Ctx, b.ID, "other", []byte(`{}`))
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
		return spawn.Born(r.w.Ctx, child, in, nil, 0)
	}
	pr, err := r.sp.Spawn(r.w.Ctx, r.spec)
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
		return a.Send(r.w.Ctx, r.parent.ID(), spawn.FacetBirth, payload)
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
	imp := r.w.Actor("impostor")
	r.w.Start(imp)
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
	pr2, err := r.sp.Spawn(r.w.Ctx, r.spec)
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
	pr, err := r.sp.Spawn(r.w.Ctx, r.spec)
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

	r.w.Clock.Advance(r.sp.Window)
	r.sp.Sweep()
	if _, err := pr.Wait(r.w.Ctx); !errors.Is(err, spawn.ErrBirthWindow) {
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
	_, err = h.a.Send(r.w.Ctx, r.parent.ID(), spawn.FacetBirth, payload)
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
	twin, err := r.sp.Spawn(r.w.Ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	r.drv.wait(t)

	r.drv.refuse = true
	_, err = r.sp.Spawn(r.w.Ctx, r.spec)
	if re := remoteErr(t, err); re.Code != actor.StatusError || !strings.HasSuffix(re.Msg, "no capacity") {
		t.Fatalf("refusal: %v", err)
	}
	if n := len(r.drv.p.Leases()); n != 1 {
		t.Fatalf("provisioner holds %d leases after a failed Start, want the twin only", n)
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
	r.w.Clock.Advance(r.sp.Window)
	r.sp.Sweep()
	if _, err := twin.Wait(r.w.Ctx); !errors.Is(err, spawn.ErrBirthWindow) {
		t.Fatalf("twin: %v", err)
	}
	if consents, _ := r.parent.Authority(); len(consents) != 0 || r.sp.Pending() != 0 {
		t.Fatalf("after sweep: %d pending, consents %+v", r.sp.Pending(), consents)
	}
	// No location, no provisioner: refused before anything is minted.
	r.sp.Provisioner = ""
	if _, err := r.sp.Spawn(r.w.Ctx, r.spec); !errors.Is(err, spawn.ErrNoProvisioner) {
		t.Fatalf("no provisioner: %v", err)
	}
	q := r.w.Actor("quiet")
	if _, err := spawn.New(q).Spawn(r.w.Ctx, spawn.Spec{Provisioner: r.drv.a.ID()}); !errors.Is(err, spawn.ErrNoLocation) {
		t.Fatalf("no location: %v", err)
	}
}

// TestOutfit: the parent's choice beyond the mandate rides in the kit
// and is installed under every (target, facet) its last link names,
// with the locations of the actors it names.
func TestOutfit(t *testing.T) {
	r := setup(t)
	sib := r.w.Actor("sibling")
	r.w.Start(sib)
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
	pr, err := r.sp.Spawn(r.w.Ctx, r.spec)
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
	if _, err := h.a.Send(r.w.Ctx, sib.ID(), "work", nil); err != nil {
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
	other := r.w.Actor("other")
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
	if _, err := spawn.Born(r.w.Ctx, other, x, nil, 0); !errors.Is(err, spawn.ErrBadIntro) {
		t.Fatalf("Born bad intro: %v", err)
	}
}

// TestLeaseFollowsRenewal pins ADR-0009's spawner half: the deadline
// asked of the provisioner is the latest exp re-issued to the child; a
// refused #extend leaves the #renew reply untouched, is logged, and is
// retried by the next beat; a re-issue that does not reach further
// extends nothing; Kill forgets the child.
func TestLeaseFollowsRenewal(t *testing.T) {
	r := setup(t)
	sink := &logSink{}
	r.sp.Log = slog.New(sink)
	pr, err := r.sp.Spawn(r.w.Ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	h := r.drv.wait(t)
	if h.err != nil {
		t.Fatal(h.err)
	}
	b := r.waitPromise(t, pr)
	beat := func(held cert.Cert) cert.Cert {
		t.Helper()
		payload, _ := actor.EncodeRenewRequest([]cert.Cert{held}, nil)
		rep, err := h.a.Send(r.w.Ctx, r.parent.ID(), actor.FacetRenew, payload)
		if err != nil {
			t.Fatalf("beat: %v", err)
		}
		fresh, errs, err := actor.DecodeRenewResponse(rep.Payload)
		if err != nil || errs[0] != nil {
			t.Fatalf("beat result: %v %v", err, errs)
		}
		h.a.Grant(r.parent.ID(), actor.FacetRenew, fresh[0])
		return fresh[0]
	}
	until := func() int64 {
		t.Helper()
		l, ok := r.drv.p.Lease(b.Lease.ID)
		if !ok {
			t.Fatal("lease gone")
		}
		return l.Until
	}
	first := t0 + r.sp.Window

	// The provisioner refuses #extend (its driver says no): the child's
	// renewal still succeeds, the deadline stays, one warning.
	r.drv.setExtendErr(errors.New("quota"))
	r.w.Clock.Advance(60)
	fresh := beat(h.kit.Grants[0][0])
	if got := until(); got != first {
		t.Fatalf("until %d after a refused #extend, want %d", got, first)
	}
	if _, asked, _ := r.sp.Child(b.ID); asked != first {
		t.Fatalf("asked deadline advanced to %d on a failed #extend", asked)
	}
	if sink.count() != 1 {
		t.Fatalf("%d log records, want 1 (the failed #extend)", sink.count())
	}

	// The next beat retries and the deadline catches up to fresh.exp.
	r.drv.setExtendErr(nil)
	r.w.Clock.Advance(60)
	fresh = beat(fresh)
	if got := until(); got != fresh.Exp {
		t.Fatalf("until %d, want fresh.exp %d", got, fresh.Exp)
	}
	// Under a frozen clock the re-issue reaches no further: no #extend.
	beat(fresh)
	if ext, _ := r.drv.seen(); len(ext) != 1 {
		t.Fatalf("driver extends %v, want exactly one", ext)
	}

	// Kill: #kill reaches the driver, the lease leaves the table, the
	// child is forgotten; a second Kill is unknown; the child's next
	// beat still renews (P's consent expires on its own) but no #extend
	// is attempted for a forgotten child.
	if err := r.sp.Kill(r.w.Ctx, b.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, kills := r.drv.seen(); len(kills) != 1 || kills[0] != provisioner.Handle("ctr-"+b.Lease.ID) {
		t.Fatalf("driver kills %v", kills)
	}
	if _, ok := r.drv.p.Lease(b.Lease.ID); ok {
		t.Fatal("lease still in the table after #kill")
	}
	if _, _, ok := r.sp.Child(b.ID); ok {
		t.Fatal("child still in the born table after Kill")
	}
	if err := r.sp.Kill(r.w.Ctx, b.ID); !errors.Is(err, spawn.ErrUnknownChild) {
		t.Fatalf("second kill: %v", err)
	}
	r.w.Clock.Advance(60)
	beat(fresh)
	if ext, _ := r.drv.seen(); len(ext) != 1 || sink.count() != 1 {
		t.Fatalf("a forgotten child's beat touched the lease: extends %v, logs %d", ext, sink.count())
	}
}

// TestLeaseLapses: a child that stops renewing. The spawner's Sweep
// forgets it once the asked deadline passes; the provisioner's Sweep
// kills it through the driver at the same instant — funding-tree
// semantics with no money and no message.
func TestLeaseLapses(t *testing.T) {
	r := setup(t)
	pr, err := r.sp.Spawn(r.w.Ctx, r.spec)
	if err != nil {
		t.Fatal(err)
	}
	if h := r.drv.wait(t); h.err != nil {
		t.Fatal(h.err)
	}
	b := r.waitPromise(t, pr)

	r.w.Clock.Advance(r.sp.Window - 1)
	r.sp.Sweep()
	r.drv.p.Sweep(r.w.Ctx)
	if _, _, ok := r.sp.Child(b.ID); !ok {
		t.Fatal("child forgotten before its deadline")
	}
	if _, ok := r.drv.p.Lease(b.Lease.ID); !ok {
		t.Fatal("lease lapsed before its deadline")
	}

	r.w.Clock.Advance(1)
	r.sp.Sweep()
	r.drv.p.Sweep(r.w.Ctx)
	if _, _, ok := r.sp.Child(b.ID); ok {
		t.Fatal("child not forgotten at its deadline")
	}
	if _, ok := r.drv.p.Lease(b.Lease.ID); ok {
		t.Fatal("lease not lapsed at its deadline")
	}
	if _, kills := r.drv.seen(); len(kills) != 1 {
		t.Fatalf("driver kills %v, want one", kills)
	}
}
