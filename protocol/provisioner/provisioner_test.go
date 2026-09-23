package provisioner_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/provisioner"
	"github.com/marnyg/talos-config/protocol/spawn"
)

const t0 int64 = 1_700_000_000

var image = "img@sha256:" + strings.Repeat("0", 64)

// driver records every call and fails on demand.
type driver struct {
	mu        sync.Mutex
	starts    []provisioner.StartSpec
	extends   []int64
	kills     []provisioner.Handle
	startErr  error
	extendErr error
	killErr   error
	// gate, when set, blocks Start until closed (a slow image pull).
	gate chan struct{}
}

func (d *driver) Start(_ context.Context, s provisioner.StartSpec) (provisioner.Handle, error) {
	if d.gate != nil {
		<-d.gate
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.startErr != nil {
		return "", d.startErr
	}
	d.starts = append(d.starts, s)
	return provisioner.Handle("ctr-" + s.Lease), nil
}

func (d *driver) Extend(_ context.Context, _ provisioner.Handle, until int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.extendErr != nil {
		return d.extendErr
	}
	d.extends = append(d.extends, until)
	return nil
}

func (d *driver) Kill(_ context.Context, h provisioner.Handle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.killErr != nil {
		return d.killErr
	}
	d.kills = append(d.kills, h)
	return nil
}

func (d *driver) set(f func(*driver)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f(d)
}

func (d *driver) counts() (starts, extends, kills int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.starts), len(d.extends), len(d.kills)
}

type rig struct {
	t        *testing.T
	ctx      context.Context
	cancel   context.CancelFunc
	now      atomic.Int64
	net      *actor.MemoryNetwork
	drv      *driver
	prov     *actor.Actor
	p        *provisioner.Provisioner
	customer *actor.Actor
	stranger *actor.Actor
}

func newActor(r *rig, name string) *actor.Actor {
	r.t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		r.t.Fatal(err)
	}
	s := cert.NewEdSigner(priv)
	ep, err := r.net.Bind(s.ActorID(), name)
	if err != nil {
		r.t.Fatal(err)
	}
	a := actor.New(s, ep)
	a.Clock = r.now.Load
	return a
}

func (r *rig) start(a *actor.Actor) {
	r.t.Helper()
	done := make(chan error, 1)
	go func() { done <- a.Listen(r.ctx) }()
	r.t.Cleanup(func() {
		r.cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				r.t.Errorf("Listen: %v", err)
			}
		case <-time.After(5 * time.Second):
			r.t.Error("Listen did not stop")
		}
	})
	if _, err := a.PublishLocation(3600); err != nil {
		r.t.Fatal(err)
	}
}

// consent is the provisioner's root for aud over the three facets.
func consent(t *testing.T, prov *actor.Actor, aud cert.ActorID) cert.Cert {
	t.Helper()
	c, err := cert.Sign(cert.Cert{
		Aud: string(aud),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{prov.ID()}, Facet: []string{spawn.FacetSpawn, spawn.FacetExtend, spawn.FacetKill}},
		Iat: t0,
		Exp: t0 + 86400,
	}, prov.Signer)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func setup(t *testing.T) *rig {
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, ctx: ctx, cancel: cancel, net: actor.NewMemoryNetwork(), drv: &driver{}}
	r.now.Store(t0)
	r.prov = newActor(r, "prov")
	r.p = provisioner.New(r.prov, r.drv)
	r.customer = newActor(r, "customer")
	r.stranger = newActor(r, "stranger")
	// Both hold a consent — the stranger is authorised to CALL, and is
	// still not the owner of the customer's lease.
	r.prov.Consents = []cert.Cert{consent(t, r.prov, r.customer.ID()), consent(t, r.prov, r.stranger.ID())}
	r.start(r.prov)
	r.start(r.customer)
	r.start(r.stranger)
	for _, a := range []*actor.Actor{r.customer, r.stranger} {
		if err := a.UpdateLocation(r.prov.ID(), r.prov.CurrentLocation()); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *rig) call(from *actor.Actor, facet string, req any) ([]byte, error) {
	r.t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		r.t.Fatal(err)
	}
	rep, err := from.Send(r.ctx, r.prov.ID(), facet, payload)
	if err != nil {
		return nil, err
	}
	return rep.Payload, nil
}

func (r *rig) spawn(from *actor.Actor, until int64) string {
	r.t.Helper()
	body, err := r.call(from, spawn.FacetSpawn, spawn.SpawnRequest{Image: image, Params: []byte(`{"opaque":true}`), Until: until})
	if err != nil {
		r.t.Fatalf("#spawn: %v", err)
	}
	var rep spawn.SpawnReply
	if err := json.Unmarshal(body, &rep); err != nil || rep.Lease == "" {
		r.t.Fatalf("#spawn reply %s: %v", body, err)
	}
	return rep.Lease
}

func refused(t *testing.T, err error, msg string) {
	t.Helper()
	var re *actor.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("want a refusal %q, got %v", msg, err)
	}
	if re.Code != actor.StatusError || !strings.Contains(re.Msg, msg) {
		t.Fatalf("refusal %q, want %q", re.Msg, msg)
	}
}

// TestLeaseMachine: #spawn → running with the caller as owner and the
// request's until; #extend moves the deadline; #kill ends it; every
// step reaches the driver exactly once, with the params unread.
func TestLeaseMachine(t *testing.T) {
	r := setup(t)
	id := r.spawn(r.customer, t0+600)
	l, ok := r.p.Lease(id)
	if !ok || l.State != provisioner.StateRunning || l.Owner != r.customer.ID() || l.Until != t0+600 ||
		l.Image != image || l.Handle != provisioner.Handle("ctr-"+id) {
		t.Fatalf("lease %+v ok=%v", l, ok)
	}
	r.drv.mu.Lock()
	s := r.drv.starts[0]
	r.drv.mu.Unlock()
	if s.Lease != id || s.Image != image || string(s.Params) != `{"opaque":true}` || s.Until != t0+600 {
		t.Fatalf("driver start %+v", s)
	}

	body, err := r.call(r.customer, spawn.FacetExtend, spawn.ExtendRequest{Lease: id, Until: t0 + 7200})
	if err != nil {
		t.Fatalf("#extend: %v", err)
	}
	var er spawn.ExtendReply
	if json.Unmarshal(body, &er) != nil || er.Until != t0+7200 {
		t.Fatalf("#extend reply %s", body)
	}
	if l, _ := r.p.Lease(id); l.Until != t0+7200 {
		t.Fatalf("until %d after extend", l.Until)
	}
	// The owner may also shorten its own lease.
	if _, err := r.call(r.customer, spawn.FacetExtend, spawn.ExtendRequest{Lease: id, Until: t0 + 1800}); err != nil {
		t.Fatalf("#extend shorter: %v", err)
	}
	if l, _ := r.p.Lease(id); l.Until != t0+1800 {
		t.Fatalf("until %d after shortening", l.Until)
	}

	if _, err := r.call(r.customer, spawn.FacetKill, spawn.KillRequest{Lease: id}); err != nil {
		t.Fatalf("#kill: %v", err)
	}
	if _, ok := r.p.Lease(id); ok {
		t.Fatal("killed lease still in the table")
	}
	if len(r.p.Leases()) != 0 {
		t.Fatalf("table %+v", r.p.Leases())
	}
	if st, ex, ki := r.drv.counts(); st != 1 || ex != 2 || ki != 1 {
		t.Fatalf("driver calls start=%d extend=%d kill=%d", st, ex, ki)
	}
	_, err = r.call(r.customer, spawn.FacetExtend, spawn.ExtendRequest{Lease: id, Until: t0 + 9000})
	refused(t, err, provisioner.RefuseUnknownLease)
}

// TestRefusals: the shape rules and the ownership rule. A caller the
// provisioner consents to is still not the owner of another's lease.
func TestRefusals(t *testing.T) {
	r := setup(t)
	_, err := r.call(r.customer, spawn.FacetSpawn, spawn.SpawnRequest{Image: "img:latest", Until: t0 + 600})
	refused(t, err, provisioner.RefuseImage)
	_, err = r.call(r.customer, spawn.FacetSpawn, spawn.SpawnRequest{Image: image, Until: t0})
	refused(t, err, provisioner.RefuseUntilPast)
	if st, _, _ := r.drv.counts(); st != 0 {
		t.Fatal("a refused #spawn reached the driver")
	}

	id := r.spawn(r.customer, t0+600)
	_, err = r.call(r.stranger, spawn.FacetExtend, spawn.ExtendRequest{Lease: id, Until: t0 + 7200})
	refused(t, err, provisioner.RefuseNotOwner)
	_, err = r.call(r.stranger, spawn.FacetKill, spawn.KillRequest{Lease: id})
	refused(t, err, provisioner.RefuseNotOwner)
	_, err = r.call(r.customer, spawn.FacetExtend, spawn.ExtendRequest{Lease: id, Until: t0})
	refused(t, err, provisioner.RefuseUntilPast)
	_, err = r.call(r.customer, spawn.FacetKill, spawn.KillRequest{Lease: "nope"})
	refused(t, err, provisioner.RefuseUnknownLease)
	if l, _ := r.p.Lease(id); l.Until != t0+600 || l.State != provisioner.StateRunning {
		t.Fatalf("lease touched by refused calls: %+v", l)
	}
	if _, ex, ki := r.drv.counts(); ex != 0 || ki != 0 {
		t.Fatal("a refused call reached the driver")
	}
	if err := provisioner.CheckImage("a b@sha256:" + strings.Repeat("0", 64)); err == nil {
		t.Fatal("image with whitespace accepted")
	}
	if err := provisioner.CheckImage("img@sha256:" + strings.Repeat("0", 63)); err == nil {
		t.Fatal("short digest accepted")
	}
}

// TestDriverFailures: Start failing drops the lease and is the
// refusal; Extend failing leaves the deadline; Kill failing leaves the
// lease running for a retry.
func TestDriverFailures(t *testing.T) {
	r := setup(t)
	r.drv.set(func(d *driver) { d.startErr = errors.New("no capacity") })
	_, err := r.call(r.customer, spawn.FacetSpawn, spawn.SpawnRequest{Image: image, Until: t0 + 600})
	refused(t, err, "no capacity")
	if len(r.p.Leases()) != 0 {
		t.Fatalf("table after failed start %+v", r.p.Leases())
	}
	r.drv.set(func(d *driver) { d.startErr = nil })

	id := r.spawn(r.customer, t0+600)
	r.drv.set(func(d *driver) { d.extendErr = errors.New("quota") })
	_, err = r.call(r.customer, spawn.FacetExtend, spawn.ExtendRequest{Lease: id, Until: t0 + 7200})
	refused(t, err, "quota")
	if l, _ := r.p.Lease(id); l.Until != t0+600 {
		t.Fatalf("until %d after a failed extend", l.Until)
	}

	r.drv.set(func(d *driver) { d.killErr = errors.New("api down") })
	_, err = r.call(r.customer, spawn.FacetKill, spawn.KillRequest{Lease: id})
	refused(t, err, "api down")
	if l, ok := r.p.Lease(id); !ok || l.State != provisioner.StateRunning {
		t.Fatalf("lease after a failed kill: %+v ok=%v", l, ok)
	}
	r.drv.set(func(d *driver) { d.killErr = nil })
	if _, err := r.call(r.customer, spawn.FacetKill, spawn.KillRequest{Lease: id}); err != nil {
		t.Fatalf("retry kill: %v", err)
	}
}

// TestSweep: the deadline is the provisioner's. At until the running
// lease is killed and leaves the table; a Kill that fails keeps it for
// the next sweep; a lease still pending (Start in flight) is not swept.
func TestSweep(t *testing.T) {
	r := setup(t)
	a := r.spawn(r.customer, t0+600)
	b := r.spawn(r.customer, t0+1200)

	r.now.Store(t0 + 600)
	r.drv.set(func(d *driver) { d.killErr = errors.New("api down") })
	r.p.Sweep(r.ctx)
	if _, ok := r.p.Lease(a); !ok {
		t.Fatal("lease dropped although Kill failed")
	}
	r.drv.set(func(d *driver) { d.killErr = nil })
	r.p.Sweep(r.ctx)
	if _, ok := r.p.Lease(a); ok {
		t.Fatal("lapsed lease still in the table")
	}
	if l, ok := r.p.Lease(b); !ok || l.State != provisioner.StateRunning {
		t.Fatalf("unexpired lease %+v ok=%v", l, ok)
	}
	if _, _, ki := r.drv.counts(); ki != 1 {
		t.Fatalf("kills %d, want 1", ki)
	}

	// A handler sweeps too: b's own #extend arriving after its deadline
	// finds it already lapsed.
	r.now.Store(t0 + 1200)
	_, err := r.call(r.customer, spawn.FacetExtend, spawn.ExtendRequest{Lease: b, Until: t0 + 2400})
	refused(t, err, provisioner.RefuseUnknownLease)

	// Pending: Start is in flight when the sweep runs.
	gate := make(chan struct{})
	r.drv.set(func(d *driver) { d.gate = gate })
	done := make(chan string, 1)
	go func() { done <- r.spawn(r.customer, t0+1800) }()
	deadline := time.After(5 * time.Second)
	for {
		if ls := r.p.Leases(); len(ls) == 1 && ls[0].State == provisioner.StatePending {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no pending lease appeared")
		case <-time.After(5 * time.Millisecond):
		}
	}
	r.now.Store(t0 + 1800)
	r.p.Sweep(r.ctx)
	if ls := r.p.Leases(); len(ls) != 1 || ls[0].State != provisioner.StatePending {
		t.Fatalf("pending lease swept: %+v", ls)
	}
	close(gate)
	id := <-done
	if l, ok := r.p.Lease(id); !ok || l.State != provisioner.StateRunning {
		t.Fatalf("lease after a slow start %+v ok=%v", l, ok)
	}
	// ...and lapses at the next sweep, its deadline having passed.
	r.p.Sweep(r.ctx)
	if _, ok := r.p.Lease(id); ok {
		t.Fatal("slow-started lease past its deadline not lapsed")
	}
}
