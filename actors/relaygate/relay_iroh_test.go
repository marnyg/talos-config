//go:build iroh

package relaygate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
)

// party is one key seen both ways: the cert signer and the iroh
// endpoint are the same Ed25519 key, which is the whole point.
type party struct {
	priv ed25519.PrivateKey
	cert.EdSigner
}

func newParty(t *testing.T) party {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return party{priv, cert.NewEdSigner(priv)}
}

// bind homes p on relayURL, claiming claim ("" ⇒ its own id).
func (p party) bind(t *testing.T, relayURL string, claim cert.ActorID) *irohtransport.Endpoint {
	t.Helper()
	if claim == "" {
		claim = p.ActorID()
	}
	e, err := irohtransport.Bind(p.priv, irohtransport.Options{BindAddr: "127.0.0.1:0", Relay: relayURL, RelayToken: Claim(claim)})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID() != p.ActorID() {
		t.Fatalf("endpoint id %s != cert id %s", e.ID(), p.ActorID())
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func online(e *irohtransport.Endpoint, d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if e.Online(ctx) != nil {
		return false
	}
	tags := e.Endpoints()
	return len(tags) > 0 && strings.HasPrefix(tags[0], irohtransport.TagRelay)
}

// echoOnce dials to through the relay hint alone and exchanges one
// message.
func echoOnce(from, to *irohtransport.Endpoint, relayURL string, d time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
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
	s, err := from.Dial(ctx, to.ID(), []string{irohtransport.TagRelay + relayURL})
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

// startRelay runs the real relay with its access hook at hookURL,
// presenting token, and returns its loopback URL.
func startRelay(t *testing.T, hookURL, token string) *url.URL {
	t.Helper()
	bin := os.Getenv("IROH_RELAY_BIN")
	if bin == "" {
		p, err := exec.LookPath("iroh-relay")
		if err != nil {
			t.Skip("iroh-relay not available (set IROH_RELAY_BIN); the gate is not tested against the relay")
		}
		bin = p
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cfg := filepath.Join(t.TempDir(), "relay.toml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(
		"enable_relay = true\nhttp_bind_addr = \"127.0.0.1:%d\"\nenable_metrics = false\nenable_quic_addr_discovery = false\n\n[access.http]\nurl = %q\n",
		port, hookURL)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--dev", "--config-path", cfg)
	cmd.Env = append(os.Environ(), "IROH_RELAY_HTTP_BEARER_TOKEN="+token, "SSL_CERT_FILE="+testCABundle(t))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay did not listen on %s; its stderr:\n%s", addr, stderr.String())
		}
	}
	u, _ := url.Parse("http://" + addr)
	return u
}

// TestGateInFrontOfRelay is ADR-0012 end to end against the real relay
// binary and real endpoints: certs in, connections out.
func TestGateInFrontOfRelay(t *testing.T) {
	ctx := context.Background()
	relay, member := newParty(t), newParty(t)
	now := time.Now().Unix()
	sign := func(iss party, aud string, cav cert.Caveats, ttl int64) cert.Cert {
		cav.Target = []cert.ActorID{relay.ActorID()}
		cav.Facet = []string{Facet}
		c, err := cert.Sign(cert.Cert{Aud: aud, Can: cert.VerbRelay, Cav: cav, Iat: time.Now().Unix(), Exp: time.Now().Unix() + ttl}, iss.EdSigner)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	consent, err := cert.Sign(cert.Cert{Aud: string(member.ActorID()), Can: cert.VerbRelay,
		Cav: cert.Caveats{Target: []cert.ActorID{relay.ActorID()}, Facet: []string{Facet}, Delegable: true},
		Iat: now - 1, Exp: now + 3600}, relay.EdSigner)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{Gate: New(relay.ActorID(), []cert.Cert{consent}), HookToken: "between-two-processes"}
	hook := httptest.NewServer(srv.Hook())
	t.Cleanup(hook.Close)
	srv.Backend = startRelay(t, hook.URL, srv.HookToken)
	public := httptest.NewServer(srv.Public())
	t.Cleanup(public.Close)
	at := public.URL // the only address an actor is given

	present := func(who party, chain ...cert.Cert) {
		t.Helper()
		p, err := Prepare(ctx, nil, relay.ActorID(), who.ActorID(), chain)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Post(ctx, nil, at, p); err != nil {
			t.Fatalf("present: %v", err)
		}
	}

	// member: nothing presented.
	m := member.bind(t, at, "")
	if !online(m, 10*time.Second) {
		t.Fatal("member: no home relay")
	}

	// stranger: nothing to present.
	if s := newParty(t).bind(t, at, ""); online(s, 3*time.Second) {
		t.Fatal("stranger: got a home relay")
	} else {
		_ = s.Close()
	}

	// child: refused until it presents its parent's link, then in by
	// itself, on the endpoint it already had.
	child := newParty(t)
	c := child.bind(t, at, "")
	if online(c, 3*time.Second) {
		t.Fatal("child before presenting: got a home relay")
	}
	present(child, sign(member, string(child.ActorID()), cert.Caveats{}, 600))
	if !online(c, 15*time.Second) {
		t.Fatal("child after presenting: no home relay")
	}
	if err := echoOnce(c, m, at, 10*time.Second); err != nil {
		t.Fatalf("child → member through the gated relay: %v", err)
	}

	// Somebody else claiming the child's id: the proxy lets the claim
	// through (the child IS admitted), the relay's hook does not.
	if th := newParty(t).bind(t, at, child.ActorID()); online(th, 3*time.Second) {
		t.Fatal("another key claiming the child's id: got a home relay")
	} else {
		_ = th.Close()
	}

	// newborn: the wildcard link from its intro, stamped for its key.
	birth := sign(member, "*", cert.Caveats{Postage: postage.Require(10)}, 120)
	newborn := newParty(t)
	present(newborn, birth)
	n := newborn.bind(t, at, "")
	if !online(n, 10*time.Second) {
		t.Fatal("newborn on its intro's link: no home relay")
	}
	if err := echoOnce(n, m, at, 10*time.Second); err != nil {
		t.Fatalf("newborn → parent (the #birth knock's path): %v", err)
	}

	// expiry: a 3 s credential. The proxy ends the connection, the
	// endpoint is unreachable until its chain is renewed and presented.
	short := newParty(t)
	present(short, sign(member, string(short.ActorID()), cert.Caveats{}, 3))
	d := short.bind(t, at, "")
	if !online(d, 5*time.Second) {
		t.Fatal("short-lived child: no home relay")
	}
	time.Sleep(4 * time.Second)
	if err := echoOnce(m, d, at, 3*time.Second); err == nil {
		t.Fatal("reached an endpoint whose credential expired a second ago")
	}
	present(short, sign(member, string(short.ActorID()), cert.Caveats{}, 600))
	began := time.Now()
	for {
		if err := echoOnce(m, d, at, 3*time.Second); err == nil {
			break
		} else if time.Since(began) > 20*time.Second {
			t.Fatalf("renewed and presented, still unreachable: %v", err)
		}
	}
	t.Logf("expired endpoint reachable again %s after presenting its renewed chain", time.Since(began).Round(100*time.Millisecond))
}
