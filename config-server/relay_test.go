package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// upgradeEcho is a stand-in for the relay's /relay handler: it accepts an
// HTTP upgrade and then echoes raw bytes, which is the shape the iroh
// relay protocol has after the 101 (a WebSocket the proxy must not
// buffer, parse, or time out).
func upgradeEcho(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/relay", func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		// The proxy must forward the client's identifying headers.
		if r.Header.Get("X-Forwarded-For") == "" {
			http.Error(w, "no X-Forwarded-For", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw.Reader)
	})
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "pong") })
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "leak") })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// relayProxyServer mounts the relay routes (and nothing else the relay
// serves) on a hub mux in front of backend.
func relayProxyServer(t *testing.T, backend *httptest.Server) *httptest.Server {
	t.Helper()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	rs := &relaySupervisor{}
	rs.proxy = newRelayProxy(target, rs.errorHandler)
	s := newTestServer(t)
	s.relay = rs
	ts := httptest.NewServer(s.mux())
	t.Cleanup(ts.Close)
	return ts
}

func TestRelayProxyProbes(t *testing.T) {
	hub := relayProxyServer(t, upgradeEcho(t))

	code, body := get(t, hub.Client(), hub.URL+"/ping")
	if code != http.StatusOK || body != "pong" {
		t.Fatalf("/ping: got %d %q", code, body)
	}
	code, _ = get(t, hub.Client(), hub.URL+"/generate_204")
	if code != http.StatusNoContent {
		t.Fatalf("/generate_204: got %d", code)
	}
	// Only the three relay-protocol paths cross the hub; the relay's
	// other surfaces stay on loopback (invariant 5: one public surface,
	// and it is the hub's).
	code, _ = get(t, hub.Client(), hub.URL+"/metrics")
	if code != http.StatusNotFound {
		t.Fatalf("/metrics leaked through the hub: got %d", code)
	}
}

func TestRelayProxyUpgrade(t *testing.T) {
	hub := relayProxyServer(t, upgradeEcho(t))
	u, _ := url.Parse(hub.URL)

	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /relay HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	// Past the 101 the proxy is a dumb pipe in both directions.
	for _, msg := range []string{"hello relay\n", "second frame\n"} {
		if _, err := io.WriteString(conn, msg); err != nil {
			t.Fatal(err)
		}
		got, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if got != msg {
			t.Fatalf("echo: got %q want %q", got, msg)
		}
	}
}

func TestRelayProxyBackendDown(t *testing.T) {
	backend := upgradeEcho(t)
	hub := relayProxyServer(t, backend)
	backend.Close()

	// A dead child answers 503, so iroh clients back off and retry
	// their home relay rather than treating it as gone.
	code, _ := get(t, hub.Client(), hub.URL+"/ping")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with the child down, got %d", code)
	}
}

func TestRelayRoutesAbsentWithoutRelay(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.mux())
	defer ts.Close()
	for _, p := range relayPaths {
		if code, _ := get(t, ts.Client(), ts.URL+p); code != http.StatusNotFound {
			t.Errorf("%s without --relay-bin: got %d, want 404", p, code)
		}
	}
}

func TestRelayConfigShape(t *testing.T) {
	cfg := relayConfig(4321)
	for _, want := range []string{
		`http_bind_addr = "127.0.0.1:4321"`,  // loopback only: the hub is the entrypoint
		`enable_quic_addr_discovery = false`, // ADR-0022: no QAD without a relay-owned cert
		`enable_metrics = false`,
		`enable_relay = true`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("relay.toml missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "[tls]") || strings.Contains(cfg, "tls") {
		t.Errorf("relay.toml must not configure TLS (fly terminates it):\n%s", cfg)
	}
}

// relayBinary finds an iroh-relay to run the child test against:
// IROH_RELAY_BIN, else PATH. Absent ⇒ skip (the nix check sandbox and
// most dev shells have none; `nix build .#iroh-relay` provides one).
func relayBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("IROH_RELAY_BIN"); p != "" {
		return p
	}
	if p, err := exec.LookPath("iroh-relay"); err == nil {
		return p
	}
	t.Skip("no iroh-relay binary (set IROH_RELAY_BIN or nix build .#iroh-relay)")
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, p, _ := net.SplitHostPort(l.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

// TestRelayChild runs the real binary under the supervisor and speaks
// the two probes through the hub mux: the shape fly sees.
func TestRelayChild(t *testing.T) {
	bin := relayBinary(t)
	rs, err := newRelaySupervisor(bin, freePort(t))
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if line, warn := rs.statusLine(); warn || !strings.HasPrefix(line, "up") {
		t.Fatalf("status after start: %q warn=%v", line, warn)
	}

	s := newTestServer(t)
	s.relay = rs
	hub := httptest.NewServer(s.mux())
	defer hub.Close()

	if code, _ := get(t, hub.Client(), hub.URL+"/generate_204"); code != http.StatusNoContent {
		t.Fatalf("/generate_204 via hub: %d", code)
	}
	if code, _ := get(t, hub.Client(), hub.URL+"/ping"); code != http.StatusOK {
		t.Fatalf("/ping via hub: %d", code)
	}
	// The upgrade path answers something relay-shaped (the real protocol
	// needs an iroh client; a bare upgrade without the relay subprotocol
	// is refused, but it must reach the child rather than the hub's 404).
	code, _ := get(t, hub.Client(), hub.URL+"/relay")
	if code == http.StatusNotFound || code == http.StatusServiceUnavailable {
		t.Fatalf("/relay did not reach the child: %d", code)
	}
}
