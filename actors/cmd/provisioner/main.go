//go:build iroh

// Command provisioner is the v0 provisioner actor (protocol ADR-0009,
// 0bc.4.5): protocol/provisioner over one platform driver, on iroh.
// Three facets — #spawn, #extend, #kill — and nothing else; it never
// reads the intro it injects and never learns whether a birth
// happened. One binary, the driver picked by flag:
//
//	provisioner -driver k8s    -customer ed:…   # in a pod: Jobs in the pod's namespace
//	provisioner -driver docker -customer ed:…   # on a host: containers on its daemon
//	provisioner -state DIR -print-id            # the id a lighthouse names in -member
//
// Identity: -state DIR holds the key (a 32-byte seed, minted on first
// run — a provisioner's id must outlive its process, customers hold
// chains to it). Everything else in DIR is derived and safe to lose:
// location.json is the current signed reach-me-at, rewritten each
// beat, for a customer to load out of band (the no-lighthouse answer
// to "how do I find the provisioner" — replies piggyback fresh ones
// after that).
//
// Lighthouse: -lighthouse ID names a lighthouse this provisioner is a
// -member of (0bc.6, ADR-0007); every beat #publishes the fresh
// reach-me-at there, so a customer finds it by #lookup with no file
// copied. The lighthouse is dialled on the relay alone the first time
// (actor.Bootstrap; the reply piggybacks its record). The consent is
// the lighthouse's, presented empty.
//
// Customers: -customer ID (repeatable) mints, at start, one consent
// {iss: me, aud: ID, invoke, cav: {target: [me], facet: [#spawn,
// #extend, #kill]}} for -customer-ttl — the one root over three facets
// (ADR-0009's open item, v0 answer). A customer presents it empty.
// Revocation is expiry or a restart without the flag.
//
// Beat: provisioner.Sweep (lapses whatever a parent stopped
// extending; on docker this IS the deadline) and a fresh reach-me-at.
// Restart: Adopt before Listen re-takes every labelled container the
// driver finds (decision uzgl).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/cmd/internal/boot"
	"github.com/marnyg/talos-config/actors/driver/docker"
	"github.com/marnyg/talos-config/actors/driver/k8s"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/lighthouse"
	"github.com/marnyg/talos-config/protocol/provisioner"
	"github.com/marnyg/talos-config/protocol/spawn"
)

const locationFile = "location.json" // derived, beside the key in -state

func main() {
	pb := boot.Flags("provisioner", "/var/lib/sap-provisioner")
	var cust boot.IDs
	var lhFlag boot.ID
	var (
		drv        = flag.String("driver", "", "platform: k8s (in-cluster, the pod's namespace) or docker (the local daemon)")
		namespace  = flag.String("namespace", "", "k8s: namespace for Jobs (default the pod's own)")
		dockerHost = flag.String("docker-host", "", "docker: daemon (default as the docker CLI: DOCKER_HOST, then the current docker context, then unix:///var/run/docker.sock)")
		custTTL    = flag.Duration("customer-ttl", 365*24*time.Hour, "lifetime of each -customer consent, from start")
		beat       = flag.Duration("beat", time.Minute, "sweep + reach-me-at interval")
		locTTL     = flag.Duration("location-ttl", 10*time.Minute, "lifetime of each published reach-me-at; keep well above -beat")
		adoptGrace = flag.Duration("adopt-grace", provisioner.DefaultAdoptGrace, "deadline given to leases adopted at start")
		drvTimeout = flag.Duration("driver-timeout", provisioner.DefaultDriverTimeout, "bound on each driver call (docker pulls inside Start)")
	)
	flag.Var(&cust, "customer", "actor id consented to #spawn/#extend/#kill; repeatable")
	flag.Var(&lhFlag, "lighthouse", "lighthouse actor id to #publish each reach-me-at to (this id must be one of its -member); dialled on -relay")
	flag.Parse()
	a, ep := pb.Up()
	defer ep.Close()
	if len(cust) == 0 {
		log.Fatal("no -customer: nobody could #spawn")
	}
	lh := lhFlag.ActorID()
	pb.ViaRelay(a, lh)
	slog.Info("provisioner: up", "id", a.ID(), "driver", *drv, "relay", pb.Relay(), "lighthouse", lh)

	now := a.Now()
	for _, id := range cust {
		c, err := cert.Sign(cert.Cert{
			Aud: string(id),
			Can: cert.VerbInvoke,
			Cav: cert.Caveats{Target: []cert.ActorID{a.ID()}, Facet: []string{spawn.FacetSpawn, spawn.FacetExtend, spawn.FacetKill}},
			Iat: now,
			Exp: now + int64(custTTL.Seconds()),
		}, a.Signer)
		if err != nil {
			log.Fatal(err)
		}
		a.Consents = append(a.Consents, c)
		slog.Info("provisioner: customer", "id", id, "exp", c.Exp)
	}

	var (
		d   provisioner.Driver
		err error
	)
	switch *drv {
	case "k8s":
		cfg, err := k8s.InCluster()
		if err != nil {
			log.Fatal(err)
		}
		if *namespace != "" {
			cfg.Namespace = *namespace
		}
		cfg.Now = a.Now
		if d, err = k8s.New(cfg); err != nil {
			log.Fatal(err)
		}
		slog.Info("provisioner: k8s", "host", cfg.Host, "namespace", cfg.Namespace)
	case "docker":
		if d, err = docker.New(docker.Config{Host: *dockerHost}); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("-driver must be k8s or docker, got %q", *drv)
	}
	p := provisioner.New(a, d)
	p.AdoptGrace = *adoptGrace
	p.DriverTimeout = *drvTimeout

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	publish := func() {
		loc, err := a.PublishLocation(int64(locTTL.Seconds()))
		if err != nil {
			slog.Warn("provisioner: location", "err", err)
			return
		}
		raw, err := cert.Encode(loc)
		if err == nil {
			err = os.WriteFile(filepath.Join(pb.State(), locationFile), raw, 0o644)
		}
		if err != nil {
			slog.Warn("provisioner: location.json", "err", err)
		}
		if lh == "" {
			return
		}
		pctx, cancel := context.WithTimeout(ctx, *beat/2)
		defer cancel()
		if err := lighthouse.Publish(pctx, a, lh, nil); err != nil {
			slog.Warn("provisioner: #publish", "lighthouse", lh, "err", err)
		} else {
			slog.Info("provisioner: #publish ok", "lighthouse", lh, "exp", loc.Exp)
		}
	}
	publish()
	if err := p.Adopt(ctx); err != nil {
		log.Fatal(err)
	}
	slog.Info("provisioner: adopted", "leases", len(p.Leases()))

	listen := make(chan error, 1)
	go func() { listen <- a.Listen(ctx) }()
	t := time.NewTicker(*beat)
	defer t.Stop()
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
			p.Sweep(ctx)
			publish()
		}
	}
	stop()
	slog.Info("provisioner: exit", "leases", len(p.Leases()))
}
