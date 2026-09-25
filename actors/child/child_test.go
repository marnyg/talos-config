package child_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
	"github.com/marnyg/talos-config/protocol/provisioner"
	"github.com/marnyg/talos-config/protocol/spawn"
)

const (
	t0    int64 = 1_800_000_000
	image       = "ghcr.io/x/child@sha256:0000000000000000000000000000000000000000000000000000000000000000"
)

// rig: a parent with a spawner, a provisioner whose driver "starts the
// container" by running child.Run in-process, all on one memory network
// under one settable clock. The beat runs on wall time (short); the
// certs' clock is the fake one, advanced by the test.
type rig struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc
	net    *actor.MemoryNetwork
	clock  atomic.Int64
	parent *actor.Actor
	sp     *spawn.Spawner
	prov   *actor.Actor
	drv    *driver
}

type driver struct {
	r     *rig
	opts  child.Options
	mu    sync.Mutex
	exts  []int64
	kills int
	ended chan error // child.Run's return
	// bornOnly, when set, runs spawn.Born instead of child.Run and hands
	// the child actor over on it once born: the test then owns Grants.
	bornOnly chan *actor.Actor
}

func (d *driver) Start(_ context.Context, spec provisioner.StartSpec) (provisioner.Handle, error) {
	in, err := spawn.DecodeIntro(spec.Params)
	if err != nil {
		return "", err
	}
	a := d.r.actor("child")
	go func() { _ = a.Listen(d.r.ctx) }()
	if d.bornOnly != nil {
		go func() {
			if _, err := a.PublishLocation(3600); err != nil {
				d.ended <- err
				return
			}
			if _, err := spawn.Born(d.r.ctx, a, in, []string{child.FacetPing}, 3600); err != nil {
				d.ended <- err
				return
			}
			d.bornOnly <- a
		}()
		return provisioner.Handle("ctr-" + spec.Lease), nil
	}
	go func() { d.ended <- child.Run(d.r.ctx, a, in, d.opts) }()
	return provisioner.Handle("ctr-" + spec.Lease), nil
}

func (d *driver) Extend(_ context.Context, _ provisioner.Handle, until int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exts = append(d.exts, until)
	return nil
}

func (d *driver) Kill(context.Context, provisioner.Handle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kills++
	return nil
}

func (d *driver) List(context.Context) ([]provisioner.Running, error) { return nil, nil }

func (d *driver) extends() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int64(nil), d.exts...)
}

func setup(t *testing.T, opts child.Options) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, ctx: ctx, cancel: cancel, net: actor.NewMemoryNetwork()}
	t.Cleanup(cancel)
	r.clock.Store(t0)

	r.parent = r.actor("parent")
	r.sp = spawn.New(r.parent)
	r.sp.Postage = postage.Require(8)
	r.sp.Window = 10 * 60
	// Longer than the window: the lease's first deadline is the window's
	// end, and #extend only fires when a re-issued exp goes past it.
	r.sp.KitTTL = 20 * 60

	r.prov = r.actor("prov")
	r.drv = &driver{r: r, opts: opts, ended: make(chan error, 1)}
	c, err := cert.Sign(cert.Cert{
		Aud: string(r.parent.ID()),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{r.prov.ID()}, Facet: []string{spawn.FacetSpawn, spawn.FacetExtend, spawn.FacetKill}},
		Iat: t0,
		Exp: t0 + 24*3600,
	}, r.prov.Signer)
	if err != nil {
		t.Fatal(err)
	}
	r.prov.Consents = []cert.Cert{c}
	provisioner.New(r.prov, r.drv)
	r.sp.Provisioner = r.prov.ID()

	r.start(r.prov)
	r.start(r.parent)
	if err := r.parent.UpdateLocation(r.prov.ID(), r.prov.CurrentLocation()); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) actor(name string) *actor.Actor {
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
	a.Clock = r.clock.Load
	return a
}

func (r *rig) start(a *actor.Actor) {
	r.t.Helper()
	go func() { _ = a.Listen(r.ctx) }()
	if _, err := a.PublishLocation(3600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) spawn(t *testing.T, spec spawn.Spec) spawn.Birth {
	t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	pr, err := r.sp.Spawn(ctx, spec)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	b, err := pr.Wait(ctx)
	if err != nil {
		t.Fatalf("promise: %v", err)
	}
	return b
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// TestRunLifecycle is the acceptance shape of 0bc.4.6 in-process: born
// → the parent pings the child → a beat renews and the provisioner
// sees #extend → the parent stops re-issuing (clock past the chain's
// exp) → the child exits with ErrLapsed.
func TestRunLifecycle(t *testing.T) {
	r := setup(t, child.Options{Beat: 20 * time.Millisecond})
	b := r.spawn(t, spawn.Spec{Image: image})
	if b.Location == nil {
		t.Fatal("birth without the child's location")
	}

	// P→C on the child's own consent, empty chain. The promise resolves
	// at the parent's #birth handler, before Born has installed that
	// consent on the child, so the first ping may be refused: retry.
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	var rep *actor.Reply
	waitFor(t, "ping", func() bool {
		var err error
		rep, err = r.parent.Send(ctx, b.ID, child.FacetPing, []byte(`"hi"`))
		return err == nil
	})
	if string(rep.Payload) != `"hi"` {
		t.Fatalf("ping reply %q", rep.Payload)
	}

	// A beat with the clock advanced re-issues the #renew chain with a
	// later exp; the spawner's decorator turns that into #extend.
	r.clock.Add(10)
	waitFor(t, "#extend", func() bool { return len(r.drv.extends()) > 0 })
	if got, want := r.drv.extends()[0], t0+10+r.sp.KitTTL; got != want {
		t.Fatalf("extend until %d, want %d", got, want)
	}
	if _, until, ok := r.sp.Child(b.ID); !ok || until != t0+10+r.sp.KitTTL {
		t.Fatalf("born table until %d ok=%v", until, ok)
	}

	// Past every re-issued exp: the next beat finds no live edge.
	r.clock.Add(24 * 3600)
	select {
	case err := <-r.drv.ended:
		if !errors.Is(err, child.ErrLapsed) {
			t.Fatalf("Run ended with %v, want ErrLapsed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not lapse")
	}
}

// TestRenewInstallsUnderEveryKey: a cert held under two (target, facet)
// keys (an Outfit chain over two facets) is sent once and its
// replacement lands under both.
func TestRenewInstallsUnderEveryKey(t *testing.T) {
	r := setup(t, child.Options{})
	r.drv.bornOnly = make(chan *actor.Actor, 1) // no Run: Renew is called by hand
	outfit := func(c cert.ActorID, now int64) (spawn.Kit, error) {
		extra, err := cert.Sign(cert.Cert{
			Aud: string(c),
			Can: cert.VerbInvoke,
			Cav: cert.Caveats{Target: []cert.ActorID{r.parent.ID()}, Facet: []string{"#a", "#b"}},
			Iat: now,
			Exp: now + 60,
		}, r.parent.Signer)
		if err != nil {
			return spawn.Kit{}, err
		}
		return spawn.Kit{Grants: [][]cert.Cert{{extra}}}, nil
	}
	r.spawn(t, spawn.Spec{Image: image, Outfit: outfit})
	var c *actor.Actor
	select {
	case c = <-r.drv.bornOnly:
	case err := <-r.drv.ended:
		t.Fatalf("born: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("child not born")
	}
	r.clock.Add(5)
	n, err := child.Renew(r.ctx, c, r.parent.ID())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("renewed %d certs, want 2 (renew chain + outfit)", n)
	}
	for _, f := range []string{actor.FacetRenew, "#a", "#b"} {
		chain := c.Grants[actor.GrantKey{Target: r.parent.ID(), Facet: f}]
		if len(chain) != 1 || chain[0].Iat != t0+5 {
			t.Fatalf("%s: chain %+v not re-issued at t0+5", f, chain)
		}
	}
	if !child.Live(c, r.parent.ID(), t0+5+r.sp.KitTTL-1) || child.Live(c, r.parent.ID(), t0+5+r.sp.KitTTL) {
		t.Fatal("Live does not follow the re-issued exp")
	}
}
