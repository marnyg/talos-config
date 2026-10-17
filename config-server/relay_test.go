package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
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
	for _, p := range append(slices.Clone(relayPaths), relayAccessPath) {
		if code, _ := get(t, ts.Client(), ts.URL+p); code != http.StatusNotFound {
			t.Errorf("%s without --relay-bin: got %d, want 404", p, code)
		}
	}
}

func TestRelayConfigShape(t *testing.T) {
	cfg := relayConfig(4321, relayHook{})
	for _, want := range []string{
		`http_bind_addr = "127.0.0.1:4321"`,  // loopback only: the hub is the entrypoint
		`enable_quic_addr_discovery = false`, // ADR-0022: no QAD without a relay-owned cert
		`enable_metrics = false`,
		`enable_relay = true`,
		`access = "everyone"`, // no hook ⇒ today's behaviour
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("relay.toml missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "[tls]") || strings.Contains(cfg, "tls") {
		t.Errorf("relay.toml must not configure TLS (fly terminates it):\n%s", cfg)
	}
}

// TestRelayConfigShapeGated: with a hook the TOML selects iroh-relay's
// http access (5gz) — the url, and nothing of the bearer, which rides
// the child's environment instead.
func TestRelayConfigShapeGated(t *testing.T) {
	h := testRelayHook(func(cert.ActorID) (bool, string) { return true, "x" })
	cfg := relayConfig(4321, h)
	for _, want := range []string{
		`http_bind_addr = "127.0.0.1:4321"`,
		"\n[access.http]\nurl = \"http://127.0.0.1:8080/relay/access\"\n",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("relay.toml missing %q:\n%s", want, cfg)
		}
	}
	for _, no := range []string{`access = "everyone"`, "bearer", h.Token} {
		if strings.Contains(cfg, no) {
			t.Errorf("relay.toml must not contain %q:\n%s", no, cfg)
		}
	}
	// The table must come last: TOML keys after a [table] header belong
	// to that table, and enable_relay inside [access.http] is a parse
	// error that would keep the child from starting.
	if i, j := strings.Index(cfg, "[access.http]"), strings.LastIndex(cfg, "\nenable_"); i < j {
		t.Errorf("top-level keys after [access.http]:\n%s", cfg)
	}
}

// testRelayHook is a fully specified hook with a fixed token.
func testRelayHook(admit func(cert.ActorID) (bool, string)) relayHook {
	return relayHook{
		URL:   "http://127.0.0.1:8080" + relayAccessPath,
		Token: strings.Repeat("ab", 32),
		Admit: admit,
	}
}

// accessPost speaks the hook contract the way iroh-relay 1.1.0 does:
// POST with X-Iroh-NodeId and an optional bearer.
func accessPost(t *testing.T, c *http.Client, url, bearer, endpointID string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if endpointID != "" {
		req.Header.Set("X-Iroh-NodeId", endpointID)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

// TestRelayAccessHook: the hub-side half of the contract. 401 without
// the bearer (the public surface is not a membership oracle), then
// exactly "true"/200 or "false"/403 from Admit, with the status line
// reflecting the mode and the counts.
func TestRelayAccessHook(t *testing.T) {
	mode := "open (cold)"
	admit := func(id cert.ActorID) (bool, string) {
		if strings.HasPrefix(mode, "open") {
			return true, mode
		}
		return id == testMemberID, mode
	}
	rs := &relaySupervisor{hook: testRelayHook(admit), deniedSeen: map[cert.ActorID]struct{}{}}
	s := newTestServer(t)
	s.relay = rs
	hub := httptest.NewServer(s.mux())
	defer hub.Close()
	u := hub.URL + relayAccessPath
	hexID := func(id cert.ActorID) string { return strings.TrimPrefix(string(id), "ed:") }

	if line, _ := rs.statusLine(); !strings.Contains(line, "access: hook wired, no connects yet") {
		t.Fatalf("status before any connect: %q", line)
	}
	// Auth first: no bearer, wrong bearer, GET.
	if code, _ := accessPost(t, hub.Client(), u, "", hexID(testMemberID)); code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d", code)
	}
	if code, _ := accessPost(t, hub.Client(), u, strings.Repeat("cd", 32), hexID(testMemberID)); code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: %d", code)
	}
	if code, _ := get(t, hub.Client(), u); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", code)
	}
	tok := rs.hook.Token
	// Cold: everyone.
	if code, body := accessPost(t, hub.Client(), u, tok, hexID(testStranger)); code != http.StatusOK || body != "true" {
		t.Fatalf("cold stranger: %d %q", code, body)
	}
	// Gated: member yes (uppercase hex too — iroh prints lowercase, but
	// the id is the key, not its spelling), hub self decided by Admit,
	// stranger no, garbage no.
	mode = "gated (1 members)"
	if code, body := accessPost(t, hub.Client(), u, tok, hexID(testMemberID)); code != http.StatusOK || body != "true" {
		t.Fatalf("member: %d %q", code, body)
	}
	if code, body := accessPost(t, hub.Client(), u, tok, strings.ToUpper(hexID(testMemberID))); code != http.StatusOK || body != "true" {
		t.Fatalf("member, uppercase: %d %q", code, body)
	}
	if code, body := accessPost(t, hub.Client(), u, tok, hexID(testStranger)); code != http.StatusForbidden || body != "false" {
		t.Fatalf("stranger: %d %q", code, body)
	}
	for _, bad := range []string{"", "zz", "ed:" + hexID(testMemberID)} {
		if code, body := accessPost(t, hub.Client(), u, tok, bad); code != http.StatusForbidden || body != "false" {
			t.Fatalf("bad id %q: %d %q", bad, code, body)
		}
	}
	line, _ := rs.statusLine()
	if !strings.Contains(line, "access: gated (1 members), 3 admitted / 1 denied") {
		t.Fatalf("status: %q", line)
	}
}

// TestRelayAccessHookLoopbackOnly: off loopback the route does not
// exist (404), bearer or not — fly's proxy never sees a membership
// oracle on the public hostname (invariant 5).
func TestRelayAccessHookLoopbackOnly(t *testing.T) {
	rs := &relaySupervisor{hook: testRelayHook(func(cert.ActorID) (bool, string) { return true, "open" }), deniedSeen: map[cert.ActorID]struct{}{}}
	req := httptest.NewRequest(http.MethodPost, relayAccessPath, nil)
	req.RemoteAddr = "203.0.113.9:4444"
	req.Header.Set("Authorization", "Bearer "+rs.hook.Token)
	req.Header.Set("X-Iroh-NodeId", strings.TrimPrefix(string(testMemberID), "ed:"))
	rec := httptest.NewRecorder()
	rs.handleRelayAccess(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("off-loopback: %d", rec.Code)
	}
	for _, addr := range []string{"127.0.0.1:5", "[::1]:5", "127.9.9.9:5"} {
		req.RemoteAddr = addr
		rec = httptest.NewRecorder()
		rs.handleRelayAccess(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "true" {
			t.Fatalf("%s: %d %q", addr, rec.Code, rec.Body.String())
		}
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
	for _, gated := range []bool{false, true} {
		t.Run(map[bool]string{false: "everyone", true: "hooked"}[gated], func(t *testing.T) {
			var hook relayHook
			if gated {
				// The hook's URL is irrelevant here: no iroh client connects,
				// so the child never POSTs. What this proves is that the
				// `[access.http]` TOML and the bearer env are accepted by
				// the real binary and it still comes up.
				hook = testRelayHook(func(cert.ActorID) (bool, string) { return true, "open" })
				// With [access.http] the relay builds a reqwest client at
				// startup, which panics (exit 101) when the host has no CA
				// bundle — the nix sandbox has none (first hub image build
				// after 5gz, 2026-10-17). fly's image ships cacert
				// (fly/image.nix); here a throwaway self-signed PEM via
				// SSL_CERT_FILE satisfies "at least one root loaded".
				t.Setenv("SSL_CERT_FILE", testCABundle(t))
			}
			testRelayChild(t, bin, hook)
		})
	}
}

func testRelayChild(t *testing.T, bin string, hook relayHook) {
	rs, err := newRelaySupervisor(bin, freePort(t), hook)
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

// testCABundle writes one self-signed certificate as a PEM file and
// returns its path (see TestRelayChild/hooked).
func testCABundle(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "relay-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
