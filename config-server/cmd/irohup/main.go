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
//
// talosctl through the bridge: `talosctl -e 127.0.0.1:50000 -n <node>`
// with the node's hostname in the endpoint's SAN set — see
// day-to-day/notes.md (2026-09-15).
//
// Cache (~/.config/talos-mesh/): <name>.key and <name>.yml are nebup's
// two files (ADR-0012); <name>.iroh/ is the identity-plane state dir
// with the same layout as a node's /var/lib/p0agent (nodeagent.State):
// key, kit.json, bundle.json, hub.json, mark. The keys are the only
// state; the rest is the member's own certs and safe-to-lose caches.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
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
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
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
		stateDir = flag.String("state", "", "identity-plane state dir (default ~/.config/talos-mesh/<name>.iroh)")
		bindAddr = flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
		beat     = flag.Duration("beat", nodeagent.DefaultBeat, "renewal beat interval")
	)
	flag.Var(&br, "bridge", "<member>/<facet>=<host:port> local TCP bridge; repeatable (facets: "+strings.Join(policy.Facets(policy.KindNode), ", ")+")")
	flag.Parse()

	dev := nebderive.Normalize(*name)
	if dev == "" {
		log.Fatal("-name must not be empty")
	}
	if *group != "admins" && *group != "media" {
		log.Fatalf("-group must be 'admins' or 'media', got %q", *group)
	}
	if len(br) == 0 {
		br = bridges{{Name: "cp1", Facet: "apid", Listen: "127.0.0.1:50000"}, {Name: "cp1", Facet: "kube-api", Listen: "127.0.0.1:6443"}}
	}
	for _, b := range br {
		if !contains(policy.Facets(policy.KindNode), b.Facet) {
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

	keyPath, cfgPath, dir, err := cachePaths(dev)
	if err != nil {
		log.Fatal(err)
	}
	if *stateDir != "" {
		dir = *stateDir
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
		if err := enroll(strings.TrimRight(*hub, "/"), dev, *group, node, keyPath, cfgPath, state, *paste); err != nil {
			log.Fatal(err)
		}
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
	var wg sync.WaitGroup
	for _, b := range br {
		wg.Add(1)
		go func() { defer wg.Done(); b.serve(ctx, a, logger) }()
	}
	<-ctx.Done()
	wg.Wait()
	_ = a.Close()
	logger.Printf("stopped")
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// cachePaths: nebup's (<name>.key, <name>.yml) and irohup's <name>.iroh/
// under ~/.config/talos-mesh/.
func cachePaths(name string) (keyPath, cfgPath, stateDir string, err error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", "", fmt.Errorf("resolving config dir: %w", err)
	}
	base := filepath.Join(dir, "talos-mesh")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", "", "", err
	}
	return filepath.Join(base, name+".key"), filepath.Join(base, name+".yml"), filepath.Join(base, name+".iroh"), nil
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

// serve runs one bridge for the life of ctx. One connection to the
// member is shared by every TCP client and redialed when it is gone;
// each TCP connection is one forward stream on it.
func (b bridge) serve(ctx context.Context, a *nodeagent.Agent, logger *log.Logger) {
	ln, err := net.Listen("tcp", b.Listen)
	if err != nil {
		logger.Printf("bridge %s/%s: listen: %v", b.Name, b.Facet, err)
		return
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	logger.Printf("bridge %s/%s on %s", b.Name, b.Facet, b.Listen)

	var mu sync.Mutex
	var conn *irohtransport.Conn
	get := func() (*irohtransport.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		if conn != nil && conn.Alive() {
			return conn, nil
		}
		if conn != nil {
			logger.Printf("bridge %s/%s: connection gone — redialing", b.Name, b.Facet)
			_ = conn.Close()
			conn = nil
		}
		c, err := b.dial(ctx, a)
		if err != nil {
			return nil, err
		}
		conn = c
		return c, nil
	}
	drop := func(c *irohtransport.Conn) {
		mu.Lock()
		defer mu.Unlock()
		if conn == c {
			_ = conn.Close()
			conn = nil
		}
	}
	for {
		tcp, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer tcp.Close()
			c, err := get()
			if err != nil {
				logger.Printf("bridge %s/%s: %v", b.Name, b.Facet, err)
				return
			}
			raw, err := c.Open(ctx)
			if err != nil {
				// Stale connection (idle timeout, node rebooted between
				// Alive and Open): drop it and try once more.
				drop(c)
				if c, err = get(); err == nil {
					raw, err = c.Open(ctx)
				}
				if err != nil {
					logger.Printf("bridge %s/%s: open: %v", b.Name, b.Facet, err)
					return
				}
			}
			t0 := time.Now()
			in, out := pipe(raw, tcp.(*net.TCPConn))
			logger.Printf("bridge %s/%s: stream done: %dB in, %dB out, %s", b.Name, b.Facet, in, out, time.Since(t0).Round(time.Millisecond))
		}()
	}
}

// dial waits out the first beat (the name map arrives with it) and
// connects with the bundle; ErrNotBeaten is a wait, anything else is
// the answer.
func (b bridge) dial(ctx context.Context, a *nodeagent.Agent) (*irohtransport.Conn, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		t0 := time.Now()
		c, err := a.Dial(ctx, b.Name, b.Facet)
		if err == nil {
			log.Printf("bridge %s/%s: connected to %s in %s", b.Name, b.Facet, c.Peer(), time.Since(t0).Round(time.Millisecond))
			return c, nil
		}
		if !errors.Is(err, nodeagent.ErrNotBeaten) || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// pipe splices tcp ↔ raw until both directions are done; half-closes
// propagate. Returns bytes from the peer and bytes sent to it.
func pipe(raw *irohtransport.Raw, tcp *net.TCPConn) (in, out int64) {
	defer raw.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, _ = io.Copy(raw, tcp)
		_ = raw.CloseWrite()
	}()
	in, _ = io.Copy(tcp, raw)
	_ = tcp.CloseWrite()
	<-done
	return in, out
}
