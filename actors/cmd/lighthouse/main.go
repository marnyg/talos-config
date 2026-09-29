//go:build iroh

// Command lighthouse is the v0 rendezvous actor (protocol ADR-0007,
// 0bc.6): protocol/lighthouse on iroh. Two facets — #publish and
// #lookup — and nothing else. It holds a volatile directory of what
// members published and hands records back by id; it cannot forge one
// (a client re-validates every record's signature).
//
//	lighthouse -state DIR -member ed:… [-member ed:…]
//
// Identity: -state DIR holds the key (a 32-byte seed, minted on first
// run — a lighthouse's id is what its members are handed as the network
// bundle, so it must outlive its process). Nothing else in DIR.
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
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/keyfile"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/lighthouse"
)

const keyFile = "key"

type members []cert.ActorID

func (m *members) String() string { return fmt.Sprint([]cert.ActorID(*m)) }
func (m *members) Set(s string) error {
	id := cert.ActorID(strings.TrimSpace(s))
	if err := id.Validate(); err != nil {
		return err
	}
	*m = append(*m, id)
	return nil
}

func main() {
	log.SetFlags(0)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	var mem members
	var (
		stateDir   = flag.String("state", "/var/lib/sap-lighthouse", "state dir: key (persisted identity)")
		relay      = flag.String("relay", "https://marnyg-talos-config.fly.dev", "iroh home relay URL")
		bindAddr   = flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
		memTTL     = flag.Duration("member-ttl", 365*24*time.Hour, "lifetime of each -member consent, from start")
		beat       = flag.Duration("beat", time.Minute, "reach-me-at interval")
		locTTL     = flag.Duration("location-ttl", 10*time.Minute, "lifetime of each published reach-me-at; keep well above -beat")
		maxRecords = flag.Int("max-records", lighthouse.DefaultMaxRecords, "directory cap: a new publisher is refused past it (<= 0 unbounded)")
	)
	flag.Var(&mem, "member", "actor id consented to #publish and #lookup; repeatable")
	flag.Parse()
	irohtransport.SetLogLevel(os.Getenv("SAP_LOG")) // trace|debug|info|warn
	if len(mem) == 0 {
		log.Fatal("no -member: nobody could #publish or #lookup")
	}

	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		log.Fatal(err)
	}
	priv, minted, err := keyfile.Load(filepath.Join(*stateDir, keyFile))
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
		slog.Info("lighthouse: key minted", "state", *stateDir)
	}
	slog.Info("lighthouse: up", "id", a.ID(), "relay", *relay)

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
