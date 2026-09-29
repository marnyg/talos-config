//go:build iroh

// Command spawn is the v0 parent (protocol ADR-0008/0009, 0bc.4.6): a
// laptop actor that rents one child from a provisioner and watches it
// live and lapse. It is the acceptance run's instrument, not a
// product — one spawn per process, everything logged.
//
//	spawn -state DIR -print-id
//	spawn -state DIR -provisioner location.json -image ghcr.io/…@sha256:…
//
// Identity: -state DIR holds the key (a 32-byte seed, minted on first
// run). It persists because the provisioner's -customer consent names
// this id at ITS start: print the id first, start the provisioner with
// it, then spawn. Nothing else in DIR.
//
// Finding the provisioner: -provisioner is the location.json its
// process writes (a signed, expiring reach-me-at), copied out of band —
// v0's answer to discovery; after the first reply piggyback keeps it
// fresh. Its iss is the provisioner's id.
//
// Life of the run: #spawn → the child knocks on #birth (the promise
// resolves: id, location, lease) → one #ping on the child's own
// consent (P→C works) → the child's beat re-#renews its kit chain and
// the spawner's decorator turns each later exp into #extend at the
// provisioner. After -renewals of those this parent STOPS answering
// #renew (a grantor is free not to re-issue; revocation is expiry),
// keeps sweeping on -beat, and exits 0 when its born table no longer
// holds the child — the lease's last asked deadline passed, which is
// when the child exits itself (child.ErrLapsed) and the provisioner
// sweeps it. -renewals 0 renews until SIGINT.
//
// Clocks: -kit-ttl must exceed -window for #extend to fire on the
// child's first beat (the lease's first deadline is the window's end,
// and #extend only fires when a re-issued exp passes it).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/actors/keyfile"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/spawn"
)

const keyFile = "key"

func main() {
	log.SetFlags(0)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	var (
		stateDir = flag.String("state", filepath.Join(os.Getenv("HOME"), ".sap-parent"), "state dir: key (persisted identity)")
		printID  = flag.Bool("print-id", false, "print this parent's actor id and exit (the provisioner's -customer)")
		provFile = flag.String("provisioner", "", "the provisioner's location.json (its signed reach-me-at)")
		image    = flag.String("image", "", "child image by content digest (name@sha256:…)")
		relay    = flag.String("relay", "https://marnyg-talos-config.fly.dev", "iroh home relay URL")
		bindAddr = flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
		window   = flag.Duration("window", 3*time.Minute, "birth window: how long the child has to knock (pull time included)")
		kitTTL   = flag.Duration("kit-ttl", 5*time.Minute, "lifetime of the child's #renew chain per issue; keep above -window")
		renewals = flag.Int("renewals", 1, "answered #renew→#extend rounds before this parent stops re-issuing; 0 = forever")
		beat     = flag.Duration("beat", 30*time.Second, "sweep + reach-me-at interval")
		locTTL   = flag.Duration("location-ttl", 10*time.Minute, "lifetime of each published reach-me-at; keep well above -beat")
	)
	flag.Parse()
	irohtransport.SetLogLevel(os.Getenv("SAP_LOG")) // trace|debug|info|warn

	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		log.Fatal(err)
	}
	priv, minted, err := keyfile.Load(filepath.Join(*stateDir, keyFile))
	if err != nil {
		log.Fatal(err)
	}
	signer := cert.NewEdSigner(priv)
	if *printID {
		fmt.Println(signer.ActorID())
		return
	}
	if *provFile == "" || *image == "" {
		log.Fatal("need -provisioner location.json and -image name@sha256:… (or -print-id)")
	}
	raw, err := os.ReadFile(*provFile)
	if err != nil {
		log.Fatal(err)
	}
	provLoc, err := cert.DecodeCert(raw)
	if err != nil {
		log.Fatalf("%s: %v", *provFile, err)
	}
	prov := provLoc.Iss

	ep, err := irohtransport.Bind(priv, irohtransport.Options{BindAddr: *bindAddr, Relay: *relay})
	if err != nil {
		log.Fatal(err)
	}
	defer ep.Close()
	a := actor.New(signer, ep)
	a.SeqBase = func() int64 { return time.Now().UnixMicro() }
	if minted {
		slog.Info("parent: key minted", "state", *stateDir)
	}
	if provLoc.Exp <= a.Now() {
		slog.Warn("parent: provisioner location expired; the #spawn will likely not reach it", "exp", provLoc.Exp)
	}
	if err := a.UpdateLocation(prov, &provLoc); err != nil {
		log.Fatalf("%s: %v", *provFile, err)
	}

	sp := spawn.New(a)
	sp.Provisioner = prov
	sp.Window = int64(window.Seconds())
	sp.KitTTL = int64(kitTTL.Seconds())
	var extends atomic.Int64
	deaf := gate(a, sp, *renewals, &extends)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	listen := make(chan error, 1)
	go func() { listen <- a.Listen(ctx) }()
	publish := func() {
		if _, err := a.PublishLocation(int64(locTTL.Seconds())); err != nil {
			slog.Warn("parent: location", "err", err)
		}
	}
	publish()
	slog.Info("parent: up", "id", a.ID(), "provisioner", prov, "relay", *relay)

	// #spawn, then the birth.
	sctx, cancel := context.WithTimeout(ctx, *window)
	pr, err := sp.Spawn(sctx, spawn.Spec{Image: *image})
	if err != nil {
		cancel()
		log.Fatalf("parent: #spawn: %v", err)
	}
	slog.Info("parent: #spawn accepted; waiting for #birth", "window", *window)
	b, err := pr.Wait(sctx)
	cancel()
	if err != nil {
		log.Fatalf("parent: birth: %v", err)
	}
	_, until, _ := sp.Child(b.ID)
	slog.Info("parent: born", "child", b.ID, "lease", b.Lease.ID, "until", until, "location", b.Location != nil)

	// P→C on the child's own consent. The promise resolved at #birth,
	// before Born installed that consent on the child: retry briefly.
	ping(ctx, a, b.ID)

	t := time.NewTicker(*beat)
	defer t.Stop()
	last := until
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-listen:
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Fatal(err)
			}
			break loop
		case <-t.C:
		}
		sp.Sweep()
		publish()
		_, until, ok := sp.Child(b.ID)
		switch {
		case !ok:
			slog.Info("parent: lapsed — the child's last deadline passed", "child", b.ID, "extends", extends.Load())
			break loop
		case until != last:
			last = until
			slog.Info("parent: lease", "child", b.ID, "until", until, "in", time.Until(time.Unix(until, 0)).Round(time.Second), "extends", extends.Load(), "renewing", !deaf.Load())
		}
	}
	stop()
	slog.Info("parent: exit", "extends", extends.Load())
}

// gate wraps the parent's #renew handler (the spawner's decorator
// included, so an answered round has already sent its #extend): each
// call that moves the caller's lease deadline counts as one extend, and
// once n of them happened the handler refuses instead. n <= 0 never
// refuses. Returned is the flag that says it has gone deaf.
func gate(a *actor.Actor, sp *spawn.Spawner, n int, extends *atomic.Int64) *atomic.Bool {
	deaf := new(atomic.Bool)
	inner := a.AcceptTable[actor.FacetRenew]
	a.AcceptTable[actor.FacetRenew] = func(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
		if deaf.Load() {
			slog.Info("parent: #renew refused (done renewing)", "from", inv.From)
			return nil, errors.New("parent: not renewing")
		}
		_, before, _ := sp.Child(inv.From)
		body, err := inner(ctx, inv)
		if err != nil {
			return body, err
		}
		_, after, ok := sp.Child(inv.From)
		if ok && after > before {
			k := extends.Add(1)
			slog.Info("parent: #renew answered → #extend", "child", inv.From, "until", after, "round", k)
			if n > 0 && k >= int64(n) {
				deaf.Store(true)
				slog.Info("parent: done renewing; the child lapses when its chain expires", "child", inv.From, "chain_exp", after)
			}
		} else {
			slog.Info("parent: #renew answered, no extend (exp not past the lease deadline)", "child", inv.From)
		}
		return body, nil
	}
	return deaf
}

// ping proves P→C once: the child's #ping under its own consent, empty
// chain. Failure is logged, not fatal — the run is about the lease.
func ping(ctx context.Context, a *actor.Actor, id cert.ActorID) {
	deadline := time.Now().Add(30 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		var rep *actor.Reply
		if rep, err = a.Send(ctx, id, child.FacetPing, []byte(`"hi"`)); err == nil {
			slog.Info("parent: #ping ok", "child", id, "reply", string(rep.Payload))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	slog.Warn("parent: #ping failed", "child", id, "err", err)
}
