package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/cert"
)

// fakeFacetConn is one inbound facet connection without iroh: the
// preamble is given, streams are pushed in by the test.
type fakeFacetConn struct {
	alpn    string
	peer    cert.ActorID
	pre     []byte
	streams chan io.ReadWriteCloser
	closed  chan struct{}
	once    sync.Once

	mu       sync.Mutex
	admitted bool
	refused  string
}

func newFakeFacetConn(alpn string, peer cert.ActorID, b cert.Bundle) *fakeFacetConn {
	pre, err := cert.EncodeBundle(b)
	if err != nil {
		panic(err)
	}
	return &fakeFacetConn{alpn: alpn, peer: peer, pre: pre, streams: make(chan io.ReadWriteCloser), closed: make(chan struct{})}
}

func (c *fakeFacetConn) ALPN() string                             { return c.alpn }
func (c *fakeFacetConn) Peer() cert.ActorID                       { return c.peer }
func (c *fakeFacetConn) Preamble(context.Context) ([]byte, error) { return c.pre, nil }
func (c *fakeFacetConn) Admit(context.Context) error {
	c.mu.Lock()
	c.admitted = true
	c.mu.Unlock()
	return nil
}
func (c *fakeFacetConn) Refuse(_ context.Context, reason string) error {
	c.mu.Lock()
	c.refused = reason
	c.mu.Unlock()
	return nil
}
func (c *fakeFacetConn) Close() error { c.once.Do(func() { close(c.closed) }); return nil }

func (c *fakeFacetConn) Accept(ctx context.Context) (io.ReadWriteCloser, error) {
	select {
	case s := <-c.streams:
		return s, nil
	case <-c.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *fakeFacetConn) state() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.admitted, c.refused
}

// open is the dialer's side: one stream through the facet connection,
// returned as the client end of a pipe.
func (c *fakeFacetConn) open(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case c.streams <- server:
		return client, nil
	case <-c.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// facetClient is an http.Client whose every connection is one stream
// on c.
func facetClient(c *fakeFacetConn) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return c.open(ctx) },
		DisableKeepAlives: true,
	}, Timeout: 5 * time.Second}
}

// memberOf mints a member of group with the recipe's grants for it,
// signed by the hub's hot key as #bundle would.
func memberOf(t *testing.T, m *hubManager, name, group string) (cert.ActorID, cert.Bundle) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	id := cert.NewEdSigner(priv).ActorID()
	k, err := m.issuer.Mint(id, name, []string{group})
	if err != nil {
		t.Fatal(err)
	}
	recipe, _, err := m.issuer.Policy()
	if err != nil {
		t.Fatal(err)
	}
	var grants []cert.Cert
	for _, g := range policy.Compile(recipe, policy.Caller{Key: id, Name: name, Groups: []string{group}}, m.issuer.Actor.Now()) {
		signed, err := cert.Sign(g, m.issuer.Actor.Signer)
		if err != nil {
			t.Fatal(err)
		}
		grants = append(grants, signed)
	}
	return id, cert.Bundle{Member: k.Member, Grants: grants, SpeakAs: []cert.Cert{k.SpeakAs}}
}

// TestHubHTTPFacet is talos-config-359.8.2.4 without iroh: the hub's
// /config over the hub-http facet — an admin fetches a composed config,
// a media member is admitted by the recipe but refused at the route,
// and a stranger never gets past the preamble; sealed, nobody does.
func TestHubHTTPFacet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := testHubManager(t, []string{wellKnownAddr}, "")
	s := &server{root: m.root, store: deviceflow.NewStore(), sessions: newSessionStore(), adminAddrs: []string{wellKnownAddr}, hub: m}
	srv, ln := facetServer(s.hubFacetMux())
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })
	alpn := policy.ALPN("hub-http")

	// Sealed: no consent exists, so even a well-formed bundle is refused.
	// (Minting needs an unsealed hub, so unseal, mint, then check the
	// decision against a sealed-looking receiver is the hold's job —
	// here the observable is the refusal before any speak-as is held.)
	_, strangerPriv, _ := ed25519.GenerateKey(rand.Reader)
	self := cert.NewEdSigner(strangerPriv)
	fake, _ := cert.Sign(cert.Cert{Aud: string(self.ActorID()), Can: cert.VerbMember, Cav: cert.Caveats{Name: "laptop", Groups: []string{"admins"}}, Iat: 1, Exp: 1 << 40}, self)
	sealed := newFakeFacetConn(alpn, self.ActorID(), cert.Bundle{Member: fake})
	m.handleFacetConn(ctx, sealed, ln)
	if adm, ref := sealed.state(); adm || ref == "" {
		t.Fatalf("sealed hub: admitted=%v refused=%q", adm, ref)
	}

	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.unsealIssuer(speakAsSig(t, m, testKey(t))); err != nil {
		t.Fatal(err)
	}

	// Admin: admitted, and /config composes the declared machine.
	adminID, adminBundle := memberOf(t, m, "laptop", "admins")
	admin := newFakeFacetConn(alpn, adminID, adminBundle)
	go m.handleFacetConn(ctx, admin, ln)
	resp, err := facetClient(admin).Get("http://hub.mesh.internal/config?mac=aa-bb-cc-dd-ee-ff")
	if err != nil {
		t.Fatalf("admin GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "version: v1alpha1") {
		t.Fatalf("admin /config: %d %s", resp.StatusCode, body)
	}
	if adm, ref := admin.state(); !adm || ref != "" {
		t.Fatalf("admin: admitted=%v refused=%q", adm, ref)
	}
	// A second stream on the same connection is a second HTTP connection.
	resp, err = facetClient(admin).Get("http://hub.mesh.internal/config?mac=00-00-00-00-00-00")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown mac: %d", resp.StatusCode)
	}
	admin.Close()

	// Media: the recipe grants hub-http, so the facet admits; the route
	// does not.
	mediaID, mediaBundle := memberOf(t, m, "tv", "media")
	media := newFakeFacetConn(alpn, mediaID, mediaBundle)
	go m.handleFacetConn(ctx, media, ln)
	resp, err = facetClient(media).Get("http://hub.mesh.internal/config?mac=aa-bb-cc-dd-ee-ff")
	if err != nil {
		t.Fatalf("media GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("media /config: %d, want 403", resp.StatusCode)
	}
	if adm, _ := media.state(); !adm {
		t.Fatal("media: recipe grants hub-http; the facet should admit")
	}
	media.Close()

	// Stranger: an admin-looking member cert nobody the hub trusts signed.
	stranger := newFakeFacetConn(alpn, self.ActorID(), cert.Bundle{Member: fake, Grants: adminBundle.Grants, SpeakAs: adminBundle.SpeakAs})
	m.handleFacetConn(ctx, stranger, ln)
	if adm, ref := stranger.state(); adm || ref != "not authorized" {
		t.Fatalf("stranger: admitted=%v refused=%q", adm, ref)
	}

	// Wrong ALPN: an admin on a node facet the hub does not serve.
	off := newFakeFacetConn(policy.ALPN("apid"), adminID, adminBundle)
	m.handleFacetConn(ctx, off, ln)
	if adm, ref := off.state(); adm || ref == "" {
		t.Fatalf("apid on the hub: admitted=%v refused=%q", adm, ref)
	}

	// Off the facet, the route is closed: no identity in the context.
	rec := httptest.NewRecorder()
	s.hubFacetMux().ServeHTTP(rec, httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no identity: %d", rec.Code)
	}
}
