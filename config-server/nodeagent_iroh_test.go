//go:build iroh

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/config-server/policy"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// TestNodeAgentEndToEnd is talos-config-359.8.3 minus the Talos box: the
// hub on iroh behind a local relay; a served machine config with a boot
// token; the agent enrolls with it, beats, lands in the name map, and
// forwards an apid stream for an admin device presenting its bundle —
// while a media device and a stranger are refused. Then it restarts
// from its state dir with no token and beats again.
func TestNodeAgentEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	home := startRelay(t)
	public := strings.Replace(home, "127.0.0.1", "localhost", 1)

	m := testHubManagerOn(t, []string{wellKnownAddr}, "", irohHubTransport(home, public, "127.0.0.1:0"))
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.unsealIssuer(speakAsSig(t, m, testKey(t))); err != nil {
		t.Fatal(err)
	}
	s := &server{root: m.root, store: deviceflow.NewStore(), sessions: newSessionStore(), adminAddrs: []string{wellKnownAddr}, hub: m}
	ts := httptest.NewServer(s.mux())
	t.Cleanup(ts.Close)
	m.publicURL = ts.URL
	go m.listen(ctx)
	fetchCert(t, ctx, ts.URL+wellKnownReachMeAtPath) // published

	// The served config carries the token; the agent's Relay is the
	// local relay's public name (on fly both are one hostname).
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/config: %d %s", rec.Code, rec.Body.String())
	}
	cfg := agentConfigFrom(t, rec.Body.String())
	cfg.Relay = public

	// apid stand-in.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()

	state := nodeagent.State{Dir: t.TempDir()}
	logger := log.New(testWriter{t}, "agent: ", 0)
	start := func(cfg nodeagent.Config) (*nodeagent.Agent, context.CancelFunc) {
		a, err := nodeagent.Start(nodeagent.Options{
			Config: cfg, State: state, Forward: map[string]string{"apid": ln.Addr().String()},
			BindAddr: "127.0.0.1:0", Log: logger, BeatEvery: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		actx, acancel := context.WithCancel(ctx)
		go func() {
			if err := a.Run(actx); err != nil && actx.Err() == nil {
				t.Errorf("agent run: %v", err)
			}
		}()
		return a, func() { acancel(); _ = a.Close() }
	}
	a, stop := start(cfg)
	waitFor(t, ctx, "first beat", func() bool { return a.Beats() == 1 })

	kit := a.Kit()
	if kit.Member.Cav.Name != "aa-bb-cc-dd-ee-ff" || kit.Member.Cav.Groups[0] != "machines" {
		t.Fatalf("member: %+v", kit.Member)
	}
	if e := issuer.Lookup(a.Bundle().NameMap, "aa-bb-cc-dd-ee-ff"); len(e) != 1 || e[0].Location == nil || e[0].Location.Iss != a.ID() {
		t.Fatalf("node not in the name map with its location: %+v", a.Bundle().NameMap)
	}
	// The token is spent: a second redemption is refused, so the Kit
	// on disk is the only way back in.
	if _, err := nodeagent.Enroll(ctx, ts.Client(), ts.URL, a.ID(), cfg.Token); !errors.Is(err, nodeagent.ErrTokenDead) {
		t.Fatalf("token reuse: %v", err)
	}

	// Two devices beat the hub for their bundles: an admin (apid granted
	// by the recipe) and a media member (no node facet at all).
	device := func(name, group string) (*irohtransport.Endpoint, cert.Bundle) {
		t.Helper()
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		ep, err := irohtransport.Bind(priv, irohtransport.Options{BindAddr: "127.0.0.1:0", Relay: public})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ep.Close() })
		d := actor.New(cert.NewEdSigner(priv), ep)
		loc := fetchCert(t, ctx, ts.URL+wellKnownReachMeAtPath)
		if err := d.UpdateLocation(m.issuer.ID(), &loc); err != nil {
			t.Fatal(err)
		}
		k, err := m.issuer.Mint(d.ID(), name, []string{group})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range issuer.BeatFacets {
			d.Grant(m.issuer.ID(), f, k.BeatGrant, k.SpeakAs)
		}
		req, _ := issuer.EncodeBundleRequest(k.Member)
		rep, err := d.Send(ctx, m.issuer.ID(), issuer.FacetBundle, req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := issuer.DecodeBundle(rep.Payload)
		if err != nil {
			t.Fatal(err)
		}
		return ep, cert.Bundle{Member: k.Member, Grants: b.Grants, SpeakAs: []cert.Cert{k.SpeakAs}}
	}
	hints := a.Endpoint().Endpoints()
	alpn := policy.ALPN("apid")

	adminEp, adminBundle := device("laptop", "admins")
	pre, _ := cert.EncodeBundle(adminBundle)
	conn, err := adminEp.DialConn(ctx, a.ID(), hints, alpn, pre)
	if err != nil {
		t.Fatalf("admin → apid: %v", err)
	}
	raw, err := conn.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	msg := bytes.Repeat([]byte("talosctl "), 4096)
	if _, err := raw.Write(msg); err != nil {
		t.Fatal(err)
	}
	_ = raw.CloseWrite()
	if got, err := io.ReadAll(raw); err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("echo through the node: %d bytes, %v", len(got), err)
	}
	_ = raw.Close()
	_ = conn.Close()

	mediaEp, mediaBundle := device("tv", "media")
	pre, _ = cert.EncodeBundle(mediaBundle)
	var refused *irohtransport.ErrRefused
	if _, err := mediaEp.DialConn(ctx, a.ID(), hints, alpn, pre); !errors.As(err, &refused) {
		t.Fatalf("media → apid: %v", err)
	}

	// A stranger: self-signed member cert, no chain to this node's consent.
	_, strangerPriv, _ := ed25519.GenerateKey(rand.Reader)
	strangerEp, err := irohtransport.Bind(strangerPriv, irohtransport.Options{BindAddr: "127.0.0.1:0", Relay: public})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = strangerEp.Close() })
	self := cert.NewEdSigner(strangerPriv)
	fake, _ := cert.Sign(cert.Cert{Aud: string(self.ActorID()), Can: cert.VerbMember, Cav: cert.Caveats{Name: "laptop", Groups: []string{"admins"}}, Iat: 1, Exp: 1 << 40}, self)
	pre, _ = cert.EncodeBundle(cert.Bundle{Member: fake, Grants: adminBundle.Grants, SpeakAs: adminBundle.SpeakAs})
	if _, err := strangerEp.DialConn(ctx, a.ID(), hints, alpn, pre); !errors.As(err, &refused) {
		t.Fatalf("stranger → apid: %v", err)
	}

	// Restart from state alone: no token, same NodeId, beats again.
	stop()
	id := a.ID()
	cfg.Token = ""
	a, stop = start(cfg)
	defer stop()
	if a.ID() != id {
		t.Fatalf("NodeId changed across restart: %s → %s", id, a.ID())
	}
	if a.Kit() == nil {
		t.Fatal("Kit not loaded from state")
	}
	waitFor(t, ctx, "beat after restart", func() bool { return a.Beats() == 1 })
	if len(issuer.Lookup(a.Bundle().NameMap, "aa-bb-cc-dd-ee-ff")) != 1 {
		t.Fatalf("name map after restart: %+v", a.Bundle().NameMap)
	}
	// The admin still gets in (a fresh consent was signed at start).
	conn, err = adminEp.DialConn(ctx, a.ID(), a.Endpoint().Endpoints(), alpn, mustEncodeBundle(t, adminBundle))
	if err != nil {
		t.Fatalf("admin → apid after restart: %v", err)
	}
	_ = conn.Close()
}

func mustEncodeBundle(t *testing.T, b cert.Bundle) []byte {
	t.Helper()
	raw, err := cert.EncodeBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func waitFor(t *testing.T, ctx context.Context, what string, ok func() bool) {
	t.Helper()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
