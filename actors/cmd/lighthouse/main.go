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
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/cmd/internal/boot"
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
	)
	flag.Var(&mem, "member", "actor id consented to #publish and #lookup; repeatable")
	flag.Parse()
	a, ep := p.Up()
	defer ep.Close()
	if len(mem) == 0 {
		log.Fatal("no -member: nobody could #publish or #lookup")
	}
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
			slog.Info("lighthouse: beat", "records", len(l.Records()))
		}
	}
	stop()
	slog.Info("lighthouse: exit", "records", len(l.Records()))
}
