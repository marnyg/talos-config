//go:build iroh

// Command gateway is the in-cluster gateway pod (Mesh v3 P2.3,
// talos-config-359.9.3): the node agent runtime under Kind gateway
// (config-server/gateway).
//
//	gateway [-hub URL] [-relay URL] [-state DIR] [-name gw] [-group media]
//	        [-ingress URL] [-jellyfin host:port] [-bind ip:port] [-beat DUR]
//
// State (key, kit, bundle, hub record, mark) lives under -state, a
// volume that outlives the pod: the key is the member's identity and
// the Kit is its membership, neither re-derivable (invariant 1). On
// first start, with no Kit, it runs the device flow headlessly
// (nodeagent.EnrollDevice): the approve URL and user code go to the
// log; the Owner signs on /status; the pod polls and redeems. A denied
// flow exits (k8s restarts it with backoff — a fresh code in the log);
// an expired one starts over; a sealed hub is polled flat.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/gateway"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/iroh-go/iroh"
	"github.com/marnyg/talos-config/protocol/cert"
)

func main() {
	var (
		hub      = flag.String("hub", "https://marnyg-talos-config.fly.dev", "hub base URL (HTTPS: enrollment and /.well-known)")
		relay    = flag.String("relay", "", "iroh home relay URL (default: the hub, ADR-0022)")
		stateDir = flag.String("state", "/var/lib/gateway", "state directory (key, kit, bundle, hub record, mark)")
		name     = flag.String("name", "gw", "proposed member name (the approver decides; services are <svc>.<name>.mesh.internal)")
		group    = flag.String("group", "media", "proposed group (the approver decides)")
		ingress  = flag.String("ingress", "http://ingress-nginx-controller.ingress-nginx.svc.cluster.local", "ingress-http upstream: the ingress controller's Service")
		jellyfin = flag.String("jellyfin", "", "jellyfin facet target host:port (the Jellyfin Service); empty ⇒ facet not served")
		bindAddr = flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
		beat     = flag.Duration("beat", nodeagent.DefaultBeat, "renewal beat interval")
		maxAge   = flag.Duration("conn-max-age", gateway.DefaultConnMaxAge, "admitted connection bound (re-authorize past it)")
	)
	flag.Parse()
	if *relay == "" {
		*relay = *hub
	}
	if lvl := os.Getenv("P0_LOG"); lvl != "" { // trace|debug|info|warn
		if l, ok := map[string]iroh.LogLevel{"trace": iroh.LogLevelTrace, "debug": iroh.LogLevelDebug, "info": iroh.LogLevelInfo, "warn": iroh.LogLevelWarn}[lvl]; ok {
			iroh.SetLogLevel(l)
		}
	}
	logger := log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
	logger.Printf("gateway start: wall %s", time.Now().UTC().Format(time.RFC3339))

	upstream, err := url.Parse(*ingress)
	if err != nil || upstream.Host == "" {
		logger.Fatalf("-ingress %q: not a URL", *ingress)
	}
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		logger.Fatalf("state: %v", err)
	}
	proxy, stopProxy := gateway.HTTPFacet("ingress-http", gateway.Proxy(upstream), logger)
	defer stopProxy()
	fwd := map[string]string{}
	if *jellyfin != "" {
		if _, _, err := net.SplitHostPort(*jellyfin); err != nil {
			logger.Fatalf("-jellyfin: %v", err)
		}
		fwd["jellyfin"] = *jellyfin
	}

	cfg := nodeagent.Config{Hub: *hub, Relay: *relay}
	a, err := nodeagent.Start(nodeagent.Options{
		Config: cfg, State: nodeagent.State{Dir: *stateDir}, Kind: "gateway",
		Serve:   map[string]nodeagent.StreamHandler{"ingress-http": proxy},
		Forward: fwd, BindAddr: *bindAddr, Log: logger, BeatEvery: *beat, ConnMaxAge: *maxAge,
		Enroll: func(ctx context.Context, node cert.ActorID) (issuer.Kit, error) {
			return enrollHeadless(ctx, cfg.Hub, node, *name, *group, logger)
		},
	})
	if err != nil {
		logger.Fatalf("start: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	err = a.Run(ctx)
	_ = a.Close()
	if err != nil && ctx.Err() == nil {
		logger.Printf("fatal: %v", err)
		os.Exit(1)
	}
	logger.Printf("stopped")
}

// enrollHeadless runs device flows until one is signed: an expired flow
// (nobody at /status within its TTL) is restarted with a fresh code;
// denied and sealed are returned for Run to act on.
func enrollHeadless(ctx context.Context, hub string, node cert.ActorID, name, group string, logger *log.Logger) (issuer.Kit, error) {
	for {
		kit, err := nodeagent.EnrollDevice(ctx, nil, hub, node, name, group, func(f nodeagent.DeviceFlow) {
			logger.Printf("ENROLL: sign at %s  (user code %s, %s to act; NodeId %s)", f.ApproveURL, f.UserCode, (time.Duration(f.ExpiresIn) * time.Second).String(), node)
		})
		if errors.Is(err, nodeagent.ErrEnrollExpired) {
			logger.Printf("enroll: %v; starting a fresh flow", err)
			continue
		}
		if err != nil {
			return issuer.Kit{}, fmt.Errorf("device flow: %w", err)
		}
		return kit, nil
	}
}
