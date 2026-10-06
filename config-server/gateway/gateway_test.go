package gateway

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/meshtoken"
	"github.com/marnyg/talos-config/protocol/cert"
)

// testSigner is a fixed gateway key: the token issuer the tests pin.
// laptopKey is a well-formed caller id (the token refuses malformed subjects).
var (
	testSigner = meshtoken.NewSigner(ed25519.NewKeyFromSeed(make([]byte, 32)))
	laptopKey  = cert.ActorID("ed:" + strings.Repeat("ab", 32))
)

// The ingress-http facet end to end without iroh: an admitted stream
// (one end of a pipe) carries a request whose Host names a service and
// whose caller-supplied identity headers are lies; upstream sees the
// Host untouched, the lies gone, and the admitted identity in their
// place.
func TestProxyInjectsIdentity(t *testing.T) {
	var got http.Header
	var gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotHost = r.Header.Clone(), r.Host
		w.Header().Set("X-Upstream", "nginx")
		_, _ = io.WriteString(w, "hello "+r.Header.Get(HeaderName))
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)

	handler, stop := HTTPFacet("ingress-http", Proxy(u, testSigner), nil)
	defer stop()

	id := cert.Identity{Key: laptopKey, Name: "laptop", Groups: []string{"admins", "media"}}
	client, server := net.Pipe()
	go handler(context.Background(), server, id, laptopKey)

	req, _ := http.NewRequest("GET", "http://jackett.gw.mesh.internal/api?x=1", nil)
	req.Header.Set(HeaderNode, "ed:forged")
	req.Header.Set(HeaderGroups, "admins,root")
	req.Header.Set(meshtoken.Header, "forged.token.here")
	req.Header.Set("X-Custom", "kept")
	if err := req.Write(client); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(client), req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = client.Close()

	if resp.StatusCode != 200 || string(body) != "hello laptop" || resp.Header.Get("X-Upstream") != "nginx" {
		t.Fatalf("response: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if gotHost != "jackett.gw.mesh.internal" {
		t.Errorf("Host reached upstream as %q", gotHost)
	}
	if got.Get(HeaderNode) != string(laptopKey) || got.Get(HeaderName) != "laptop" || got.Get(HeaderGroups) != "admins,media" {
		t.Errorf("identity headers: %v", got)
	}
	if got.Get("X-Custom") != "kept" || got.Get("X-Forwarded-Host") != "jackett.gw.mesh.internal" {
		t.Errorf("other headers: %v", got)
	}
	if v := got.Get("X-Forwarded-For"); v != "" {
		t.Errorf("X-Forwarded-For should be absent (the peer is a key, not an address): %q", v)
	}
	// The signed token carries the admitted identity, bound to the Host;
	// the caller's forged one is gone.
	v, _ := meshtoken.NewVerifier(testSigner.ID())
	c, err := v.Verify(got.Get(meshtoken.Header), "jackett.gw.mesh.internal", time.Now())
	if err != nil {
		t.Fatalf("token: %v (%q)", err, got.Get(meshtoken.Header))
	}
	if c.Subject != laptopKey || c.Name != "laptop" || !c.HasGroup("media") || c.HasGroup("root") {
		t.Errorf("token claims: %+v", c)
	}
	if _, err := v.Verify(got.Get(meshtoken.Header), "sonarr.gw.mesh.internal", time.Now()); err == nil {
		t.Error("token for jackett verified for sonarr")
	}
}

// Mounted off a facet — no identity in the context — the proxy refuses
// rather than forwarding anonymously.
func TestProxyRefusesOffFacet(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1")
	rec := httptest.NewRecorder()
	Proxy(u, testSigner).ServeHTTP(rec, httptest.NewRequest("GET", "http://x/", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not over a facet") {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

// Upstream down: 502 to the caller, the facet server keeps serving.
func TestProxyUpstreamDown(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1")
	handler, stop := HTTPFacet("ingress-http", Proxy(u, testSigner), nil)
	defer stop()
	for i := 0; i < 2; i++ {
		client, server := net.Pipe()
		go handler(context.Background(), server, cert.Identity{Name: "laptop"}, "ed:x")
		req, _ := http.NewRequest("GET", "http://svc.gw.mesh.internal/", nil)
		if err := req.Write(client); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(client), req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("round %d: %d", i, resp.StatusCode)
		}
		_ = client.Close()
	}
}
