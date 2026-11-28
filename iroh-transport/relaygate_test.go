package irohtransport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// testGate is the smallest gate that can sit in front of a stock
// iroh-relay (spike talos-config-qx5c). Three parts:
//
//   - present: a credential for id X arrives out of band (in the design
//     an HTTPS POST of X's `relay` cert chain; here a table write) and is
//     remembered until it expires — a cache of what the grantee showed.
//   - the relay's own [access.http] hook: it names the key that PROVED
//     itself inside the WebSocket, and admits it only while a presented
//     credential for that key is live.
//   - a reverse proxy: it owns the byte stream, so it can end a
//     connection when its credential does. It cannot see the proven key,
//     so the client tells it which id the connection is for with a
//     stable bearer (Options.RelayToken = the id).
//
// What a credential is (a cert chain) is TestRelayAdmissionShapes in
// protocol/cert; this gate only keeps `id → until`.
type testGate struct {
	mu       sync.Mutex
	until    map[cert.ActorID]time.Time // presented credentials
	upgrades map[cert.ActorID]int       // /relay upgrades per claimed id
	cuts     map[cert.ActorID]int       // connections the proxy ended
	denied   map[cert.ActorID]int       // hook refusals
	hookHdr  string                     // the id header the relay sent
}

func (g *testGate) present(id cert.ActorID, until time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.until[id] = until
}

func (g *testGate) live(id cert.ActorID) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	u, ok := g.until[id]
	return u, ok && time.Now().Before(u)
}

func (g *testGate) count(m map[cert.ActorID]int, id cert.ActorID) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return m[id]
}

// startGatedRelay runs iroh-relay with its access hook pointed at g and
// returns the URL of the proxy in front of it — the only address a
// client is given.
func startGatedRelay(t *testing.T) (*testGate, string) {
	t.Helper()
	bin := os.Getenv("IROH_RELAY_BIN")
	if bin == "" {
		p, err := exec.LookPath("iroh-relay")
		if err != nil {
			t.Skip("iroh-relay not available (set IROH_RELAY_BIN); relay gate not tested")
		}
		bin = p
	}
	g := &testGate{
		until:    map[cert.ActorID]time.Time{},
		upgrades: map[cert.ActorID]int{},
		cuts:     map[cert.ActorID]int{},
		denied:   map[cert.ActorID]int{},
	}

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var hex string
		for _, h := range []string{"X-Iroh-NodeId", "X-Iroh-Endpoint-Id"} {
			if v := r.Header.Get(h); v != "" {
				hex = v
				g.mu.Lock()
				g.hookHdr = h
				g.mu.Unlock()
			}
		}
		id := cert.ActorID("ed:" + strings.ToLower(hex))
		if _, ok := g.live(id); !ok {
			g.mu.Lock()
			g.denied[id]++
			g.mu.Unlock()
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "true")
	}))
	t.Cleanup(hook.Close)

	port := freePort(t)
	cfg := filepath.Join(t.TempDir(), "relay.toml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(
		"enable_relay = true\nhttp_bind_addr = \"127.0.0.1:%d\"\nenable_metrics = false\nenable_quic_addr_discovery = false\n\n[access.http]\nurl = %q\n",
		port, hook.URL)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--dev", "--config-path", cfg)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", port))

	backend, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	rp := httputil.NewSingleHostReverseProxy(backend)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relay" {
			rp.ServeHTTP(w, r) // probes carry no claim
			return
		}
		id := cert.ActorID(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		g.mu.Lock()
		g.upgrades[id]++
		g.mu.Unlock()
		if _, ok := g.live(id); !ok {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		// The connection lives while the credential does: when the
		// deadline it was admitted under passes, look again — a renewal
		// presented meanwhile extends it, otherwise the proxy closes the
		// backend (ReverseProxy does so when this context ends).
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			for {
				until, ok := g.live(id)
				if !ok {
					g.mu.Lock()
					g.cuts[id]++
					g.mu.Unlock()
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(until)):
				}
			}
		}()
		rp.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(front.Close)
	return g, front.URL
}

// gated binds a fresh key on the gated relay, claiming claim (default:
// its own id) to the proxy.
func gated(t *testing.T, relay string, claim cert.ActorID) *Endpoint {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ActorIDFromPublicKey(priv.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if claim == "" {
		claim = id
	}
	e, err := Bind(priv, Options{BindAddr: "127.0.0.1:0", Relay: relay, RelayToken: string(claim)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

// online reports whether e got a home relay within d.
func online(e *Endpoint, d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if e.Online(ctx) != nil {
		return false
	}
	tags := e.Endpoints()
	return len(tags) > 0 && strings.HasPrefix(tags[0], TagRelay)
}

// echoOnce dials to through the relay hint alone and exchanges one
// message; to must be accepting.
func echoOnce(ctx context.Context, from, to *Endpoint, relay string) error {
	srv := make(chan error, 1)
	go func() {
		s, _, err := to.Accept(ctx)
		if err != nil {
			srv <- err
			return
		}
		defer s.Close()
		msg, err := s.RecvMsg(ctx)
		if err != nil {
			srv <- err
			return
		}
		srv <- s.SendMsg(ctx, append([]byte("re:"), msg...))
	}()
	s, err := from.Dial(ctx, to.ID(), []string{TagRelay + relay})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer s.Close()
	if err := s.SendMsg(ctx, []byte("beat")); err != nil {
		return err
	}
	got, err := s.RecvMsg(ctx)
	if err != nil || string(got) != "re:beat" {
		return fmt.Errorf("reply %q %v", got, err)
	}
	return <-srv
}

// TestRelayGate measures, against the real relay binary, what the qx5c
// design rests on:
//
//  1. the hook names the key that actually connected, so presenting a
//     credential for X admits X and nobody claiming to be X;
//  2. an endpoint refused at first gets in by itself once its credential
//     is presented — no rebind, so "present, then connect" needs no
//     ordering and a gate that lost its cache recovers on re-presentation;
//  3. the proxy can end a connection when its credential expires, the
//     client returns by itself, and is refused until it is renewed;
//  4. a renewal presented in time keeps the connection, uninterrupted.
func TestRelayGate(t *testing.T) {
	g, relay := startGatedRelay(t)
	ctx := testCtx(t)
	far := time.Now().Add(time.Hour)

	// 1: presented; not presented; claiming to be someone presented.
	x := gated(t, relay, "")
	g.present(x.ID(), far)
	if !online(x, 10*time.Second) {
		t.Fatal("presented credential: no home relay")
	}
	t.Logf("hook id header: %s", g.hookHdr)
	thief := gated(t, relay, x.ID())
	if online(thief, 3*time.Second) {
		t.Fatal("another key claiming x's id: got a home relay")
	}
	if g.count(g.denied, thief.ID()) == 0 {
		t.Fatal("the thief was not refused by the hook — something else stopped it")
	}
	_ = thief.Close()

	// 2: bound first, presented later.
	y := gated(t, relay, "")
	if online(y, 3*time.Second) {
		t.Fatal("not presented: got a home relay")
	}
	began := time.Now()
	g.present(y.ID(), far)
	if !online(y, 15*time.Second) {
		t.Fatal("presented after being refused: never came online")
	}
	t.Logf("refused endpoint online %s after its credential was presented (%d refused upgrades before)",
		time.Since(began).Round(100*time.Millisecond), g.count(g.upgrades, y.ID())-1)
	if err := echoOnce(ctx, y, x, relay); err != nil {
		t.Fatalf("two admitted endpoints through the gate: %v", err)
	}

	// 3: expiry ends the connection; renewal brings it back.
	w := gated(t, relay, "")
	g.present(w.ID(), time.Now().Add(3*time.Second))
	if !online(w, 3*time.Second) {
		t.Fatal("short-lived credential: no home relay")
	}
	deadline := time.Now().Add(10 * time.Second)
	for g.count(g.cuts, w.ID()) == 0 || g.count(g.upgrades, w.ID()) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("after expiry: %d cuts, %d upgrades — want the proxy to cut and the client to knock again",
				g.count(g.cuts, w.ID()), g.count(g.upgrades, w.ID()))
		}
		time.Sleep(100 * time.Millisecond)
	}
	began = time.Now()
	g.present(w.ID(), far)
	for {
		ectx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := echoOnce(ectx, y, w, relay)
		cancel()
		if err == nil {
			break
		}
		if time.Since(began) > 15*time.Second {
			t.Fatalf("renewed after the cut: still unreachable: %v", err)
		}
	}
	t.Logf("cut endpoint reachable again %s after renewal, same endpoint", time.Since(began).Round(100*time.Millisecond))

	// 4: renewed before expiry — one upgrade, no cut.
	v := gated(t, relay, "")
	g.present(v.ID(), time.Now().Add(2*time.Second))
	if !online(v, 3*time.Second) {
		t.Fatal("to-be-renewed credential: no home relay")
	}
	g.present(v.ID(), far)
	time.Sleep(4 * time.Second)
	if n, c := g.count(g.upgrades, v.ID()), g.count(g.cuts, v.ID()); n != 1 || c != 0 {
		t.Fatalf("renewed in time: %d upgrades, %d cuts — want 1 and 0", n, c)
	}
	if err := echoOnce(ctx, y, v, relay); err != nil {
		t.Fatalf("reach v past its first credential's expiry: %v", err)
	}
}
