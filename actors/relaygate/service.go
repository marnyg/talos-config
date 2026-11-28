package relaygate

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// hookTokenEnv is how the hook's bearer reaches the relay: it reads
// this over `[access.http].bearer_token`, so the token is never in the
// config file.
const hookTokenEnv = "IROH_RELAY_HTTP_BEARER_TOKEN"

// Config is what Start needs: the gate, and the relay binary to run
// behind it.
type Config struct {
	Gate *Gate
	// Bin is the iroh-relay binary. With an access hook configured it
	// builds an HTTPS client at startup and panics when the host has no
	// CA bundle at all (measured, iroh-relay 1.1.0) — the image must
	// ship one although the hook is plain HTTP on loopback.
	Bin string
	// RelayLog is the relay's own log level (RUST_LOG). "" ⇒ "info".
	RelayLog string
	// Log receives the gate's lines and the relay's output. nil ⇒ none.
	Log *slog.Logger
}

// Service is a Gate with the relay it guards: the relay binary as a
// supervised child on loopback, its access hook on a loopback listener
// of its own, and the public handler in front of both. The relay holds
// no key and no state; everything that decides is in the Gate, which
// outlives a relay restart — a child that dies comes back to the same
// table, and its clients are admitted as they return.
type Service struct {
	srv     *Server
	backend *url.URL
	hookURL string
	hook    *http.Server
	dir     string
	bin     string
	level   string

	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	running bool
	starts  int
	lastErr error
	pid     int
}

// Start runs the relay behind c.Gate and returns once it answers its
// probe on loopback, or with the reason it did not. The relay is
// restarted when it exits, until ctx ends or Close.
func Start(ctx context.Context, c Config) (*Service, error) {
	if c.Gate == nil {
		return nil, errors.New("relaygate: no gate")
	}
	if _, err := os.Stat(c.Bin); err != nil {
		return nil, fmt.Errorf("relaygate: relay binary: %w", err)
	}
	var tok [32]byte
	if _, err := rand.Read(tok[:]); err != nil {
		return nil, fmt.Errorf("relaygate: hook token: %w", err)
	}
	s := &Service{bin: c.Bin, level: c.RelayLog, done: make(chan struct{})}
	if s.level == "" {
		s.level = "info"
	}
	s.srv = &Server{Gate: c.Gate, HookToken: hex.EncodeToString(tok[:]), Log: c.Log}

	// The hook: a loopback listener of its own, never a route on the
	// public one.
	hl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("relaygate: hook listener: %w", err)
	}
	s.hookURL = "http://" + hl.Addr().String() + HookPath
	mux := http.NewServeMux()
	mux.Handle("POST "+HookPath, s.srv.Hook())
	s.hook = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.hook.Serve(hl) }()

	// The relay: a free loopback port, kept across restarts so Backend
	// stays true.
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.hook.Close()
		return nil, fmt.Errorf("relaygate: relay port: %w", err)
	}
	port := pl.Addr().(*net.TCPAddr).Port
	pl.Close()
	s.backend = &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", fmt.Sprint(port))}
	s.srv.Backend = s.backend
	if s.dir, err = os.MkdirTemp("", "relaygate-"); err == nil {
		err = os.WriteFile(filepath.Join(s.dir, "relay.toml"), []byte(relayConfig(port, s.hookURL)), 0o600)
	}
	if err != nil {
		s.hook.Close()
		os.RemoveAll(s.dir)
		return nil, fmt.Errorf("relaygate: relay config: %w", err)
	}

	ctx, s.cancel = context.WithCancel(ctx)
	go s.supervise(ctx)
	if err := s.ready(ctx, 15*time.Second); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// relayConfig is the child's TOML: the relay protocol on loopback in
// plain HTTP (TLS is the platform's, in front of the public handler),
// no QUIC address discovery, no metrics listener, and every connect
// asked of the hook. No secret is in it.
func relayConfig(port int, hookURL string) string {
	return fmt.Sprintf("enable_relay = true\nhttp_bind_addr = \"127.0.0.1:%d\"\nenable_quic_addr_discovery = false\nenable_metrics = false\n\n[access.http]\nurl = %q\n", port, hookURL)
}

// Handler is the relay's whole public surface (Server.Public).
func (s *Service) Handler() http.Handler { return s.srv.Public() }

// Backend is the relay's own loopback URL, behind the gate's proxy.
// The actor that owns the gate's key homes here — it is always
// admitted, and it must not depend on its own public listener — while
// advertising the public URL to everyone else
// (irohtransport.Options.AdvertiseRelay).
func (s *Service) Backend() string { return s.backend.String() }

// Close stops the relay and the hook listener.
func (s *Service) Close() {
	s.cancel()
	<-s.done
	_ = s.hook.Close()
	os.RemoveAll(s.dir)
}

func (s *Service) log() *slog.Logger { return s.srv.log() }

// supervise restarts the relay when it exits, with a capped backoff. A
// relay that keeps dying is logged, never fatal here: the owner's other
// work (a lighthouse) does not stop for it, and the public handler
// answers 503 meanwhile.
func (s *Service) supervise(ctx context.Context) {
	defer close(s.done)
	backoff := time.Second
	for {
		began := time.Now()
		err := s.runOnce(ctx)
		s.mu.Lock()
		s.running, s.lastErr, s.pid = false, err, 0
		s.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > time.Minute {
			backoff = time.Second // it ran; this is not a crash loop
		}
		s.log().Warn("relaygate: relay exited; restarting", "err", err, "in", backoff)
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (s *Service) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, s.bin, "--config-path", filepath.Join(s.dir, "relay.toml"))
	cmd.Env = append(os.Environ(), "RUST_LOG="+s.level, "NO_COLOR=1", hookTokenEnv+"="+s.srv.HookToken)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	s.mu.Lock()
	s.running, s.pid = true, cmd.Process.Pid
	s.starts++
	s.mu.Unlock()
	s.log().Info("relaygate: relay started", "pid", cmd.Process.Pid, "listen", s.backend.Host)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			s.log().Info("relay| " + sc.Text())
		}
		_, _ = io.Copy(io.Discard, pr) // a line too long for the scanner must not block the child
	}()
	err := cmd.Wait()
	pw.Close()
	return err
}

// ready polls the relay's own probe on loopback.
func (s *Service) ready(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for {
		res, err := client.Get(s.backend.String() + "/generate_204")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusNoContent {
				return nil
			}
		}
		s.mu.Lock()
		exited, lastErr := !s.running && s.starts > 0, s.lastErr
		s.mu.Unlock()
		if exited && lastErr != nil {
			return fmt.Errorf("relaygate: relay exited before it was ready: %w", lastErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("relaygate: relay not ready after %s (last probe: %v)", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
