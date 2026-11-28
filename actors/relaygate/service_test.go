package relaygate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// testCABundle writes a throwaway self-signed CA and returns its path.
// With [access.http] the relay builds an HTTPS client at startup and
// panics when the host has no CA bundle at all — the nix sandbox has
// none (config-server/relay_test.go met this first). One root in
// SSL_CERT_FILE satisfies it; the hook itself is plain HTTP.
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

// relayBin is the real relay binary, or a skip.
func relayBin(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("IROH_RELAY_BIN"); bin != "" {
		return bin
	}
	bin, err := exec.LookPath("iroh-relay")
	if err != nil {
		t.Skip("iroh-relay not available (set IROH_RELAY_BIN); the gate is not tested against the relay")
	}
	return bin
}

// startService runs the real relay behind gate, as cmd/lighthouse does,
// until the test ends.
func startService(t *testing.T, gate *Gate, log *slog.Logger) *Service {
	t.Helper()
	bin := relayBin(t)
	t.Setenv("SSL_CERT_FILE", testCABundle(t))
	ctx, cancel := context.WithCancel(context.Background())
	svc, err := Start(ctx, Config{Gate: gate, Bin: bin, RelayLog: "warn", Log: log})
	if err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Close(); cancel() })
	return svc
}

func status(url string) int {
	res, err := (&http.Client{Timeout: time.Second}).Get(url)
	if err != nil {
		return 0
	}
	res.Body.Close()
	return res.StatusCode
}

// TestServiceSupervisesTheRelay: Start brings the real relay up behind
// the gate; a relay that dies comes back on the same address, and the
// gate — which is not the relay's — still knows who it admitted.
func TestServiceSupervisesTheRelay(t *testing.T) {
	w := newWorld(t)
	w.now = time.Now().Unix()
	w.gate = New(w.relay.ActorID(), []cert.Cert{w.consent(w.parent.ActorID(), w.now+3600)})
	svc := startService(t, w.gate, nil)
	public := httptest.NewServer(svc.Handler())
	t.Cleanup(public.Close)

	if got := status(public.URL + "/generate_204"); got != http.StatusNoContent {
		t.Fatalf("probe through the gate: %d, want 204", got)
	}
	if got := status(public.URL + "/"); got != http.StatusNotFound {
		t.Fatalf("the relay's index through the gate: %d, want 404", got)
	}
	// The hook is on its own listener and answers only the relay.
	res, err := http.Post(svc.hookURL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("hook without the relay's bearer: %d, want 401", res.StatusCode)
	}
	if got := status(public.URL + HookPath); got != http.StatusNotFound {
		t.Fatalf("the hook's path on the public handler: %d, want 404", got)
	}
	if got := status(strings.TrimSuffix(svc.hookURL, HookPath) + "/generate_204"); got != http.StatusNotFound {
		t.Fatalf("a relay path on the hook listener: %d, want 404", got)
	}

	child := newKey(t).ActorID()
	if _, err := w.gate.Present(Presentation{ID: child, Chain: []cert.Cert{w.toChild(child, 600)}}); err != nil {
		t.Fatal(err)
	}

	// Kill the relay under the gate.
	svc.mu.Lock()
	pid := svc.pid
	svc.mu.Unlock()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	down := false
	deadline := time.Now().Add(15 * time.Second)
	for {
		got := status(public.URL + "/generate_204")
		if got == http.StatusServiceUnavailable {
			down = true
		}
		svc.mu.Lock()
		starts, now := svc.starts, svc.pid
		svc.mu.Unlock()
		if got == http.StatusNoContent && starts >= 2 && now != pid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay not back after being killed: probe %d, %d starts", got, starts)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !down {
		t.Log("the probe never saw the gap (the restart was faster than the poll)")
	}
	if !w.admitted(child) || !w.admitted(w.parent.ActorID()) {
		t.Fatal("the gate forgot whom it admitted when the relay restarted")
	}

	dir, backend := svc.dir, svc.Backend()
	svc.Close()
	if got := status(backend + "/generate_204"); got != 0 {
		t.Fatalf("relay still answers after Close: %d", got)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config dir left behind: %v", err)
	}
}

// A relay that cannot start is Start's error, promptly — not a
// supervisor quietly looping behind a gate that answers 503.
func TestStartReportsARelayThatDoesNotStart(t *testing.T) {
	gate := New(newKey(t).ActorID(), nil)
	if _, err := Start(context.Background(), Config{Gate: gate, Bin: filepath.Join(t.TempDir(), "absent")}); err == nil {
		t.Fatal("Start with no binary: no error")
	}
	if _, err := Start(context.Background(), Config{Bin: "/bin/sh"}); err == nil {
		t.Fatal("Start with no gate: no error")
	}
	exits, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no `false` on PATH")
	}
	began := time.Now()
	_, err = Start(context.Background(), Config{Gate: gate, Bin: exits})
	if err == nil || !strings.Contains(err.Error(), "before it was ready") {
		t.Fatalf("a binary that exits at once: err = %v", err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("Start took %s to report a relay that exited at once", took.Round(time.Millisecond))
	}
}
