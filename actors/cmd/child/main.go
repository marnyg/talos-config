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
//	            is homed, so the two meet at one relay server. When
//	            the intro carries a relay birth link (protocol
//	            ADR-0012) the same URL is where the relay's gate is:
//	            the child presents that link there before it binds,
//	            and its own relay chain after birth and on every beat
//	SAP_BEAT    renewal interval (Go duration); default child.DefaultBeat
//	SAP_LOG     iroh log level: trace|debug|info|warn (off by default)
//	SAP_PI      the pi binary (default pi); the child serves #task,
//	            #steer and #abort and streams pi's records up the
//	            parent's agent-events facet (child.TaskOptions);
//	            SAP_PI="" (set, empty) ⇒ #ping only
//	SAP_PI_ARGS extra pi arguments, comma-separated (--provider,x,…)
//	SAP_WORKDIR pi's working directory; default /workspace. A #task
//	            naming a repo clones it under here and runs pi in the
//	            clone.
//	FORGEJO_URL, FORGEJO_TOKEN, FORGEJO_USER
//	            the forge a task's repo comes from and its result goes
//	            to (child.Forge, e6pu.3) — the outfit's `forgejo-agent`
//	            secret (ADR-0037). Read once and REMOVED from the
//	            environment before pi starts: the token reaches git as
//	            a header and the agent never sees it. Unset — a #task
//	            naming a repo is refused.
//
// The key is minted here, in memory, and never written: a spawned
// actor's identity exists only on its own compute (invariant 13). It
// binds iroh under that key, publishes its reach-me-at, knocks on
// P#birth (spawn.Born), then beats (actors/child.Run) until the
// renewal edge to the parent lapses — exit 0, its lease is the
// provisioner's to sweep — or the platform stops it (SIGTERM: exit 0).
// Meanwhile it is the parent's agent (talos-config-e6pu.2): #task runs
// pi and streams its records up the parent's agent-events facet.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/actors/cmd/internal/boot"
	"github.com/marnyg/talos-config/actors/driver"
	"github.com/marnyg/talos-config/actors/relaygate"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/spawn"
)

func main() {
	boot.Logging()
	hideProc()

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
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// A gated relay (ADR-0012): nothing below can reach anyone until
	// the relay admits this key, so the intro's link is presented
	// first — and waited on, for as long as it is valid, if the gate
	// is briefly unreachable.
	var present func(context.Context, []cert.Cert) error
	if in.Relay != nil {
		if err := spawn.CheckIntro(in, time.Now().Unix()); err != nil {
			log.Fatal(err)
		}
		if relay == "" {
			log.Fatal("the intro names a gated relay but no relay URL: set SAP_RELAY")
		}
		gate := in.Relay.Cav.Target[0]
		present = relaygate.Presenter(&http.Client{Timeout: 15 * time.Second}, relay, gate, cert.NewEdSigner(priv).ActorID())
		pctx, cancel := context.WithDeadline(ctx, time.Unix(in.Relay.Exp, 0))
		err := relaygate.PresentUntil(pctx, present, []cert.Cert{*in.Relay}, 2*time.Second)
		cancel()
		if err != nil {
			log.Fatalf("relay gate %s at %s: %v", gate, relay, err)
		}
		slog.Info("child: admitted to the relay on the intro's link", "gate", gate, "until", in.Relay.Exp)
	}
	a, ep, err := boot.Actor(priv, relay, "", nil, in.Relay != nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ep.Close()
	task := taskOptions(ep)
	slog.Info("child: up", "id", a.ID(), "parent", in.Parent, "relay", relay, "beat", beat, "pi", task.PiBin, "workdir", task.Dir, "forge", task.Forge.URL)

	listen := make(chan error, 1)
	go func() { listen <- a.Listen(ctx) }()

	err = child.Run(ctx, a, in, child.Options{Beat: beat, Task: task, Present: present})
	stop()
	<-listen
	switch {
	case err == nil, errors.Is(err, child.ErrLapsed), errors.Is(err, context.Canceled):
		slog.Info("child: exit", "why", err)
	default:
		log.Fatal(err)
	}
}

// taskOptions reads SAP_PI, SAP_PI_ARGS, SAP_WORKDIR and FORGEJO_*
// and binds the Dialer to iroh. FORGEJO_* is unset from Go's copy of
// the environment so pi and git do not inherit it; the kernel's copy
// (/proc/1/environ) is what hideProc covers — os.Unsetenv never
// touches it (62a0ec24, 2026-10-19). The Dialer: one connection to the parent's agent-events facet,
// admitted on the preamble, kept for the process's lifetime and
// re-dialled when it is gone; each Dial call is one stream on it.
func taskOptions(ep *irohtransport.Endpoint) child.TaskOptions {
	bin, set := os.LookupEnv("SAP_PI")
	if !set {
		bin = "pi"
	}
	if bin == "" {
		return child.TaskOptions{}
	}
	var args []string
	if s := os.Getenv("SAP_PI_ARGS"); s != "" {
		args = strings.Split(s, ",")
	}
	dir := os.Getenv("SAP_WORKDIR")
	if dir == "" {
		dir = "/workspace"
	}
	forge := child.Forge{URL: os.Getenv("FORGEJO_URL"), Token: os.Getenv("FORGEJO_TOKEN"), User: os.Getenv("FORGEJO_USER")}
	for _, k := range []string{"FORGEJO_URL", "FORGEJO_TOKEN", "FORGEJO_USER"} {
		_ = os.Unsetenv(k)
	}
	var mu sync.Mutex
	var conn *irohtransport.Conn
	dial := func(ctx context.Context, parent cert.ActorID, hints []string, preamble []byte) (io.ReadWriteCloser, error) {
		mu.Lock()
		defer mu.Unlock()
		if conn != nil && conn.Alive() {
			if raw, err := conn.Open(ctx); err == nil {
				return raw, nil
			}
		}
		if conn != nil {
			_ = conn.Close()
			conn = nil
		}
		c, err := ep.DialConn(ctx, parent, hints, child.FacetAgentEvents, preamble)
		if err != nil {
			return nil, err
		}
		raw, err := c.Open(ctx)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		conn = c
		return raw, nil
	}
	return child.TaskOptions{PiBin: bin, PiArgs: args, Dir: dir, Forge: forge, Dial: dial}
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
