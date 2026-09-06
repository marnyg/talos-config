package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestSmokeDirect is the go-test face of the smoke binary: same code,
// RelayMode::Disabled, direct 127.0.0.1 dial.
func TestSmokeDirect(t *testing.T) {
	if err := Run(Options{Timeout: 30 * time.Second, Log: testWriter{t}}); err != nil {
		t.Fatal(err)
	}
}

// TestSmokeRelay runs a local iroh-relay (--dev: plain HTTP on loopback)
// and repeats the smoke with RelayMode::Custom. Needs the relay binary:
// IROH_RELAY_BIN, else `iroh-relay` on PATH (nix build .#iroh-go-smoke
// provides it); skipped otherwise so a bare `go test` stays green.
func TestSmokeRelay(t *testing.T) {
	bin := os.Getenv("IROH_RELAY_BIN")
	if bin == "" {
		p, err := exec.LookPath("iroh-relay")
		if err != nil {
			t.Skip("iroh-relay not available (set IROH_RELAY_BIN); relay path not tested")
		}
		bin = p
	}
	port := freePort(t)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// The relay CLI takes only --dev and --config-path; bind address and
	// the metrics/QUIC-addr-discovery side servers come from the config.
	cfg := filepath.Join(t.TempDir(), "relay.toml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(
		"http_bind_addr = \"127.0.0.1:%d\"\nenable_metrics = false\nenable_quic_addr_discovery = false\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--dev", "--config-path", cfg)
	cmd.Stdout, cmd.Stderr = testWriter{t}, testWriter{t}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", port))

	if err := Run(Options{RelayURL: url, Timeout: 30 * time.Second, Log: testWriter{t}}); err != nil {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitListening(t *testing.T, addr string) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("relay did not listen on %s", addr)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
