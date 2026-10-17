package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// relayPaths are the three routes the iroh relay protocol needs on the
// hub's public listener (ADR-0022, Mesh v3 P0.1): the WebSocket upgrade
// members hold open, and the two probes the client's net report sends.
// Everything else the relay binary serves (index, /metrics, robots) is
// deliberately not exposed — the relay is a transport component behind
// the hub's single entrypoint, not a second web surface (invariant 5).
var relayPaths = []string{"/relay", "/ping", "/generate_204"}

// relayAccessPath is the hub-served access hook the relay child POSTs
// to on every client connect (talos-config-5gz). It is a loopback
// contract between two processes of one machine, not a web route:
// register mounts it beside relayPaths, handleRelayAccess answers 404
// to anything not from loopback and 401 without the per-process
// bearer, so the public surface never becomes a membership oracle
// (invariant 5).
const relayAccessPath = "/relay/access"

// relayEndpointHeader carries the connecting client's EndpointId. The
// iroh-relay 1.1.0 doc comment calls it X-Iroh-Endpoint-Id; the code
// (and the binary's strings) say X-Iroh-NodeId — the code wins.
const relayEndpointHeader = "X-Iroh-NodeId"

// relayTokenEnv is how the bearer reaches the child: iroh-relay reads
// IROH_RELAY_HTTP_BEARER_TOKEN over `[access.http].bearer_token`, so
// the token never touches the config file.
const relayTokenEnv = "IROH_RELAY_HTTP_BEARER_TOKEN"

// relayHook is the membership gate behind the relay (talos-config-5gz,
// ruled 2026-10-17): iroh-relay's HTTP access hook, served by the hub
// itself. The zero value means no gate — `access = "everyone"`, today's
// behaviour — for tests and for a hub without an identity plane.
//
// Admit answers one connect: ok, plus the mode /status shows
// ("open (cold)", "gated (N members)"). The relay is not a facet and
// no grant reaches the hook (ruled 2026-09-18): admission is a
// transport courtesy decided from the hub's safe-to-lose beat cache
// and the git blocklist, never an authorization — a member refused
// here has lost a path, not a capability.
type relayHook struct {
	URL   string
	Token string
	Admit func(id cert.ActorID) (ok bool, mode string)
}

// enabled is true when the hook is fully specified; a partial hook is
// a programming error and is treated as absent rather than half-wired.
func (h relayHook) enabled() bool {
	return h.URL != "" && h.Token != "" && h.Admit != nil
}

// newRelayToken mints the per-process bearer: 32 random bytes, hex.
func newRelayToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("relay: token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// relayConfig renders the relay's TOML. Plain HTTP on loopback: fly
// terminates TLS on 443 and forwards to the hub's :8080, which proxies
// to this port — the relay never owns a certificate. QUIC address
// discovery stays off (needs a relay-owned cert on UDP 7842 and only
// serves remote hole-punching, which ADR-0006 gave up). Metrics off:
// nothing scrapes them and they would be a fourth path to reason about.
//
// Access: `[access.http]` pointing back at the hub's hook when one is
// given (5gz; serde's externally tagged, lowercase AccessConfig enum ⇒
// the `http` table), else everyone. The bearer is deliberately absent
// from the file (relayTokenEnv).
func relayConfig(port int, hook relayHook) string {
	cfg := fmt.Sprintf(`enable_relay = true
http_bind_addr = "127.0.0.1:%d"
enable_quic_addr_discovery = false
enable_metrics = false
`, port)
	if !hook.enabled() {
		return cfg + "access = \"everyone\"\n"
	}
	return cfg + fmt.Sprintf("\n[access.http]\nurl = %q\n", hook.URL)
}

// relaySupervisor runs the iroh-relay binary as a child of the hub
// process and reverse-proxies the relay protocol from the hub's mux.
//
// Why a child and not a library: iroh-ffi binds only the client
// Endpoint; the relay server has no FFI surface (ADR-0022 option D).
// Why not a fly process group: a group is a separate machine that
// cannot share [http_service] — a second entrypoint (option C).
//
// The relay holds no key and no state (domain-model §2: "Shell (not an
// actor): … relay child"), so it starts with the process and keeps
// running across seals: a sealed hub still relays, which is exactly the
// property the identity plane needs when every remote path rides the
// hub (359.8.2 note, fbb).
type relaySupervisor struct {
	bin  string // path to iroh-relay
	port int    // loopback port the child binds
	dir  string // tmpfs dir holding relay.toml (deleted on Close)

	proxy *httputil.ReverseProxy
	hook  relayHook

	mu       sync.Mutex
	running  bool
	starts   int
	lastErr  error
	lastExit time.Time
	cancel   context.CancelFunc
	done     chan struct{}

	// Access-hook bookkeeping for /status: decisions since start, the
	// mode Admit last reported, and the ids already logged as denied
	// (one line per id per process — a refused stranger retries forever).
	admitted, denied int
	mode             string
	deniedSeen       map[cert.ActorID]struct{}
}

// newRelaySupervisor prepares (but does not start) a supervisor. The
// config file (no secrets in it) is written under the OS temp dir and
// removed by Close. A zero hook leaves the relay open to everyone.
func newRelaySupervisor(bin string, port int, hook relayHook) (*relaySupervisor, error) {
	if bin == "" {
		return nil, errors.New("relay: empty binary path")
	}
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("relay: bad port %d", port)
	}
	cfgDir, err := os.MkdirTemp("", "iroh-relay-")
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "relay.toml"), []byte(relayConfig(port, hook)), 0o600); err != nil {
		os.RemoveAll(cfgDir)
		return nil, fmt.Errorf("relay: %w", err)
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", fmt.Sprint(port))}
	r := &relaySupervisor{bin: bin, port: port, dir: cfgDir, hook: hook, done: make(chan struct{}), deniedSeen: map[cert.ActorID]struct{}{}}
	r.proxy = newRelayProxy(target, r.errorHandler)
	return r, nil
}

// newRelayProxy builds the reverse proxy for the relay routes. The
// stdlib proxy forwards WebSocket upgrades (Connection: Upgrade is
// honoured and both halves are copied until either side closes), which
// is all the 1.x relay protocol needs. Hop-by-hop headers are handled by
// the proxy; X-Forwarded-For is appended by default, and fly's
// Fly-Client-IP rides through untouched for the relay's own logging.
func newRelayProxy(target *url.URL, onErr func(http.ResponseWriter, *http.Request, error)) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	p.ErrorHandler = onErr
	// Long-lived upgraded connections must not inherit an idle timeout,
	// and the relay is on loopback, so a small pool is plenty.
	p.Transport = &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		MaxIdleConns:        8,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		MaxIdleConnsPerHost: 8,
	}
	return p
}

// errorHandler answers when the child is not accepting: 503, so a
// member's iroh client retries its home relay with backoff instead of
// treating the hub as gone.
func (r *relaySupervisor) errorHandler(w http.ResponseWriter, req *http.Request, err error) {
	log.Printf("relay proxy %s %s: %v", req.Method, req.URL.Path, err)
	http.Error(w, "relay unavailable", http.StatusServiceUnavailable)
}

// ServeHTTP proxies one relay-protocol request to the child.
func (r *relaySupervisor) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.proxy.ServeHTTP(w, req)
}

// register wires the relay routes onto mux, plus the access hook when
// the relay is gated.
func (r *relaySupervisor) register(mux *http.ServeMux) {
	for _, p := range relayPaths {
		mux.Handle(p, r)
	}
	if r.hook.enabled() {
		mux.HandleFunc("POST "+relayAccessPath, r.handleRelayAccess)
	}
}

// handleRelayAccess is the hook the child calls per connect (5gz).
// Contract (iroh-relay 1.1.0 AccessConfig::Http): grant ⇔ 200 with
// body exactly "true"; anything else denies. 404 off loopback and 401
// without the bearer keep it from being a public membership oracle.
func (r *relaySupervisor) handleRelayAccess(w http.ResponseWriter, req *http.Request) {
	if !isLoopback(req.RemoteAddr) {
		http.NotFound(w, req)
		return
	}
	if !r.bearerOK(req.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="relay"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, err := relayEndpointID(req.Header.Get(relayEndpointHeader))
	if err != nil {
		log.Printf("relay access: %v", err)
		http.Error(w, "false", http.StatusForbidden)
		return
	}
	ok, mode := r.hook.Admit(id)
	r.mu.Lock()
	r.mode = mode
	if ok {
		r.admitted++
	} else {
		r.denied++
	}
	_, seen := r.deniedSeen[id]
	if !ok && !seen && len(r.deniedSeen) < 1024 { // bounded: a scanner must not grow the hub
		r.deniedSeen[id] = struct{}{}
	}
	r.mu.Unlock()
	if !ok {
		if !seen {
			log.Printf("relay access: denied %s (%s)", id, mode)
		}
		http.Error(w, "false", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, "true")
}

// bearerOK compares the Authorization header against the per-process
// token in constant time.
func (r *relaySupervisor) bearerOK(authz string) bool {
	const prefix = "Bearer "
	if len(authz) <= len(prefix) || !strings.EqualFold(authz[:len(prefix)], prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(authz[len(prefix):])), []byte(r.hook.Token)) == 1
}

// relayEndpointID maps the hook header to the repo's actor id. iroh's
// EndpointId Display is the full 32-byte key as lowercase hex, and
// "ed:" + that hex is the same key's actor id (iroh-transport/nodeid.go).
func relayEndpointID(hdr string) (cert.ActorID, error) {
	id := cert.ActorID("ed:" + strings.ToLower(strings.TrimSpace(hdr)))
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("bad %s %q: %w", relayEndpointHeader, hdr, err)
	}
	return id, nil
}

// isLoopback reports whether a RemoteAddr is 127.0.0.0/8 or ::1.
func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Start launches the child and the supervision loop. It returns once
// the relay answers /generate_204 on loopback, or with the first exit
// error if the binary dies before that, or when ctx ends.
func (r *relaySupervisor) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
	go r.loop(ctx)
	return r.waitReady(ctx, 10*time.Second)
}

// loop restarts the child on exit with a capped backoff. A relay that
// keeps dying is logged, not fatal: the hub's other surfaces (config,
// KMS, unseal) do not depend on it.
func (r *relaySupervisor) loop(ctx context.Context) {
	defer close(r.done)
	backoff := time.Second
	for {
		err := r.runOnce(ctx)
		r.mu.Lock()
		r.running = false
		r.lastErr = err
		r.lastExit = time.Now()
		r.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		log.Printf("relay: child exited: %v; restarting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// runOnce runs the child to completion, streaming its output into the
// hub log with a prefix.
func (r *relaySupervisor) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, r.bin, "--config-path", filepath.Join(r.dir, "relay.toml"))
	cmd.Env = append(os.Environ(), "RUST_LOG="+relayLogLevel(), "NO_COLOR=1")
	if r.hook.enabled() {
		// The bearer rides the environment, not relay.toml (5gz).
		cmd.Env = append(cmd.Env, relayTokenEnv+"="+r.hook.Token)
	}
	// The relay logs to stderr via tracing; stdout is quiet.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	r.mu.Lock()
	r.running = true
	r.starts++
	r.mu.Unlock()
	log.Printf("relay: iroh-relay pid %d on 127.0.0.1:%d (plain HTTP, QAD off)", cmd.Process.Pid, r.port)
	go prefixLines("relay| ", pr)
	err := cmd.Wait()
	pw.Close()
	return err
}

// relayLogLevel keeps the child's tracing at info unless the operator
// asks for more; the relay is chatty at debug.
func relayLogLevel() string {
	if v := os.Getenv("RELAY_LOG"); v != "" {
		return v
	}
	return "info"
}

// prefixLines copies line-oriented output into the hub log.
func prefixLines(prefix string, r io.Reader) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	flush := func() {
		if len(buf) > 0 {
			log.Print(prefix + strings.TrimRight(string(buf), "\r\n"))
			buf = buf[:0]
		}
	}
	for {
		n, err := r.Read(tmp)
		for _, b := range tmp[:n] {
			buf = append(buf, b)
			if b == '\n' {
				flush()
			}
		}
		if err != nil {
			flush()
			return
		}
	}
}

// waitReady polls the relay's own probe on loopback.
func (r *relaySupervisor) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	probe := fmt.Sprintf("http://127.0.0.1:%d/generate_204", r.port)
	for {
		resp, err := client.Get(probe)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return nil
			}
		}
		r.mu.Lock()
		exited, lastErr := !r.running && r.starts > 0, r.lastErr
		r.mu.Unlock()
		if exited && lastErr != nil {
			return fmt.Errorf("relay: child exited before ready: %w", lastErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("relay: not ready after %s (last probe: %v)", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Close stops the child and removes the config dir.
func (r *relaySupervisor) Close() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		<-r.done
	}
	os.RemoveAll(r.dir)
}

// statusLine is the /status row: state, restarts, last error, and the
// access mode (5gz): "everyone" without a hook; with one, the mode
// Admit last reported ("open (cold)" / "gated (N members)") plus the
// admitted/denied counts since start.
func (r *relaySupervisor) statusLine() (line string, warn bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.running && r.starts == 1:
		return fmt.Sprintf("up — iroh-relay child on loopback :%d, proxied at /relay (plain HTTP behind fly TLS, QAD off)%s", r.port, r.accessLine()), false
	case r.running:
		return fmt.Sprintf("up — iroh-relay child on loopback :%d (restarted %d×, last exit %s: %v)%s", r.port, r.starts-1, ago(time.Now(), r.lastExit), r.lastErr, r.accessLine()), true
	case r.starts == 0:
		return "starting" + r.accessLine(), true
	default:
		return fmt.Sprintf("DOWN — iroh-relay exited %s: %v (restarting)%s", ago(time.Now(), r.lastExit), r.lastErr, r.accessLine()), true
	}
}

// accessLine is the " · access: …" suffix. Caller holds r.mu. The
// mode is whatever Admit said on the last connect; before the first
// one the hook is wired but has not been asked.
func (r *relaySupervisor) accessLine() string {
	if !r.hook.enabled() {
		return " · access: everyone"
	}
	mode := r.mode
	if mode == "" {
		mode = "hook wired, no connects yet"
	}
	return fmt.Sprintf(" · access: %s, %d admitted / %d denied", mode, r.admitted, r.denied)
}
