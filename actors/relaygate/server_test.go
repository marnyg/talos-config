package relaygate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// fakeRelay stands in for the relay binary: it upgrades /relay and
// echoes, answers the probes, and has pages of its own that must never
// be reachable through the gate.
type fakeRelay struct {
	mu   sync.Mutex
	auth []string // Authorization seen on each /relay upgrade
}

func (f *fakeRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/ping", "/generate_204":
		_, _ = io.WriteString(w, "probe")
	case RelayPath:
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = buf.Flush()
		_, _ = io.Copy(conn, buf)
	default:
		_, _ = io.WriteString(w, "relay internals")
	}
}

// site is a gate on the wall clock (the proxy's deadlines are real
// timers) in front of a fakeRelay.
type site struct {
	*world
	relayed *fakeRelay
	srv     *Server
	public  *httptest.Server
	hook    *httptest.Server
}

func newSite(t *testing.T) *site {
	t.Helper()
	w := newWorld(t)
	// Re-root the world on the wall clock.
	w.now = time.Now().Unix()
	w.gate = New(w.relay.ActorID(), []cert.Cert{w.consent(w.parent.ActorID(), w.now+86_400)})
	f := &fakeRelay{}
	backend := httptest.NewServer(f)
	t.Cleanup(backend.Close)
	u, _ := url.Parse(backend.URL)
	s := &site{world: w, relayed: f, srv: &Server{Gate: w.gate, Backend: u, HookToken: "hook-secret"}}
	s.public = httptest.NewServer(s.srv.Public())
	t.Cleanup(s.public.Close)
	s.hook = httptest.NewServer(s.srv.Hook())
	t.Cleanup(s.hook.Close)
	return s
}

// upgrade opens /relay through the gate as claim and returns the
// status and, on 101, the raw connection.
func (s *site) upgrade(t *testing.T, claim string) (int, net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(s.public.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	req := "GET /relay HTTP/1.1\r\nHost: relay\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"
	if claim != "" {
		req += "Authorization: Bearer " + claim + "\r\n"
	}
	if _, err := io.WriteString(conn, req+"\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, conn, br
}

func echo(conn net.Conn, br *bufio.Reader, msg string) error {
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(conn, msg); err != nil {
		return err
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(br, got); err != nil {
		return err
	}
	if string(got) != msg {
		return errors.New("echo mismatch: " + string(got))
	}
	return nil
}

func (s *site) get(t *testing.T, path string) (int, string) {
	t.Helper()
	res, err := http.Get(s.public.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestPublicSurface(t *testing.T) {
	s := newSite(t)
	for _, p := range []string{"/", "/metrics", "/relay/access", "/relay/other"} {
		if code, body := s.get(t, p); code != http.StatusNotFound || strings.Contains(body, "internals") {
			t.Fatalf("GET %s = %d %q, want 404 from the gate", p, code, body)
		}
	}
	for _, p := range probePaths {
		if code, body := s.get(t, p); code != http.StatusOK || body != "probe" {
			t.Fatalf("GET %s = %d %q, want the relay's probe", p, code, body)
		}
	}
	stranger := newKey(t).ActorID()
	for name, claim := range map[string]string{
		"no claim":        "",
		"stranger":        Claim(stranger),
		"not an id":       "let-me-in",
		"a non-relay key": "eth:0x0000000000000000000000000000000000000001",
	} {
		if code, _, _ := s.upgrade(t, claim); code != http.StatusUnauthorized {
			t.Fatalf("upgrade, %s: %d, want 401", name, code)
		}
	}
	if n := len(s.relayed.auth); n != 0 {
		t.Fatalf("%d refused upgrades reached the relay", n)
	}

	code, conn, br := s.upgrade(t, Claim(s.parent.ActorID()))
	if code != http.StatusSwitchingProtocols {
		t.Fatalf("member upgrade: %d, want 101", code)
	}
	if err := echo(conn, br, "hello"); err != nil {
		t.Fatalf("through the gate: %v", err)
	}
	if got := s.relayed.auth; len(got) != 1 || got[0] != "" {
		t.Fatalf("Authorization the relay saw: %q, want the claim stripped", got)
	}
}

func TestAdmitEndpoint(t *testing.T) {
	s := newSite(t)
	ctx := context.Background()
	admitURL := s.public.URL + AdmitPath

	if code, _ := s.get(t, AdmitPath); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d, want 405", code)
	}
	post := func(body []byte) int {
		res, err := http.Post(admitURL, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post([]byte("{")); code != http.StatusBadRequest {
		t.Fatalf("garbage: %d, want 400", code)
	}
	if code := post(bytes.Repeat([]byte("x"), maxPresentation+1)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: %d, want 413", code)
	}

	child := newKey(t).ActorID()
	good := Presentation{ID: child, Chain: []cert.Cert{s.toChild(child, 600)}}
	exp, err := Post(ctx, nil, s.public.URL+"/", good) // a trailing slash on the relay URL is iroh's normal form
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if exp != s.now+600 {
		t.Fatalf("exp = %d, want %d", exp, s.now+600)
	}
	if !s.admitted(child) {
		t.Fatal("posted presentation: not admitted")
	}

	other := newKey(t).ActorID()
	if _, err := Post(ctx, nil, s.public.URL, Presentation{ID: other, Chain: good.Chain}); !errors.Is(err, ErrRefused) {
		t.Fatalf("another key on the child's chain: err = %v, want ErrRefused", err)
	}
	if _, err := Post(ctx, nil, s.public.URL, Presentation{ID: "nobody"}); !errors.Is(err, ErrBadPresentation) {
		t.Fatalf("bad id: err = %v, want ErrBadPresentation", err)
	}
	s.gate.MaxEntries = 1
	if _, err := Post(ctx, nil, s.public.URL, Presentation{ID: other, Chain: []cert.Cert{s.toChild(other, 600)}}); !errors.Is(err, ErrFull) {
		t.Fatalf("full table: err = %v, want ErrFull", err)
	}
}

func TestHook(t *testing.T) {
	s := newSite(t)
	ask := func(bearer, idHeader string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, s.hook.URL, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.Header.Set(HookIDHeader, idHeader)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	hexOf := func(id cert.ActorID) string { return strings.TrimPrefix(string(id), "ed:") }
	member, stranger := hexOf(s.parent.ActorID()), hexOf(newKey(t).ActorID())

	if code, body := ask("hook-secret", member); code != http.StatusOK || body != "true" {
		t.Fatalf("member: %d %q, want 200 true", code, body)
	}
	if code, body := ask("hook-secret", strings.ToUpper(member)); code != http.StatusOK || body != "true" {
		t.Fatalf("member, upper-case hex: %d %q, want 200 true", code, body)
	}
	for name, h := range map[string]string{"stranger": stranger, "empty": "", "junk": "zz"} {
		if code, body := ask("hook-secret", h); code != http.StatusForbidden || body != "false" {
			t.Fatalf("%s: %d %q, want 403 false", name, code, body)
		}
	}
	// Without the relay's bearer the hook answers nobody — not even
	// about a member.
	for _, bearer := range []string{"", "wrong"} {
		if code, _ := ask(bearer, member); code != http.StatusUnauthorized {
			t.Fatalf("bearer %q: %d, want 401", bearer, code)
		}
	}
	s.srv.HookToken = ""
	if code, _ := ask("", member); code != http.StatusUnauthorized {
		t.Fatalf("no HookToken configured: %d, want 401 for everyone", code)
	}
}

// closedWithin reports whether the peer ends conn within d.
func closedWithin(conn net.Conn, br *bufio.Reader, d time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(d))
	_, err := br.ReadByte()
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

func TestProxyEndsConnectionAtExpiry(t *testing.T) {
	s := newSite(t)
	child := newKey(t).ActorID()
	if _, err := s.gate.Present(Presentation{ID: child, Chain: []cert.Cert{s.toChild(child, 2)}}); err != nil {
		t.Fatal(err)
	}
	code, conn, br := s.upgrade(t, Claim(child))
	if code != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d, want 101", code)
	}
	if err := echo(conn, br, "before"); err != nil {
		t.Fatal(err)
	}
	if !closedWithin(conn, br, 5*time.Second) {
		t.Fatal("connection still open 5 s after a 2 s credential")
	}
	if code, _, _ := s.upgrade(t, Claim(child)); code != http.StatusUnauthorized {
		t.Fatalf("upgrade after expiry: %d, want 401", code)
	}
}

func TestProxyKeepsConnectionAcrossRenewal(t *testing.T) {
	s := newSite(t)
	child := newKey(t).ActorID()
	if _, err := s.gate.Present(Presentation{ID: child, Chain: []cert.Cert{s.toChild(child, 2)}}); err != nil {
		t.Fatal(err)
	}
	code, conn, br := s.upgrade(t, Claim(child))
	if code != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d, want 101", code)
	}
	if _, err := s.gate.Present(Presentation{ID: child, Chain: []cert.Cert{s.toChild(child, 600)}}); err != nil {
		t.Fatal(err)
	}
	if closedWithin(conn, br, 4*time.Second) {
		t.Fatal("connection ended although its credential was renewed in time")
	}
	if err := echo(conn, br, "after"); err != nil {
		t.Fatalf("past the first credential's expiry: %v", err)
	}
}

// TestPresenter is the actor's side end to end over HTTP: a newborn
// presents its intro's wildcard link (the stamp solved for it), then
// its kit's chain.
func TestPresenter(t *testing.T) {
	s := newSite(t)
	ctx := context.Background()
	newborn := newKey(t).ActorID()
	present := Presenter(nil, s.public.URL, s.relay.ActorID(), newborn)

	if err := present(ctx, []cert.Cert{s.birth(10, 2)}); err != nil {
		t.Fatalf("intro link: %v", err)
	}
	if until, ok := s.gate.Admitted(newborn); !ok || until != s.now+2 {
		t.Fatalf("after the intro link: admitted %v until %d, want until the window's end %d", ok, until, s.now+2)
	}
	if err := present(ctx, []cert.Cert{s.toChild(newborn, 600)}); err != nil {
		t.Fatalf("kit chain: %v", err)
	}
	if until, _ := s.gate.Admitted(newborn); until != s.now+600 {
		t.Fatalf("after the kit chain: until %d, want %d", until, s.now+600)
	}
	// A chain for somebody else, presented as this actor: the gate's
	// verdict comes back as ErrRefused.
	other := newKey(t).ActorID()
	if err := present(ctx, []cert.Cert{s.toChild(other, 600)}); !errors.Is(err, ErrRefused) {
		t.Fatalf("another key's chain: err = %v, want ErrRefused", err)
	}
}

func TestPresentUntil(t *testing.T) {
	chain := []cert.Cert{{}}
	calls := 0
	flaky := func(context.Context, []cert.Cert) error {
		calls++
		if calls < 3 {
			return errors.New("connection refused")
		}
		return nil
	}
	if err := PresentUntil(context.Background(), flaky, chain, time.Millisecond); err != nil || calls != 3 {
		t.Fatalf("an unreachable gate: err = %v after %d calls, want nil after 3", err, calls)
	}

	// A verdict is final: the same chain cannot do better next time.
	for _, verdict := range []error{ErrRefused, ErrBadPresentation} {
		calls = 0
		err := PresentUntil(context.Background(), func(context.Context, []cert.Cert) error { calls++; return verdict }, chain, time.Millisecond)
		if !errors.Is(err, verdict) || calls != 1 {
			t.Fatalf("%v: err = %v after %d calls, want it back after 1", verdict, err, calls)
		}
	}

	// The birth window ends: the wait does too, and says why it waited.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	down := errors.New("gate down")
	err := PresentUntil(ctx, func(context.Context, []cert.Cert) error { return down }, chain, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, down) {
		t.Fatalf("deadline: err = %v, want the deadline and the last failure", err)
	}
}
