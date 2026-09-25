//go:build iroh

// Command provisioner is the v0 provisioner actor (protocol ADR-0009,
// 0bc.4.5): protocol/provisioner over one platform driver, on iroh.
// Three facets — #spawn, #extend, #kill — and nothing else; it never
// reads the intro it injects and never learns whether a birth
// happened. One binary, the driver picked by flag:
//
//	provisioner -driver k8s    -customer ed:…   # in a pod: Jobs in the pod's namespace
//	provisioner -driver docker -customer ed:…   # on a host: containers on its daemon
//
// Identity: -state DIR holds the key (a 32-byte seed, minted on first
// run — a provisioner's id must outlive its process, customers hold
// chains to it). Everything else in DIR is derived and safe to lose:
// location.json is the current signed reach-me-at, rewritten each
// beat, for a customer to load out of band (v0's "how do I find the
// provisioner" — replies piggyback fresh ones after that).
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
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/driver/docker"
	"github.com/marnyg/talos-config/actors/driver/k8s"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/provisioner"
	"github.com/marnyg/talos-config/protocol/spawn"
)

const (
	keyFile      = "key"
	locationFile = "location.json"
)

type customers []cert.ActorID

func (c *customers) String() string { return fmt.Sprint([]cert.ActorID(*c)) }
func (c *customers) Set(s string) error {
	id := cert.ActorID(strings.TrimSpace(s))
	if err := id.Validate(); err != nil {
		return err
	}
	*c = append(*c, id)
	return nil
}

func main() {
	log.SetFlags(0)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	var cust customers
	var (
		drv        = flag.String("driver", "", "platform: k8s (in-cluster, the pod's namespace) or docker (the local daemon)")
		stateDir   = flag.String("state", "/var/lib/sap-provisioner", "state dir: key (persisted identity), location.json (derived)")
		relay      = flag.String("relay", "https://marnyg-talos-config.fly.dev", "iroh home relay URL")
		bindAddr   = flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
		namespace  = flag.String("namespace", "", "k8s: namespace for Jobs (default the pod's own)")
		dockerHost = flag.String("docker-host", "", "docker: daemon (default DOCKER_HOST, then unix:///var/run/docker.sock)")
		custTTL    = flag.Duration("customer-ttl", 365*24*time.Hour, "lifetime of each -customer consent, from start")
		beat       = flag.Duration("beat", time.Minute, "sweep + reach-me-at interval")
		locTTL     = flag.Duration("location-ttl", 10*time.Minute, "lifetime of each published reach-me-at; keep well above -beat")
		adoptGrace = flag.Duration("adopt-grace", provisioner.DefaultAdoptGrace, "deadline given to leases adopted at start")
		drvTimeout = flag.Duration("driver-timeout", provisioner.DefaultDriverTimeout, "bound on each driver call (docker pulls inside Start)")
	)
	flag.Var(&cust, "customer", "actor id consented to #spawn/#extend/#kill; repeatable")
	flag.Parse()
	irohtransport.SetLogLevel(os.Getenv("SAP_LOG")) // trace|debug|info|warn
	if len(cust) == 0 {
		log.Fatal("no -customer: nobody could #spawn")
	}

	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		log.Fatal(err)
	}
	priv, minted, err := loadKey(filepath.Join(*stateDir, keyFile))
	if err != nil {
		log.Fatal(err)
	}
	ep, err := irohtransport.Bind(priv, irohtransport.Options{BindAddr: *bindAddr, Relay: *relay})
	if err != nil {
		log.Fatal(err)
	}
	defer ep.Close()
	a := actor.New(cert.NewEdSigner(priv), ep)
	a.SeqBase = func() int64 { return time.Now().UnixMicro() }
	if minted {
		slog.Info("provisioner: key minted", "state", *stateDir)
	}
	slog.Info("provisioner: up", "id", a.ID(), "driver", *drv, "relay", *relay)

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

	var d provisioner.Driver
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
			err = os.WriteFile(filepath.Join(*stateDir, locationFile), raw, 0o644)
		}
		if err != nil {
			slog.Warn("provisioner: location.json", "err", err)
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

// loadKey reads the 32-byte seed at path, or mints and writes one.
func loadKey(path string) (ed25519.PrivateKey, bool, error) {
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(b) != ed25519.SeedSize {
			return nil, false, fmt.Errorf("%s: %d bytes, want %d", path, len(b), ed25519.SeedSize)
		}
		return ed25519.NewKeyFromSeed(b), false, nil
	case errors.Is(err, os.ErrNotExist):
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(path, seed, 0o600); err != nil {
			return nil, false, err
		}
		return ed25519.NewKeyFromSeed(seed), true, nil
	default:
		return nil, false, err
	}
}
