//go:build iroh

// Command child is the v0 spawned actor (protocol ADR-0008/0009,
// 0bc.4.5): the process a provisioner's driver starts from the image
// a parent named. It takes no flags — a container gets its whole
// world from the environment the driver set:
//
//	SAP_INTRO   the intro, verbatim (driver.ParamsEnv): parent id, the
//	            parent's reach-me-at, the birth consent, the nonce
//	SAP_RELAY   home relay URL; default: the first iroh:relay= in the
//	            parent's reach-me-at — a child homes where its parent
//	            is homed, so the two meet at one relay server
//	SAP_BEAT    renewal interval (Go duration); default child.DefaultBeat
//	SAP_LOG     iroh log level: trace|debug|info|warn (off by default)
//
// The key is minted here, in memory, and never written: a spawned
// actor's identity exists only on its own compute (invariant 13). It
// binds iroh under that key, publishes its reach-me-at, knocks on
// P#birth (spawn.Born), then beats (actors/child.Run) until the
// renewal edge to the parent lapses — exit 0, its lease is the
// provisioner's to sweep — or the platform stops it (SIGTERM: exit 0).
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/actors/driver"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/spawn"
)

func main() {
	log.SetFlags(0)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	raw := os.Getenv(driver.ParamsEnv)
	if raw == "" {
		log.Fatalf("%s is not set: nothing to be born from", driver.ParamsEnv)
	}
	in, err := spawn.DecodeIntro([]byte(raw))
	if err != nil {
		log.Fatal(err)
	}
	relay := os.Getenv("SAP_RELAY")
	if relay == "" {
		relay = homeOf(in)
	}
	beat := child.DefaultBeat
	if s := os.Getenv("SAP_BEAT"); s != "" {
		if beat, err = time.ParseDuration(s); err != nil {
			log.Fatalf("SAP_BEAT: %v", err)
		}
	}
	irohtransport.SetLogLevel(os.Getenv("SAP_LOG")) // trace|debug|info|warn

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	ep, err := irohtransport.Bind(priv, irohtransport.Options{Relay: relay})
	if err != nil {
		log.Fatal(err)
	}
	defer ep.Close()
	a := actor.New(cert.NewEdSigner(priv), ep)
	a.SeqBase = func() int64 { return time.Now().UnixMicro() }
	slog.Info("child: up", "id", a.ID(), "parent", in.Parent, "relay", relay, "beat", beat)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	listen := make(chan error, 1)
	go func() { listen <- a.Listen(ctx) }()

	err = child.Run(ctx, a, in, child.Options{Beat: beat})
	stop()
	<-listen
	switch {
	case err == nil, errors.Is(err, child.ErrLapsed), errors.Is(err, context.Canceled):
		slog.Info("child: exit", "why", err)
	default:
		log.Fatal(err)
	}
}

// homeOf is the first iroh relay the parent's reach-me-at names, or
// "" (relay-less: direct addresses only, which across NATs is
// nothing — SAP_RELAY then).
func homeOf(in spawn.Intro) string {
	for _, e := range in.Location.Cav.Endpoints {
		if v, ok := strings.CutPrefix(e, irohtransport.TagRelay); ok && v != "" {
			return v
		}
	}
	return ""
}
