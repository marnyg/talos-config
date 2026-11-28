//go:build iroh

// Command lighthouse is the v0 rendezvous actor (protocol ADR-0007,
// 0bc.6): protocol/lighthouse on iroh. Two facets — #publish and
// #lookup — and nothing else. It holds a volatile directory of what
// members published and hands records back by id; it cannot forge one
// (a client re-validates every record's signature).
//
//	lighthouse -state DIR -print-id
//	lighthouse -state DIR -member ed:… [-member ed:…]
//
// Identity: -state DIR holds the key (a 32-byte seed, minted on first
// run — a lighthouse's id is what its members are handed as the network
// bundle, so it must outlive its process). Nothing else in DIR.
// -print-id prints it (minting the key if needed) and exits.
//
// Members: -member ID (repeatable) mints, at start, two consents
// {iss: me, aud: ID, publish, cav: {target: [me], facet: [#publish]}}
// and {…, invoke, facet: [#lookup]} for -member-ttl — the lighthouse is
// its own founder, the v0 shape of "a network" (the founder
// indirection, where a founder F mints publish-caps without the
// lighthouse restarting, is the upgrade path and needs no change here:
// a `-founder` flag would consent delegably to F instead). A member
// presents the consent empty. Revocation is expiry or a restart without
// the flag.
//
// Finding the lighthouse: a member needs only this id and the relay it
// shares with it — the network bundle's "lighthouse endpoints" are
// `iroh:relay=<url>`, which every actor already holds as -relay. The
// member seeds actor.Bootstrap with that hint; the first reply
// piggybacks the signed record and the hint goes unused.
//
// Beat: a fresh reach-me-at (piggybacked on every reply, so members
// keep a live record of the lighthouse) and a directory size log line.
//
// # The network's relay (-relay-bin, protocol ADR-0012, root ADR-0051)
//
//	lighthouse -state DIR -relay-bin /usr/local/bin/iroh-relay \
//	           -relay https://net.example -listen :8080 -member ed:…
//
// With -relay-bin this process IS the network's public surface: it
// runs that iroh-relay binary as a child on loopback, puts the
// admission gate (actors/relaygate) in front of it on -listen, and
// its own key roots the gate — one founder key per network. Then:
//
//   - -relay is the PUBLIC url of this relay: what members dial and
//     what this actor advertises. The actor itself homes on the child's
//     loopback listener, behind its own gate.
//   - each -member also gets {iss: me, aud: ID, can: relay, cav:
//     {target: [me], facet: [#relay], delegable}} for -member-ttl. A
//     member is admitted with nothing presented; being delegable, it
//     can admit the children it spawns (a parent's -relay-id is this
//     actor's id). Dropping a member, and every chain under it, is a
//     restart without the flag — which also empties the gate's table:
//     children present again on their next beat.
//   - TLS is the platform's, in front of -listen. The image must ship a
//     CA bundle: the relay will not start without one once it has an
//     access hook.
//
// Nothing here knows the talos hub, a cluster, or a wallet.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/cmd/internal/boot"
	"github.com/marnyg/talos-config/actors/relaygate"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/lighthouse"
)

func main() {
	p := boot.Flags("lighthouse", "/var/lib/sap-lighthouse")
	var mem boot.IDs
	var (
		memTTL     = flag.Duration("member-ttl", 365*24*time.Hour, "lifetime of each -member consent, from start")
		beat       = flag.Duration("beat", time.Minute, "reach-me-at interval")
		locTTL     = flag.Duration("location-ttl", 10*time.Minute, "lifetime of each published reach-me-at; keep well above -beat")
		maxRecords = flag.Int("max-records", lighthouse.DefaultMaxRecords, "directory cap: a new publisher is refused past it (<= 0 unbounded)")
		relayBin   = flag.String("relay-bin", "", "run this iroh-relay binary behind the admission gate and be the network's relay (ADR-0012): -relay is then this relay's PUBLIC url, -listen serves it, and each -member is also admitted to it")
		listenAddr = flag.String("listen", ":8080", "with -relay-bin: HTTP listen address of the gated relay (TLS is the platform's)")
	)
	flag.Var(&mem, "member", "actor id consented to #publish and #lookup — and, with -relay-bin, admitted to the relay (delegably: it can admit its children); repeatable")
	flag.Parse()
	priv := p.Key() // -print-id exits here
	if len(mem) == 0 {
		log.Fatal("no -member: nobody could #publish or #lookup")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var a *actor.Actor
	var ep *irohtransport.Endpoint
	var gate *relaygate.Gate
	if *relayBin == "" {
		a, ep = p.Up()
	} else {
		// The gate before the actor: the actor homes on the relay the
		// gate starts, and the gate needs only the key.
		signer := cert.NewEdSigner(priv)
		now := time.Now().Unix()
		var admit []cert.Cert
		for _, id := range mem {
			c, err := cert.Sign(cert.Cert{
				Aud: string(id),
				Can: cert.VerbRelay,
				Cav: cert.Caveats{Target: []cert.ActorID{signer.ActorID()}, Facet: []string{relaygate.Facet}, Delegable: true},
				Iat: now,
				Exp: now + int64(memTTL.Seconds()),
			}, signer)
			if err != nil {
				log.Fatal(err)
			}
			admit = append(admit, c)
		}
		gate = relaygate.New(signer.ActorID(), admit)
		svc, err := relaygate.Start(ctx, relaygate.Config{Gate: gate, Bin: *relayBin, RelayLog: os.Getenv("RELAY_LOG"), Log: slog.Default()})
		if err != nil {
			log.Fatal(err)
		}
		defer svc.Close()
		web := &http.Server{Addr: *listenAddr, Handler: svc.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := web.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("lighthouse: -listen %s: %v", *listenAddr, err)
			}
		}()
		defer web.Close()
		slog.Info("lighthouse: relay gated", "listen", *listenAddr, "public", p.Relay(), "members", gate.Members())
		a, ep = p.UpBehind(priv, svc.Backend())
	}
	defer ep.Close()
	slog.Info("lighthouse: up", "id", a.ID(), "relay", p.Relay())

	now := a.Now()
	for _, id := range mem {
		for _, f := range []struct {
			verb  cert.Verb
			facet string
		}{{cert.VerbPublish, lighthouse.FacetPublish}, {cert.VerbInvoke, lighthouse.FacetLookup}} {
			c, err := cert.Sign(cert.Cert{
				Aud: string(id),
				Can: f.verb,
				Cav: cert.Caveats{Target: []cert.ActorID{a.ID()}, Facet: []string{f.facet}},
				Iat: now,
				Exp: now + int64(memTTL.Seconds()),
			}, a.Signer)
			if err != nil {
				log.Fatal(err)
			}
			a.Consents = append(a.Consents, c)
		}
		slog.Info("lighthouse: member", "id", id, "exp", now+int64(memTTL.Seconds()))
	}
	l := lighthouse.New(a)
	l.MaxRecords = *maxRecords

	publish := func() {
		if _, err := a.PublishLocation(int64(locTTL.Seconds())); err != nil {
			slog.Warn("lighthouse: location", "err", err)
		}
	}
	publish()

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
			publish()
			if gate != nil {
				slog.Info("lighthouse: beat", "records", len(l.Records()), "relay_members", gate.Members(), "relay_presented", gate.Shown())
			} else {
				slog.Info("lighthouse: beat", "records", len(l.Records()))
			}
		}
	}
	stop()
	slog.Info("lighthouse: exit", "records", len(l.Records()))
}
