package irohtransport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// ---- fixtures (mirrors protocol/actor/actor_test.go) ----------------------

const t0 int64 = 1_700_000_000

type fakeClock struct{ now atomic.Int64 }

func newClock() *fakeClock {
	c := &fakeClock{}
	c.now.Store(t0)
	return c
}
func (c *fakeClock) Now() int64 { return c.now.Load() }

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

func echo(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	return inv.Envelope.Payload, nil
}

func wantRemote(t *testing.T, err error, code string) {
	t.Helper()
	var re *actor.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("want RemoteError %s, got %v", code, err)
	}
	if re.Code != code {
		t.Fatalf("want status %s, got %s (%s)", code, re.Code, re.Msg)
	}
}

// world is a set of actors, each on its own iroh endpoint, sharing a
// fake clock. relay == "" is the direct path.
type world struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc
	clk    *fakeClock
	relay  string
}

func newWorld(t *testing.T, relay string) *world {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	return &world{t: t, ctx: ctx, cancel: cancel, clk: newClock(), relay: relay}
}

// actor binds a new actor on a fresh iroh endpoint. On the relay path
// it waits for the home relay and its reach-me-at record carries only
// the relay tag, so the first packets must traverse the relay.
func (w *world) actor(name string) (*actor.Actor, cert.EdSigner, *Endpoint) {
	w.t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		w.t.Fatal(err)
	}
	s := cert.NewEdSigner(priv)
	ep, err := Bind(priv, Options{BindAddr: "127.0.0.1:0", Relay: w.relay})
	if err != nil {
		w.t.Fatalf("bind %s: %v", name, err)
	}
	w.t.Cleanup(func() { _ = ep.Close() })
	a := actor.New(s, ep)
	a.Clock = w.clk.Now
	var tags []string
	if w.relay == "" {
		tags = ep.Endpoints()
	} else {
		if err := ep.Online(w.ctx); err != nil {
			w.t.Fatalf("%s online: %v", name, err)
		}
		for _, tag := range ep.Endpoints() {
			if strings.HasPrefix(tag, TagRelay) {
				tags = append(tags, tag)
			}
		}
		// iroh normalises the URL (trailing slash); the tag carries iroh's form.
		if len(tags) != 1 || strings.TrimSuffix(tags[0], "/") != TagRelay+w.relay {
			w.t.Fatalf("%s: relay tags %v, want [%s]", name, tags, TagRelay+w.relay)
		}
	}
	if _, err := a.PublishLocation(3600, tags...); err != nil {
		w.t.Fatal(err)
	}
	w.t.Logf("%s = %s at %v", name, a.ID(), tags)
	return a, s, ep
}

// introduce seeds callers' location caches with target's reach-me-at:
// what the piggyback would have done after a first contact.
func (w *world) introduce(target *actor.Actor, callers ...*actor.Actor) {
	w.t.Helper()
	loc := target.CurrentLocation()
	if loc == nil {
		w.t.Fatal("target has no location")
	}
	for _, c := range callers {
		if err := c.UpdateLocation(target.ID(), loc); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *world) start(a *actor.Actor) {
	w.t.Helper()
	done := make(chan error, 1)
	go func() { done <- a.Listen(w.ctx) }()
	w.t.Cleanup(func() {
		w.cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, actor.ErrClosed) {
				w.t.Errorf("Listen: %v", err)
			}
		case <-time.After(10 * time.Second):
			w.t.Error("Listen did not stop")
		}
	})
}

// ---- the handshake ----------------------------------------------------------

// runHandshake is protocol/actor's TestTwoActorsHandshake (consent,
// grant, 3-link chain, attenuation, stranger, unknown facet) over iroh.
func runHandshake(t *testing.T, relay string) {
	w := newWorld(t, relay)
	now := w.clk.Now()

	b, bs, _ := w.actor("b") // receiver
	a, as, _ := w.actor("a") // caller holding a grant from principal P
	c, _, _ := w.actor("c")  // third actor, re-delegated to by A
	stranger, _, _ := w.actor("stranger")
	_, pk, _ := ed25519.GenerateKey(rand.Reader)
	p := cert.NewEdSigner(pk) // principal B consented to (no runtime needed)

	B := b.ID()
	b.AcceptTable["echo"] = echo
	b.AcceptTable["admin"] = func(context.Context, *actor.Invocation) ([]byte, error) {
		return []byte("admin ok"), nil
	}

	consent := issue(t, bs, string(p.ActorID()), []cert.ActorID{B}, []string{"echo", "admin"}, true, now-30, now+3600)
	b.Consents = []cert.Cert{consent}
	grantPA := issue(t, p, string(as.ActorID()), []cert.ActorID{B}, []string{"echo", "admin"}, true, now-20, now+3600)
	a.Grant(B, "echo", grantPA)
	a.Grant(B, "admin", grantPA)
	grantAC := issue(t, as, string(c.ID()), []cert.ActorID{B}, []string{"echo"}, false, now-10, now+3600)
	c.Grant(B, "echo", grantPA, grantAC)
	c.Grant(B, "admin", grantPA, grantAC)

	// Callers learn where B is (location record, not DNS/pkarr).
	w.introduce(b, a, c, stranger)

	w.start(b)
	w.start(a)
	w.start(c)
	w.start(stranger)

	// 2-link chain: consent(B→P) · grant(P→A), signer A.
	rep, err := a.Send(w.ctx, B, "echo", []byte("hello"))
	if err != nil {
		t.Fatalf("A→B echo: %v", err)
	}
	if string(rep.Payload) != "hello" || rep.Status.Code != actor.StatusOK || rep.Wire.From != B {
		t.Fatalf("bad reply: %+v", rep)
	}
	// B's reply piggybacked its location; A's envelope piggybacked A's.
	if loc := b.GetLocation(a.ID()); loc == nil || !strings.HasPrefix(loc.Cav.Endpoints[0], "iroh:") {
		t.Fatalf("B did not cache A's iroh location: %+v", loc)
	}
	if rep, err := a.Send(w.ctx, B, "admin", nil); err != nil || string(rep.Payload) != "admin ok" {
		t.Fatalf("A→B admin: %v %+v", err, rep)
	}

	// 3-link chain: consent · grant(P→A) · grant(A→C), signer C.
	if rep, err := c.Send(w.ctx, B, "echo", []byte("from c")); err != nil || string(rep.Payload) != "from c" {
		t.Fatalf("C→B echo: %v %+v", err, rep)
	}
	_, err = c.Send(w.ctx, B, "admin", nil)
	wantRemote(t, err, actor.StatusUnauthorized)

	_, err = a.Send(w.ctx, B, "nope", nil)
	wantRemote(t, err, actor.StatusUnauthorized)

	_, err = stranger.Send(w.ctx, B, "echo", nil)
	wantRemote(t, err, actor.StatusUnauthorized)

	a.Grant(B, "missing", grantPA)
	_, err = a.Send(w.ctx, B, "missing", nil)
	wantRemote(t, err, actor.StatusUnauthorized)
	delete(b.AcceptTable, "echo")
	_, err = a.Send(w.ctx, B, "echo", nil)
	wantRemote(t, err, actor.StatusUnknownFacet)

	if lw := b.LowWater(); lw != grantAC.Iat {
		t.Fatalf("low-water mark %d, want max rooted iat %d", lw, grantAC.Iat)
	}

	// B, now knowing A from the piggyback, can call back with no
	// pre-seeded hints: the cached record drives Dial.
	a.AcceptTable["ping"] = echo
	a.Consents = []cert.Cert{issue(t, as, string(B), []cert.ActorID{a.ID()}, []string{"ping"}, false, now-1, now+3600)}
	if rep, err := b.Send(w.ctx, a.ID(), "ping", []byte("back")); err != nil || string(rep.Payload) != "back" {
		t.Fatalf("B→A ping: %v %+v", err, rep)
	}
}

// TestTwoActorsHandshakeDirect: RelayMode::Disabled, direct 127.0.0.1
// sockets from the reach-me-at records.
func TestTwoActorsHandshakeDirect(t *testing.T) {
	runHandshake(t, "")
}

// TestTwoActorsHandshakeRelay: RelayMode::Custom(local iroh-relay --dev),
// records carry only iroh:relay=, so the first packets go through the
// relay (iroh then hole-punches to loopback). Needs IROH_RELAY_BIN or
// iroh-relay on PATH; skipped otherwise.
func TestTwoActorsHandshakeRelay(t *testing.T) {
	runHandshake(t, startRelay(t))
}

// ---- relay helper (mirrors iroh-go/cmd/smoke/smoke_test.go) ----------------

func startRelay(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("IROH_RELAY_BIN")
	if bin == "" {
		p, err := exec.LookPath("iroh-relay")
		if err != nil {
			t.Skip("iroh-relay not available (set IROH_RELAY_BIN); relay path not tested")
		}
		bin = p
	}
	port := freePort(t)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := filepath.Join(t.TempDir(), "relay.toml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(
		"http_bind_addr = \"127.0.0.1:%d\"\nenable_metrics = false\nenable_quic_addr_discovery = false\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--dev", "--config-path", cfg)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", port))
	return url
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitListening(t *testing.T, addr string) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("relay did not listen on %s", addr)
}
