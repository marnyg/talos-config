package meshtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMintsOnceAndReuses(t *testing.T) {
	dir := t.TempDir()
	a, err := Load(dir, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(dir, "ignored-on-reload")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Root().Equal(b.Root()) {
		t.Error("second Load minted a new root")
	}
	if got := a.Root().Subject.CommonName; !strings.HasPrefix(got, "talos-mesh device CA ") || !strings.HasSuffix(got, "(laptop)") {
		t.Errorf("CN %q", got)
	}
	st, _ := os.Stat(filepath.Join(dir, KeyFile))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("ca.key mode %v", st.Mode().Perm())
	}
	st, _ = os.Stat(filepath.Join(dir, CertFile))
	if st.Mode().Perm() != 0o644 {
		t.Errorf("ca.crt mode %v", st.Mode().Perm())
	}
	if a.RootPath() != filepath.Join(dir, CertFile) {
		t.Errorf("RootPath %q", a.RootPath())
	}
}

// The root is what ADR-0033 promised: a P-256 CA, pathLen 0, name-
// constrained to the zone and nothing else, critical.
func TestRootShape(t *testing.T) {
	a, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	r := a.Root()
	if r.PublicKeyAlgorithm != x509.ECDSA || !r.IsCA || !r.MaxPathLenZero {
		t.Errorf("alg %v CA %v pathLenZero %v", r.PublicKeyAlgorithm, r.IsCA, r.MaxPathLenZero)
	}
	if len(r.PermittedDNSDomains) != 1 || r.PermittedDNSDomains[0] != Zone || !r.PermittedDNSDomainsCritical {
		t.Errorf("nameConstraints %v critical %v", r.PermittedDNSDomains, r.PermittedDNSDomainsCritical)
	}
	if r.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("root lacks keyCertSign")
	}
	if !strings.Contains(string(a.RootPEM()), "BEGIN CERTIFICATE") {
		t.Error("RootPEM is not PEM")
	}
}

func TestLeafVerifiesAndIsCached(t *testing.T) {
	a, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	c1, err := a.Leaf("workouts.gw.mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := a.Leaf("Workouts.GW.mesh.internal.")
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 {
		t.Error("leaf not cached across case/trailing-dot spellings")
	}
	if c1.Leaf.PublicKeyAlgorithm != x509.ECDSA {
		t.Errorf("leaf alg %v (browsers negotiate no Ed25519 leaves)", c1.Leaf.PublicKeyAlgorithm)
	}
	if len(c1.Leaf.ExtKeyUsage) != 1 || c1.Leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("EKU %v", c1.Leaf.ExtKeyUsage)
	}
	if _, err := c1.Leaf.Verify(x509.VerifyOptions{Roots: a.Pool(), DNSName: "workouts.gw.mesh.internal"}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if _, err := c1.Leaf.Verify(x509.VerifyOptions{Roots: a.Pool(), DNSName: "other.gw.mesh.internal"}); err == nil {
		t.Error("leaf for workouts verified for other")
	}
	// Chain as served: leaf + root, so a verifier that lacks the root
	// still sees who signed.
	if len(c1.Certificate) != 2 {
		t.Errorf("chain length %d", len(c1.Certificate))
	}
}

func TestLeafRefusesOutsideZone(t *testing.T) {
	a, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"example.com", "mesh.internal", "evil.com.mesh.internal.example.com", ".mesh.internal", "a..b.mesh.internal", "mesh.internal.", "x/y.mesh.internal"} {
		if _, err := a.Leaf(n); err == nil {
			t.Errorf("Leaf(%q) succeeded", n)
		}
	}
}

func TestLeafRenewsNearExpiry(t *testing.T) {
	a, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	a.now = func() time.Time { return t0 }
	c1, _ := a.Leaf("cp1.mesh.internal")
	a.now = func() time.Time { return t0.Add(leafValidity - leafRenewBefore/2) }
	c2, _ := a.Leaf("cp1.mesh.internal")
	if c1 == c2 {
		t.Error("leaf not re-minted inside the renewal window")
	}
	if !c2.Leaf.NotBefore.Before(t0.Add(leafValidity - leafRenewBefore/2)) {
		t.Error("renewed leaf not yet valid")
	}
}

func TestLoadRefusesHalfState(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, KeyFile))
	if _, err := Load(dir, ""); err == nil || !strings.Contains(err.Error(), "one of") {
		t.Errorf("Load over a cert without its key: %v", err)
	}
}

// A browser's view, end to end without iroh: the flow lands on the
// front, the handshake chains to the device root, the request reaches
// the "member" as plain HTTP with Host intact and X-Forwarded-Proto:
// https, and the response comes back over TLS.
func TestFrontTerminatesAndForwards(t *testing.T) {
	a, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	var gotHost, gotProto, gotFwdHost, gotPort string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotProto, gotFwdHost, gotPort = r.Host, r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Port")
		w.Header().Set("X-Upstream", "gw")
		_, _ = io.WriteString(w, "hello "+r.URL.Path)
	}))
	defer upstream.Close()

	const name = "workouts.gw.mesh.internal"
	var opened int
	f, err := NewFront(FrontOptions{
		Authority: a,
		Resolve: func(n string) (Target, error) {
			if n != name {
				return Target{}, errors.New("unknown name")
			}
			return Target{Member: "gw", Facet: "ingress-http"}, nil
		},
		Open: func(ctx context.Context, tg Target) (net.Conn, error) {
			if tg.Member != "gw" || tg.Facet != "ingress-http" {
				t.Errorf("Open(%+v)", tg)
			}
			opened++
			return net.Dial("tcp", strings.TrimPrefix(upstream.URL, "http://"))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// The "tun": a loopback listener whose every accept is one flow to
	// name's fake IP :443.
	tun, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			c, err := tun.Accept()
			if err != nil {
				return
			}
			go func() {
				if _, err := f.Serve(ctx, c, name); err != nil {
					t.Errorf("Serve: %v", err)
					_ = c.Close()
				}
			}()
		}
	}()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("tcp", tun.Addr().String())
		},
		TLSClientConfig: &tls.Config{RootCAs: a.Pool(), ServerName: name},
	}}
	for i := 0; i < 2; i++ {
		resp, err := client.Get("https://" + name + "/api/x")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "hello /api/x" || resp.Header.Get("X-Upstream") != "gw" {
			t.Fatalf("round %d: %d %q %v", i, resp.StatusCode, body, resp.Header)
		}
		if resp.TLS == nil || resp.TLS.NegotiatedProtocol == "h2" {
			t.Errorf("round %d: TLS %+v", i, resp.TLS)
		}
	}
	if gotHost != name || gotProto != "https" || gotFwdHost != name || gotPort != "443" {
		t.Errorf("upstream saw Host %q Proto %q Fwd-Host %q Port %q", gotHost, gotProto, gotFwdHost, gotPort)
	}
	if opened != 1 {
		t.Errorf("opened %d upstream streams for two keep-alive requests on one client conn", opened)
	}
}

// A client that does not hold the root fails the handshake — that is
// the whole point; and a name the directory lacks is refused before any
// TLS happens.
func TestFrontRefusals(t *testing.T) {
	a, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewFront(FrontOptions{
		Authority: a,
		Resolve:   func(n string) (Target, error) { return Target{}, errors.New("unknown name " + n) },
		Open:      func(context.Context, Target) (net.Conn, error) { return nil, errors.New("unreachable") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	if _, err := f.Serve(context.Background(), c2, "nobody.mesh.internal"); err == nil || !strings.Contains(err.Error(), "unknown name") {
		t.Errorf("Serve unknown: %v", err)
	}

	// Known name, untrusting client.
	g, _ := NewFront(FrontOptions{
		Authority: a,
		Resolve:   func(string) (Target, error) { return Target{Member: "gw", Facet: "ingress-http"}, nil },
		Open:      func(context.Context, Target) (net.Conn, error) { return nil, errors.New("unreachable") },
	})
	defer g.Close()
	tun, _ := net.Listen("tcp", "127.0.0.1:0")
	defer tun.Close()
	go func() {
		c, err := tun.Accept()
		if err != nil {
			return
		}
		_, _ = g.Serve(context.Background(), c, "x.gw.mesh.internal")
	}()
	raw, err := net.Dial("tcp", tun.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tc := tls.Client(raw, &tls.Config{RootCAs: x509.NewCertPool(), ServerName: "x.gw.mesh.internal"})
	if err := tc.Handshake(); err == nil {
		t.Error("handshake succeeded without the device root")
	}
}

// Upstream unreachable: 502 over TLS, the front keeps serving.
func TestFrontUpstreamDown(t *testing.T) {
	a, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewFront(FrontOptions{
		Authority: a,
		Resolve:   func(string) (Target, error) { return Target{Member: "gw", Facet: "ingress-http"}, nil },
		Open:      func(context.Context, Target) (net.Conn, error) { return nil, errors.New("member gone") },
		Log:       quietLog(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tun, _ := net.Listen("tcp", "127.0.0.1:0")
	defer tun.Close()
	go func() {
		for {
			c, err := tun.Accept()
			if err != nil {
				return
			}
			go f.Serve(context.Background(), c, "x.gw.mesh.internal")
		}
	}()
	client := &http.Client{Transport: &http.Transport{
		DialContext:     func(context.Context, string, string) (net.Conn, error) { return net.Dial("tcp", tun.Addr().String()) },
		TLSClientConfig: &tls.Config{RootCAs: a.Pool(), ServerName: "x.gw.mesh.internal"},
	}}
	for i := 0; i < 2; i++ {
		resp, err := client.Get("https://x.gw.mesh.internal/")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("round %d: %d", i, resp.StatusCode)
		}
	}
}

func quietLog(t *testing.T) *log.Logger {
	return log.New(testWriter{t}, "", 0)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
