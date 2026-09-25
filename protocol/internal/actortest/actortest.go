// Package actortest is the shared test rig for packages that run
// actors over an actor.MemoryNetwork under a fake clock: bind a fresh
// key, start its Listen loop with a cleanup that stops it, publish its
// location. Test-only; internal so it never becomes protocol API.
package actortest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// LocationTTL is the lifetime of the location record Start publishes.
const LocationTTL int64 = 3600

// Clock is a settable clock every actor in a World reads.
type Clock struct{ now atomic.Int64 }

func (c *Clock) Now() int64      { return c.now.Load() }
func (c *Clock) Advance(d int64) { c.now.Add(d) }
func (c *Clock) Set(t int64)     { c.now.Store(t) }

// World is one memory network, one clock and one context shared by its
// actors. Ctx is cancelled at the first Start cleanup (cleanups run
// LIFO, so a later registration waiting on Listen would otherwise run
// before the cancel) and again at test end.
type World struct {
	T      testing.TB
	Ctx    context.Context
	Net    *actor.MemoryNetwork
	Clock  *Clock
	cancel context.CancelFunc
}

// New makes a world whose clock reads t0.
func New(t testing.TB, t0 int64) *World {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &World{T: t, Ctx: ctx, Net: actor.NewMemoryNetwork(), Clock: &Clock{}, cancel: cancel}
	w.Clock.Set(t0)
	return w
}

// Signer mints a fresh ed25519 identity.
func (w *World) Signer() cert.EdSigner {
	w.T.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		w.T.Fatal(err)
	}
	return cert.NewEdSigner(priv)
}

// Actor binds a fresh identity to the network under name, on the
// world's clock. Not started.
func (w *World) Actor(name string) *actor.Actor {
	w.T.Helper()
	s := w.Signer()
	ep, err := w.Net.Bind(s.ActorID(), name)
	if err != nil {
		w.T.Fatal(err)
	}
	a := actor.New(s, ep)
	a.Clock = w.Clock.Now
	return a
}

// Start runs a.Listen until the test ends (failing the test if it
// errors or does not stop) and publishes a's location for LocationTTL.
func (w *World) Start(a *actor.Actor) {
	w.T.Helper()
	done := make(chan error, 1)
	go func() { done <- a.Listen(w.Ctx) }()
	w.T.Cleanup(func() {
		w.cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				w.T.Errorf("Listen: %v", err)
			}
		case <-time.After(5 * time.Second):
			w.T.Error("Listen did not stop")
		}
	})
	if _, err := a.PublishLocation(LocationTTL); err != nil {
		w.T.Fatal(err)
	}
}
