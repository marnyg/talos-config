//go:build iroh

// Command irohup is the owner's desktop entrypoint to the identity
// plane (Mesh v3 Phase 1, talos-config-359.8.4) — the successor of
// cmd/nebup, and for the dual-plane window its sibling: one wallet
// signature enrolls the device on both planes.
//
// On first run it enrolls: the NodeId is minted here (an Ed25519 key,
// the iroh EndpointId — never derived from anything the hub holds),
// the nebula key is nebup's <name>.key (reused or created), and the
// wallet signs the v2 enrollment message naming both (enrollmsg). The
// hub answers {config, kit}: the nebula config is completed and cached
// where nebup expects it, the Kit (member cert, beat grant, speak-as)
// is persisted in the state dir. Later runs load the Kit and skip the
// ceremony.
//
// Then it is a member: it beats the hub for its invoke grants and the
// name map (config-server/nodeagent, caller-only), and serves TCP
// bridges — each local listener is one (member name, facet) pair;
// every accepted TCP connection becomes one stream on a connection to
// that member, opened with this device's bundle on connect. The
// receiver (the node agent) authorizes once per connection; the bridge
// redials when the connection is gone (the node rebooted).
//
//	irohup                                   # enroll if needed, beat, bridge cp1's apid + kube-api
//	irohup -bridge cp1/apid=127.0.0.1:50000  # explicit bridges (repeatable): <member>/<facet>=<listen>
//	irohup -reenroll                         # discard the Kit and the nebula artifact, re-sign; keep both keys
//	irohup -rekey                            # discard the keys too: a new NodeId and nebula identity
//	irohup -paste                            # headless: paste the signature instead
//	irohup -enroll-only                      # enroll (browser or -paste) and exit
//
// talosctl through the bridge: `talosctl -e 127.0.0.1:50000 -n <node>`
// with the node's hostname in the endpoint's SAN set — see
// day-to-day/notes.md (2026-09-15).
//
// Desktop presentation (Phase 2.0, talos-config-359.9.6, decision fgr):
//
//	sudo irohup -tun -state /var/lib/talos-mesh/laptop.iroh   # utun + 198.18/15 + split DNS
//
// runs the same member as a daemon behind a utun: names under
// mesh.internal that the name map knows resolve to fake IPs, and a TCP
// flow to <fake IP>:<facet port> is one stream to that member. Started
// as root by launchd, it creates the utun, assigns 198.18.0.1, routes
// the /15 in, then drops to -user (_talosmesh) before anything touches
// the network; DNS is /etc/resolver/mesh.internal → 198.18.0.2, which
// nix-darwin declares statically. See tun.go.
//
// Cache (~/.config/talos-mesh/): <name>.key and <name>.yml are nebup's
// two files (ADR-0012); <name>.iroh/ is the identity-plane state dir
// with the same layout as a node's /var/lib/p0agent (nodeagent.State):
// key, kit.json, bundle.json, hub.json, mark. The keys are the only
// state; the rest is the member's own certs and safe-to-lose caches.
// With -state DIR the nebula files sit beside DIR instead, so a daemon
// with no home of its own is self-contained.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/devkey"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/config-server/walletsign"
	"github.com/marnyg/talos-config/iroh-go/iroh"
	"github.com/marnyg/talos-config/protocol/cert"
)

func main() {
	log.SetFlags(0)
	var br bridges
	var (
		hub      = flag.String("hub", "https://marnyg-talos-config.fly.dev", "hub base URL (HTTPS: enrollment and /.well-known)")
		relay    = flag.String("relay", "", "iroh home relay URL (default: the hub — one hostname on fly, ADR-0022)")
		name     = flag.String("name", "laptop", "device name (the approver may edit this on /status)")
		group    = flag.String("group", "admins", "requested group (admins|media); the approver decides the final value")
		reenroll = flag.Bool("reenroll", false, "discard the Kit and the nebula artifact and re-sign with the SAME keys")
		rekey    = flag.Bool("rekey", false, "discard the keys too: brand-new NodeId and nebula identity")
		paste    = flag.Bool("paste", false, "paste a signature instead of signing in the browser (headless)")
		enrollOn = flag.Bool("enroll-only", false, "enroll if needed, then exit (the daemon's handoff: run as its user, sign as yourself)")
		stateDir = flag.String("state", "", "identity-plane state dir (default ~/.config/talos-mesh/<name>.iroh)")
		bindAddr = flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
		beat     = flag.Duration("beat", nodeagent.DefaultBeat, "renewal beat interval")
		tunMode  = flag.Bool("tun", false, "desktop presentation: utun + 198.18/15 fake IPs + split DNS instead of bridges (root at start; drops to -user)")
		runAs    = flag.String("user", "_talosmesh", "with -tun: the user to drop to after the privileged setup")
		dnsUp    = flag.String("dns-upstream", "", "with -tun: where mesh.internal names NOT in the name map go (nebula's DNS while it coexists); empty = NXDOMAIN")
	)
	flag.Var(&br, "bridge", "<member>/<facet>=<host:port> local TCP bridge; repeatable (facets: "+strings.Join(policy.Facets(policy.KindNode), ", ")+")")
	flag.Parse()

	// Privileged setup first, before anything else runs: it takes no
	// input but the flags, and everything after it runs as -user
	// (decision fgr). tunUp is nil without -tun.
	var tunUp *tunSetup
	if *tunMode {
		if *stateDir == "" {
			log.Fatal("-tun needs an explicit -state (HOME is root's at start and -user's after the drop)")
		}
		var err error
		if tunUp, err = privilegedSetup(*stateDir, *runAs); err != nil {
			log.Fatal(err)
		}
	}

	dev := nebderive.Normalize(*name)
	if dev == "" {
		log.Fatal("-name must not be empty")
	}
	if *group != "admins" && *group != "media" {
		log.Fatalf("-group must be 'admins' or 'media', got %q", *group)
	}
	switch {
	case *tunMode && len(br) > 0:
		log.Fatal("-tun and -bridge are two presentations of the same member; run one")
	case !*tunMode && len(br) == 0:
		for _, f := range policy.Facets(policy.KindNode) {
			br = append(br, bridge{Name: "cp1", Facet: f, Listen: fmt.Sprintf("127.0.0.1:%d", policy.FacetPort(f))})
		}
	}
	for _, b := range br {
		if !slices.Contains(policy.Facets(policy.KindNode), b.Facet) {
			log.Fatalf("-bridge %s/%s: %q is not a node facet (%v)", b.Name, b.Facet, b.Facet, policy.Facets(policy.KindNode))
		}
	}
	if *relay == "" {
		*relay = *hub
	}
	if lvl := os.Getenv("P0_LOG"); lvl != "" { // trace|debug|info|warn
		if l, ok := map[string]iroh.LogLevel{"trace": iroh.LogLevelTrace, "debug": iroh.LogLevelDebug, "info": iroh.LogLevelInfo, "warn": iroh.LogLevelWarn}[lvl]; ok {
			iroh.SetLogLevel(l)
		}
	}

	keyPath, cfgPath, dir, err := cachePaths(dev, *stateDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	state := nodeagent.State{Dir: dir}
	if *rekey {
		_ = os.Remove(keyPath)
		_ = os.Remove(filepath.Join(dir, nodeagent.KeyFile))
	}
	if *rekey || *reenroll {
		_ = os.Remove(cfgPath)
		_ = os.Remove(filepath.Join(dir, nodeagent.KitFile))
		_ = os.Remove(filepath.Join(dir, nodeagent.BundleFile))
	}

	priv, minted, err := state.Key()
	if err != nil {
		log.Fatal(err)
	}
	node := cert.NewEdSigner(priv).ActorID()
	if minted {
		log.Printf("minted NodeId %s (%s)", node, filepath.Join(dir, nodeagent.KeyFile))
	}
	if _, ok, err := state.Kit(); err != nil {
		log.Fatalf("%s: %v (rerun with -reenroll)", filepath.Join(dir, nodeagent.KitFile), err)
	} else if !ok {
		if *tunMode {
			// Enrollment is a user-session act (browser + wallet); the
			// daemon has neither. Enroll as the service user once, headless.
			log.Fatalf("not enrolled: run `sudo -u %s irohup -name %s -state %s -enroll-only` once (sign in the browser, or add -paste), then start the daemon", *runAs, dev, dir)
		}
		if err := enroll(strings.TrimRight(*hub, "/"), dev, *group, node, keyPath, cfgPath, state, *paste); err != nil {
			log.Fatal(err)
		}
	} else if *enrollOn {
		log.Printf("already enrolled (%s)", filepath.Join(dir, nodeagent.KitFile))
	}
	if *enrollOn {
		return
	}

	logger := log.New(os.Stderr, "", log.Ltime)
	a, err := nodeagent.Start(nodeagent.Options{
		Config: nodeagent.Config{Hub: *hub, Relay: *relay}, State: state,
		BindAddr: *bindAddr, Log: logger, BeatEvery: *beat,
	})
	if err != nil {
		log.Fatalf("start: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		if err := a.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Printf("fatal: %v", err)
			stop()
		}
	}()
	pool := newConnPool(a, logger)
	var wg sync.WaitGroup
	for _, b := range br {
		wg.Add(1)
		go func() { defer wg.Done(); b.serve(ctx, pool, logger) }()
	}
	exit := 0
	if tunUp != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := serveTun(ctx, tunUp, a, pool, *dnsUp, logger); err != nil && ctx.Err() == nil {
				// The utun or its route is gone and we cannot re-add:
				// exit non-zero so launchd restarts us as root.
				logger.Printf("fatal: %v", err)
				exit = 1
				stop()
			}
		}()
	}
	<-ctx.Done()
	wg.Wait()
	_ = a.Close()
	logger.Printf("stopped")
	os.Exit(exit)
}

// cachePaths: nebup's (<name>.key, <name>.yml) and irohup's <name>.iroh/
// under ~/.config/talos-mesh/ — or, with an explicit state dir, the
// nebula files beside it.
func cachePaths(name, explicit string) (keyPath, cfgPath, stateDir string, err error) {
	base := ""
	if explicit != "" {
		base = filepath.Dir(explicit)
	} else {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", "", "", fmt.Errorf("resolving config dir: %w", err)
		}
		base = filepath.Join(dir, "talos-mesh")
		explicit = filepath.Join(base, name+".iroh")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", "", "", err
	}
	return filepath.Join(base, name+".key"), filepath.Join(base, name+".yml"), explicit, nil
}

// enroll runs the wallet-signed v2 enrollment and persists both
// planes' results: the completed nebula config at cfgPath (nebup's
// artifact) and the Kit in state.
func enroll(hub, name, group string, node cert.ActorID, keyPath, cfgPath string, state nodeagent.State, paste bool) error {
	xpriv, xpub, created, err := devkey.LoadOrCreate(keyPath)
	if err != nil {
		return err
	}
	if created {
		log.Printf("minted nebula key %s", keyPath)
	}
	body, err := walletsign.MeshEnroll(hub+"/mesh/enroll", name, group, hex.EncodeToString(xpub[:]), string(node), "irohup", paste)
	if err != nil {
		return err
	}
	var env struct {
		Config string          `json:"config"`
		Kit    json.RawMessage `json:"kit"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Kit) == 0 {
		return fmt.Errorf("enrollment: the hub did not answer with {config, kit} (identity plane not served?): %s", strings.TrimSpace(firstLine(body)))
	}
	kit, err := issuer.DecodeKit(env.Kit)
	if err != nil {
		return fmt.Errorf("enrollment: kit: %w", err)
	}
	if err := nodeagent.CheckKit(kit, node); err != nil {
		return err
	}
	if err := state.SaveKit(kit); err != nil {
		return err
	}
	spliced, err := devkey.SpliceKeyInline([]byte(env.Config), xpriv)
	if err != nil {
		log.Printf("warning: nebula config not cached: %v", err)
	} else if err := os.WriteFile(cfgPath, spliced, 0o600); err != nil {
		log.Printf("warning: nebula config not cached: %v", err)
	}
	log.Printf("enrolled %q groups %v until %s (issuer %s) — kit at %s, nebula config at %s",
		kit.Member.Cav.Name, kit.Member.Cav.Groups, time.Unix(kit.Member.Exp, 0).UTC().Format(time.RFC3339), kit.Member.Iss,
		filepath.Join(state.Dir, nodeagent.KitFile), cfgPath)
	return nil
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// serve runs one bridge for the life of ctx: each accepted TCP
// connection is one forward stream on the pool's connection to the
// member.
func (b bridge) serve(ctx context.Context, pool *connPool, logger *log.Logger) {
	ln, err := net.Listen("tcp", b.Listen)
	if err != nil {
		logger.Printf("bridge %s/%s: listen: %v", b.Name, b.Facet, err)
		return
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	logger.Printf("bridge %s/%s on %s", b.Name, b.Facet, b.Listen)
	for {
		tcp, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer tcp.Close()
			raw, err := pool.open(ctx, b.Name, b.Facet)
			if err != nil {
				logger.Printf("bridge %s/%s: %v", b.Name, b.Facet, err)
				return
			}
			t0 := time.Now()
			in, out := pipe(raw, tcp.(*net.TCPConn))
			logger.Printf("bridge %s/%s: stream done: %dB in, %dB out, %s", b.Name, b.Facet, in, out, time.Since(t0).Round(time.Millisecond))
		}()
	}
}
