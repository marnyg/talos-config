//go:build iroh

package relaygate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/actors/keyfile"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
	"github.com/marnyg/talos-config/protocol/provisioner"
	"github.com/marnyg/talos-config/protocol/spawn"
)

// party is one key seen both ways: the cert signer and the iroh
// endpoint are the same Ed25519 key, which is the whole point.
type party struct {
	priv ed25519.PrivateKey
	cert.EdSigner
}

func newParty(t *testing.T) party {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return party{priv, cert.NewEdSigner(priv)}
}

// bind homes p on relayURL, claiming claim ("" ⇒ its own id).
func (p party) bind(t *testing.T, relayURL string, claim cert.ActorID) *irohtransport.Endpoint {
	t.Helper()
	if claim == "" {
		claim = p.ActorID()
	}
	e, err := irohtransport.Bind(p.priv, irohtransport.Options{BindAddr: "127.0.0.1:0", Relay: relayURL, RelayToken: Claim(claim)})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID() != p.ActorID() {
		t.Fatalf("endpoint id %s != cert id %s", e.ID(), p.ActorID())
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func online(e *irohtransport.Endpoint, d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if e.Online(ctx) != nil {
		return false
	}
	tags := e.Endpoints()
	return len(tags) > 0 && strings.HasPrefix(tags[0], irohtransport.TagRelay)
}

// echoOnce dials to through the relay hint alone and exchanges one
// message.
func echoOnce(from, to *irohtransport.Endpoint, relayURL string, d time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	srv := make(chan error, 1)
	go func() {
		s, _, err := to.Accept(ctx)
		if err != nil {
			srv <- err
			return
		}
		defer s.Close()
		msg, err := s.RecvMsg(ctx)
		if err != nil {
			srv <- err
			return
		}
		srv <- s.SendMsg(ctx, append([]byte("re:"), msg...))
	}()
	s, err := from.Dial(ctx, to.ID(), []string{irohtransport.TagRelay + relayURL})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer s.Close()
	if err := s.SendMsg(ctx, []byte("beat")); err != nil {
		return err
	}
	got, err := s.RecvMsg(ctx)
	if err != nil || string(got) != "re:beat" {
		return fmt.Errorf("reply %q %v", got, err)
	}
	return <-srv
}

// TestGateInFrontOfRelay is ADR-0012 end to end against the real relay
// binary and real endpoints: certs in, connections out.
func TestGateInFrontOfRelay(t *testing.T) {
	ctx := context.Background()
	relay, member := newParty(t), newParty(t)
	now := time.Now().Unix()
	sign := func(iss party, aud string, cav cert.Caveats, ttl int64) cert.Cert {
		cav.Target = []cert.ActorID{relay.ActorID()}
		cav.Facet = []string{Facet}
		c, err := cert.Sign(cert.Cert{Aud: aud, Can: cert.VerbRelay, Cav: cav, Iat: time.Now().Unix(), Exp: time.Now().Unix() + ttl}, iss.EdSigner)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	consent, err := cert.Sign(cert.Cert{Aud: string(member.ActorID()), Can: cert.VerbRelay,
		Cav: cert.Caveats{Target: []cert.ActorID{relay.ActorID()}, Facet: []string{Facet}, Delegable: true},
		Iat: now - 1, Exp: now + 3600}, relay.EdSigner)
	if err != nil {
		t.Fatal(err)
	}

	svc := startService(t, New(relay.ActorID(), []cert.Cert{consent}), nil)
	public := httptest.NewServer(svc.Handler())
	t.Cleanup(public.Close)
	at := public.URL // the only address an actor is given

	present := func(who party, chain ...cert.Cert) {
		t.Helper()
		p, err := Prepare(ctx, nil, relay.ActorID(), who.ActorID(), chain)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Post(ctx, nil, at, p); err != nil {
			t.Fatalf("present: %v", err)
		}
	}

	// member: nothing presented.
	m := member.bind(t, at, "")
	if !online(m, 10*time.Second) {
		t.Fatal("member: no home relay")
	}

	// stranger: nothing to present.
	if s := newParty(t).bind(t, at, ""); online(s, 3*time.Second) {
		t.Fatal("stranger: got a home relay")
	} else {
		_ = s.Close()
	}

	// child: refused until it presents its parent's link, then in by
	// itself, on the endpoint it already had.
	child := newParty(t)
	c := child.bind(t, at, "")
	if online(c, 3*time.Second) {
		t.Fatal("child before presenting: got a home relay")
	}
	present(child, sign(member, string(child.ActorID()), cert.Caveats{}, 600))
	if !online(c, 15*time.Second) {
		t.Fatal("child after presenting: no home relay")
	}
	if err := echoOnce(c, m, at, 10*time.Second); err != nil {
		t.Fatalf("child → member through the gated relay: %v", err)
	}

	// Somebody else claiming the child's id: the proxy lets the claim
	// through (the child IS admitted), the relay's hook does not.
	if th := newParty(t).bind(t, at, child.ActorID()); online(th, 3*time.Second) {
		t.Fatal("another key claiming the child's id: got a home relay")
	} else {
		_ = th.Close()
	}

	// newborn: the wildcard link from its intro, stamped for its key.
	birth := sign(member, "*", cert.Caveats{Postage: postage.Require(10)}, 120)
	newborn := newParty(t)
	present(newborn, birth)
	n := newborn.bind(t, at, "")
	if !online(n, 10*time.Second) {
		t.Fatal("newborn on its intro's link: no home relay")
	}
	if err := echoOnce(n, m, at, 10*time.Second); err != nil {
		t.Fatalf("newborn → parent (the #birth knock's path): %v", err)
	}

	// expiry: a 3 s credential. The proxy ends the connection, the
	// endpoint is unreachable until its chain is renewed and presented.
	short := newParty(t)
	present(short, sign(member, string(short.ActorID()), cert.Caveats{}, 3))
	d := short.bind(t, at, "")
	if !online(d, 5*time.Second) {
		t.Fatal("short-lived child: no home relay")
	}
	time.Sleep(4 * time.Second)
	if err := echoOnce(m, d, at, 3*time.Second); err == nil {
		t.Fatal("reached an endpoint whose credential expired a second ago")
	}
	present(short, sign(member, string(short.ActorID()), cert.Caveats{}, 600))
	began := time.Now()
	for {
		if err := echoOnce(m, d, at, 3*time.Second); err == nil {
			break
		} else if time.Since(began) > 20*time.Second {
			t.Fatalf("renewed and presented, still unreachable: %v", err)
		}
	}
	t.Logf("expired endpoint reachable again %s after presenting its renewed chain", time.Since(began).Round(100*time.Millisecond))
}

// syncBuf is a log sink two goroutines can share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// container is what cmd/child does, in-process: mint a key, present
// the intro's relay link, bind claiming that key, and live (child.Run).
// mute is a child that never shows the gate its kit's chain.
type containers struct {
	ctx  context.Context
	at   string
	mu   sync.Mutex
	n    int
	errs chan error
}

func (c *containers) Start(_ context.Context, spec provisioner.StartSpec) (provisioner.Handle, error) {
	c.mu.Lock()
	c.n++
	mute := c.n == 2
	c.mu.Unlock()
	go func() {
		if err := c.run(spec.Params, mute); err != nil && c.ctx.Err() == nil {
			c.errs <- err
		}
	}()
	return provisioner.Handle("ctr-" + spec.Lease), nil
}
func (c *containers) Extend(context.Context, provisioner.Handle, int64) error { return nil }
func (c *containers) Kill(context.Context, provisioner.Handle) error          { return nil }
func (c *containers) List(context.Context) ([]provisioner.Running, error)     { return nil, nil }

func (c *containers) run(params []byte, mute bool) error {
	in, err := spawn.DecodeIntro(params)
	if err != nil {
		return err
	}
	if in.Relay == nil {
		return fmt.Errorf("intro without a relay link")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	signer := cert.NewEdSigner(priv)
	present := Presenter(nil, c.at, in.Relay.Cav.Target[0], signer.ActorID())
	if err := PresentUntil(c.ctx, present, []cert.Cert{*in.Relay}, 200*time.Millisecond); err != nil {
		return fmt.Errorf("intro link: %w", err)
	}
	ep, err := irohtransport.Bind(priv, irohtransport.Options{BindAddr: "127.0.0.1:0", Relay: c.at, RelayToken: Claim(signer.ActorID())})
	if err != nil {
		return err
	}
	defer ep.Close()
	if err := ep.Online(c.ctx); err != nil {
		return fmt.Errorf("newborn never got a home relay: %w", err)
	}
	a := actor.New(signer, ep)
	go func() { _ = a.Listen(c.ctx) }()
	o := child.Options{Beat: time.Second, Present: present}
	if mute {
		o.Present = func(context.Context, []cert.Cert) error { return nil }
	}
	if err := child.Run(c.ctx, a, in, o); err != nil && c.ctx.Err() == nil {
		return err
	}
	return nil
}

// TestBornThroughTheGate is ADR-0012 whole: a real Spawner whose
// network gates its relay, a provisioner, and children that do what
// cmd/child does — all on the real relay behind the gate. A child is
// born on the wildcard link in its intro and outlives the birth window
// on the chain from its kit; a child that never presents that chain is
// cut off when the window ends.
func TestBornThroughTheGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	relay, parent, prov := newParty(t), newParty(t), newParty(t)
	now := time.Now().Unix()
	member := func(p party, delegable bool) cert.Cert {
		c, err := cert.Sign(cert.Cert{Aud: string(p.ActorID()), Can: cert.VerbRelay,
			Cav: cert.Caveats{Target: []cert.ActorID{relay.ActorID()}, Facet: []string{Facet}, Delegable: delegable},
			Iat: now - 1, Exp: now + 3600}, relay.EdSigner)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	var logs syncBuf
	gate := New(relay.ActorID(), []cert.Cert{member(parent, true), member(prov, false)})
	svc := startService(t, gate, slog.New(slog.NewTextHandler(&logs, nil)))
	public := httptest.NewServer(svc.Handler())
	t.Cleanup(public.Close)
	at := public.URL

	// The two members: bound, online, then wired, then listening.
	up := func(p party) (*actor.Actor, *irohtransport.Endpoint) {
		ep := p.bind(t, at, "")
		if !online(ep, 10*time.Second) {
			t.Fatal("member: no home relay")
		}
		return actor.New(p.EdSigner, ep), ep
	}
	pa, _ := up(parent)
	va, _ := up(prov)
	rent, err := cert.Sign(cert.Cert{Aud: string(parent.ActorID()), Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{prov.ActorID()}, Facet: []string{spawn.FacetSpawn, spawn.FacetExtend, spawn.FacetKill}},
		Iat: now - 1, Exp: now + 3600}, prov.EdSigner)
	if err != nil {
		t.Fatal(err)
	}
	va.Consents = []cert.Cert{rent}
	boxes := &containers{ctx: ctx, at: at, errs: make(chan error, 4)}
	provisioner.New(va, boxes)
	sp := spawn.New(pa)
	sp.Provisioner = prov.ActorID()
	sp.Relay = relay.ActorID()
	sp.Postage = postage.Require(8)
	sp.Window = 5
	sp.KitTTL = 8
	for _, a := range []*actor.Actor{pa, va} {
		go func() { _ = a.Listen(ctx) }()
		if _, err := a.PublishLocation(600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pa.UpdateLocation(va.ID(), va.CurrentLocation()); err != nil {
		t.Fatal(err)
	}

	image := "img@sha256:" + strings.Repeat("a", 64)
	bear := func() cert.ActorID {
		t.Helper()
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		pr, err := sp.Spawn(sctx, spawn.Spec{Image: image})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		done := make(chan struct{})
		var b spawn.Birth
		go func() { b, err = pr.Wait(sctx); close(done) }()
		select {
		case <-done:
		case cerr := <-boxes.errs:
			t.Fatalf("the child failed: %v", cerr)
		}
		if err != nil {
			t.Fatalf("birth through the gated relay: %v", err)
		}
		return b.ID
	}
	began := time.Now()
	kept := bear() // presents its kit's chain
	cut := bear()  // never does
	t.Logf("two children born through the gate in %s", time.Since(began).Round(100*time.Millisecond))
	if _, ok := gate.Admitted(kept); !ok {
		t.Fatal("a born child is not admitted")
	}

	// Past both birth windows.
	time.Sleep(time.Duration(sp.Window+3)*time.Second - time.Since(began))
	select {
	case err := <-boxes.errs:
		t.Fatalf("a child died: %v", err)
	default:
	}
	until, ok := gate.Admitted(kept)
	if !ok {
		t.Fatal("the child that presents its chain is not admitted past the birth window")
	}
	if until <= now+sp.Window+3 {
		t.Fatalf("admitted until %d: its renewed chain is not reaching the gate", until)
	}
	if _, ok := gate.Admitted(cut); ok {
		t.Fatal("the child that never presented its chain is still admitted past the birth window")
	}
	ended := func(id cert.ActorID) bool {
		for _, line := range strings.Split(logs.String(), "\n") {
			if strings.Contains(line, "connection ended") && strings.Contains(line, string(id)) {
				return true
			}
		}
		return false
	}
	// (On loopback the silent child still reaches its parent directly
	// and keeps renewing; what it has lost is the relay, which is what
	// the gate's own log says.)
	if !ended(cut) {
		t.Fatalf("the gate did not end the silent child's connection; its log:\n%s", logs.String())
	}
	if ended(kept) {
		t.Fatalf("the gate ended the connection of a child that kept presenting; its log:\n%s", logs.String())
	}
	pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
	defer pcancel()
	if rep, err := pa.Send(pctx, kept, child.FacetPing, []byte(`"still here"`)); err != nil || string(rep.Payload) != `"still here"` {
		t.Fatalf("parent → child past the birth window: %v", err)
	}
}

// TestLiveNetwork runs against a DEPLOYED network — through the
// platform's TLS proxy, which no other test here crosses. Skipped
// unless told where:
//
//	SAP_NET_URL         the relay's public url (https://…)
//	SAP_NET_ID          the lighthouse's id (the gate's root)
//	SAP_NET_MEMBER_KEY  key file (actors/keyfile) of one of its -member
//
// It checks what a deploy can break: the upgrade and the id claim
// survive the proxy, the hook admits a member, POST /relay/admit takes
// a child's chain and a newborn's stamped link, a stranger stays out,
// and two admitted endpoints reach each other through the relay.
func TestLiveNetwork(t *testing.T) {
	at, root, keyPath := os.Getenv("SAP_NET_URL"), cert.ActorID(os.Getenv("SAP_NET_ID")), os.Getenv("SAP_NET_MEMBER_KEY")
	if at == "" || root == "" || keyPath == "" {
		t.Skip("SAP_NET_URL, SAP_NET_ID and SAP_NET_MEMBER_KEY are not all set: no live network to check")
	}
	priv, _, err := keyfile.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	member := party{priv, cert.NewEdSigner(priv)}
	ctx := context.Background()
	sign := func(aud string, cav cert.Caveats, ttl int64) cert.Cert {
		cav.Target, cav.Facet = []cert.ActorID{root}, []string{Facet}
		c, err := cert.Sign(cert.Cert{Aud: aud, Can: cert.VerbRelay, Cav: cav, Iat: time.Now().Unix(), Exp: time.Now().Unix() + ttl}, member.EdSigner)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	m := member.bind(t, at, "")
	if !online(m, 30*time.Second) {
		t.Fatal("member: no home relay — is this key a -member of the deployed lighthouse?")
	}
	t.Logf("member online at %v", m.Endpoints()[0])

	if s := newParty(t).bind(t, at, ""); online(s, 5*time.Second) {
		t.Fatal("stranger: got a home relay")
	} else {
		_ = s.Close()
	}

	child := newParty(t)
	began := time.Now()
	if err := Presenter(nil, at, root, child.ActorID())(ctx, []cert.Cert{sign(string(child.ActorID()), cert.Caveats{}, 120)}); err != nil {
		t.Fatalf("child presents its chain: %v", err)
	}
	c := child.bind(t, at, "")
	if !online(c, 30*time.Second) {
		t.Fatal("child after presenting: no home relay")
	}
	t.Logf("child presented and online in %s", time.Since(began).Round(10*time.Millisecond))
	if err := echoOnce(c, m, at, 20*time.Second); err != nil {
		t.Fatalf("child → member through the deployed relay: %v", err)
	}

	newborn := newParty(t)
	if err := Presenter(nil, at, root, newborn.ActorID())(ctx, []cert.Cert{sign("*", cert.Caveats{Postage: postage.Require(12)}, 60)}); err != nil {
		t.Fatalf("newborn presents a stamped wildcard link: %v", err)
	}
	n := newborn.bind(t, at, "")
	if !online(n, 30*time.Second) {
		t.Fatal("newborn on a wildcard link: no home relay")
	}
	if err := echoOnce(n, m, at, 20*time.Second); err != nil {
		t.Fatalf("newborn → member (the #birth knock's path): %v", err)
	}

	// A chain the gate must refuse: its verdict has to come back
	// through the proxy as a verdict.
	other := newParty(t)
	err = Presenter(nil, at, root, other.ActorID())(ctx, []cert.Cert{sign(string(child.ActorID()), cert.Caveats{}, 120)})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("another key's chain: err = %v, want ErrRefused", err)
	}
}
