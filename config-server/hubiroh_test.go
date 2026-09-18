//go:build iroh

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/issuer"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// TestHubBeatOverIroh is talos-config-e8d end to end, the hub's real
// shape minus fly: the hubkey bound on iroh and homed on "its" relay
// (a local iroh-relay --dev standing in for the relay child, dialled by
// one name, advertised by another); a member with its own NodeId, homed
// at the advertised name, learns the hub's location from /.well-known
// and runs the beat — #renew and #bundle — through the relay. Enroll's
// in-process wire is untouched: the Issuer serves both from one inbox
// (actor.Multi). Needs IROH_RELAY_BIN (or iroh-relay on PATH); skipped
// otherwise. nix runs it (-tags iroh).
func TestHubBeatOverIroh(t *testing.T) {
	if irohTagRelay != irohtransport.TagRelay {
		t.Fatalf("hubseal.go irohTagRelay %q drifted from irohtransport.TagRelay %q", irohTagRelay, irohtransport.TagRelay)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	home := startRelay(t)
	public := strings.Replace(home, "127.0.0.1", "localhost", 1)

	m := testHubManagerOn(t, []string{wellKnownAddr}, "", irohHubTransport(home, public, "127.0.0.1:0"))
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	s := &server{root: m.root, store: deviceflow.NewStore(), sessions: newSessionStore(), adminAddrs: []string{wellKnownAddr}, hub: m}
	ts := httptest.NewServer(s.mux())
	t.Cleanup(ts.Close)

	// Sealed identity: the endpoint is up and named, the location is
	// published (it is a fact about the process, not about authority),
	// and the inbox refuses.
	if !strings.Contains(m.endpoints(), irohtransport.TagRelay+public) {
		t.Fatalf("endpoints %q lack the advertised relay", m.endpoints())
	}
	go m.listen(ctx)
	loc := fetchCert(t, ctx, ts.URL+wellKnownReachMeAtPath)
	if loc.Iss != m.issuer.ID() || loc.Can != cert.VerbReachMeAt || !slices.Contains(loc.Cav.Endpoints, irohtransport.TagRelay+public) {
		t.Fatalf("reach-me-at %+v", loc)
	}

	// The member: its own key on iroh, homed at the public name.
	_, nodePriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nodeEp, err := irohtransport.Bind(nodePriv, irohtransport.Options{BindAddr: "127.0.0.1:0", Relay: public})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeEp.Close() })
	node := cert.NewEdSigner(nodePriv)
	n := actor.New(node, nodeEp)
	if err := n.UpdateLocation(m.issuer.ID(), &loc); err != nil {
		t.Fatal(err)
	}
	go n.Listen(ctx)

	// Sealed hub: a beat is refused at the chain (no consent held yet).
	if _, err := n.Send(ctx, m.issuer.ID(), issuer.FacetBundle, nil); err == nil {
		t.Fatal("sealed hub served #bundle")
	}

	// Unseal identity, mint the member's kit as Enroll would, beat.
	if _, err := m.unsealIssuer(speakAsSig(t, m, testKey(t))); err != nil {
		t.Fatal(err)
	}
	kit, err := m.issuer.Mint(node.ActorID(), "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range issuer.BeatFacets {
		n.Grant(m.issuer.ID(), f, kit.BeatGrant, kit.SpeakAs)
	}

	req, err := issuer.EncodeBundleRequest(kit.Member)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := n.Send(ctx, m.issuer.ID(), issuer.FacetBundle, req)
	if err != nil {
		t.Fatalf("#bundle over iroh: %v", err)
	}
	b, err := issuer.DecodeBundle(rep.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Grants) == 0 || b.SpeakAs.Aud != string(m.issuer.ID()) {
		t.Fatalf("bundle %+v", b)
	}
	for _, g := range b.Grants {
		if g.Iss != m.issuer.ID() || cert.Verify(g) != nil {
			t.Fatalf("grant not hub-signed: %+v", g)
		}
	}
	// The reply piggybacked the hub's location; the member's cache is
	// current without the WAN fetch from here on.
	if got := n.GetLocation(m.issuer.ID()); got == nil || !slices.Contains(got.Cav.Endpoints, irohtransport.TagRelay+public) {
		t.Fatalf("hub location after the beat: %+v", got)
	}

	renewReq, err := actor.EncodeRenewRequest([]cert.Cert{kit.Member}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, err = n.Send(ctx, m.issuer.ID(), actor.FacetRenew, renewReq)
	if err != nil {
		t.Fatalf("#renew over iroh: %v", err)
	}
	renewed, errs, err := actor.DecodeRenewResponse(rep.Payload)
	if err != nil || len(renewed) != 1 || errs[0] != nil {
		t.Fatalf("renew: %v %v %v", renewed, errs, err)
	}
	if renewed[0].Aud != string(node.ActorID()) || renewed[0].Iss != m.issuer.ID() || renewed[0].Cav.Name != "laptop" {
		t.Fatalf("renewed member: %+v", renewed[0])
	}

	// /status names the endpoint.
	resp, err := ts.Client().Get(ts.URL + "/sealed")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), irohtransport.TagRelay+public) {
		t.Fatalf("/sealed does not name the endpoint:\n%s", body)
	}
}

// fetchCert GETs a cert document, retrying 503 briefly: the hub
// publishes its location from listen()'s goroutine.
func fetchCert(t *testing.T, ctx context.Context, url string) cert.Cert {
	t.Helper()
	for {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			c, err := cert.DecodeCert(body)
			if err != nil {
				t.Fatal(err)
			}
			if err := cert.Verify(c); err != nil {
				t.Fatal(err)
			}
			return c
		}
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s: %d %s", url, resp.StatusCode, body)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s stayed 503", url)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// ---- relay helper (mirrors iroh-transport/handshake_test.go) --------------

func startRelay(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("IROH_RELAY_BIN")
	if bin == "" {
		p, err := exec.LookPath("iroh-relay")
		if err != nil {
			t.Skip("iroh-relay not available (set IROH_RELAY_BIN); hub-over-iroh not tested")
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
	if err := os.WriteFile(cfg, []byte(relayConfig(port)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--dev", "--config-path", cfg)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return "http://" + addr
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("relay did not listen on %s", addr)
	return ""
}
