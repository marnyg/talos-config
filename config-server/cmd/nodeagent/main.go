//go:build iroh

// Command nodeagent is the Mesh v3 node agent (talos-config-359.8.3):
// the Talos system extension service that makes a node a member of the
// identity plane. Successor of iroh-go/cmd/p0agent (the P0.3 probe),
// same state dir and key file, so an upgraded node keeps its NodeId.
//
//	nodeagent [-config PATH] [-state DIR] [-forward facet=host:port ...] [-bind ip:port] [-beat DUR]
//
// Reads the hub-injected config (nodeagent.ConfigPath), loads or mints
// the key under -state, enrolls with the boot token when it holds no
// Kit, and then beats and serves stream facets for the life of the
// process. See config-server/nodeagent for what each step is.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/config-server/policy"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
)

type forwards map[string]string

func (f forwards) String() string {
	var parts []string
	for k, v := range f {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (f forwards) Set(s string) error {
	facet, target, ok := strings.Cut(s, "=")
	if !ok || facet == "" || target == "" {
		return fmt.Errorf("want facet=host:port, got %q", s)
	}
	f[facet] = target
	return nil
}

func main() {
	fwd := forwards{}
	cfgPath := flag.String("config", nodeagent.ConfigPath, "hub-injected agent config (ExtensionServiceConfig file)")
	stateDir := flag.String("state", "/var/lib/p0agent", "state directory (key, kit, bundle, hub record, mark)")
	bindAddr := flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
	beat := flag.Duration("beat", nodeagent.DefaultBeat, "renewal beat interval")
	flag.Var(fwd, "forward", "facet=host:port loopback target for a node facet; repeatable (facets: "+strings.Join(policy.Facets(policy.KindNode), ", ")+")")
	flag.Parse()
	if len(fwd) == 0 {
		fwd["apid"] = fmt.Sprintf("127.0.0.1:%d", policy.FacetPort("apid"))
	}
	irohtransport.SetLogLevel(os.Getenv("P0_LOG")) // trace|debug|info|warn
	logger := log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
	// ADR-0019: the service depends on time.sync; the clock we start
	// with is on record beside the uptime.
	logger.Printf("nodeagent start: wall %s uptime %s", time.Now().UTC().Format(time.RFC3339), uptime())

	// The config is an ExtensionServiceConfig document the hub injects
	// at config serve. A node upgraded to this binary before its config
	// was re-served has none yet: wait for it rather than crash-loop.
	cfg, err := nodeagent.Load(*cfgPath)
	for err != nil {
		logger.Printf("config: %v (waiting; re-serve the machine config with `nix run .#apply`)", err)
		time.Sleep(30 * time.Second)
		cfg, err = nodeagent.Load(*cfgPath)
	}
	a, err := nodeagent.Start(nodeagent.Options{
		Config: cfg, State: nodeagent.State{Dir: *stateDir}, Forward: fwd,
		BindAddr: *bindAddr, Log: logger, BeatEvery: *beat,
	})
	if err != nil {
		logger.Fatalf("start: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	err = a.Run(ctx)
	_ = a.Close()
	if err != nil && ctx.Err() == nil {
		// ErrTokenDead: nothing to do until a fresh config serve replaces
		// our file — Talos restarts the service when it does. Exit slowly
		// so `restart: always` does not spin.
		logger.Printf("fatal: %v", err)
		time.Sleep(10 * time.Minute)
		os.Exit(1)
	}
	logger.Printf("stopped")
}

func uptime() string {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "n/a"
	}
	f, _, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	return f + "s"
}
