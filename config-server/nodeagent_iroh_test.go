//go:build iroh

package main

import (
	"bufio"
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
	"sync/atomic"
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
// from its state dir with no token and beats again. Then the hub is
// redeployed under a new hubkey: the node, which dials nothing on its
// own, beats when its pooled connection to the dead process closes and
// again the moment the new one is unsealed (decision z2go); a caller's
// next dial to the hub goes through without a restart (ipt7). Last, a
// rolling redeploy leaves the old process alive: only an admitted
// caller's newer speak-as tells the node the hub moved (z2go, B).
func TestNodeAgentEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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
	// One public URL for the life of the test; what answers behind it
	// is swapped at the redeploy below (fly: same hostname, new process).
	var front atomic.Pointer[http.ServeMux]
	front.Store(s.mux())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { front.Load().ServeHTTP(w, r) }))
	t.Cleanup(ts.Close)
	m.publicURL = ts.URL
	hctx, hcancel := context.WithCancel(ctx)
	go m.listen(hctx)
	go m.serveHTTPFacet(hctx, s.hubFacetMux())
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
			BindAddr: "127.0.0.1:0", Log: logger, BeatEvery: time.Hour, MinRebeat: 2 * time.Second,
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

	// irohup's shape (359.8.4): the same runtime as a caller-only
	// member — a Kit handed to it (a device's enrollment is wallet-
	// signed, not a boot token), no facets forwarded, beats for its
	// grants and the name map, and dials the node BY NAME with its
	// bundle on connect.
	devState := nodeagent.State{Dir: t.TempDir()}
	if _, _, err := devState.Key(); err != nil {
		t.Fatal(err)
	}
	devPriv, _, _ := devState.Key()
	devKit, err := m.issuer.Mint(cert.NewEdSigner(devPriv).ActorID(), "desk", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	if err := devState.SaveKit(devKit); err != nil {
		t.Fatal(err)
	}
	dev, err := nodeagent.Start(nodeagent.Options{
		Config: nodeagent.Config{Hub: cfg.Hub, Relay: public}, State: devState,
		BindAddr: "127.0.0.1:0", Log: log.New(testWriter{t}, "desk: ", 0), BeatEvery: time.Hour,
		DialTimeout: 3 * time.Second, MinRebeat: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev.Close() })
	if _, err := dev.Dial(ctx, "aa-bb-cc-dd-ee-ff", "apid"); !errors.Is(err, nodeagent.ErrNotBeaten) {
		t.Fatalf("dial before the first beat: %v", err)
	}
	go func() {
		if err := dev.Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("desk run: %v", err)
		}
	}()
	waitFor(t, ctx, "desk beat", func() bool { return dev.Beats() == 1 })
	// A second Send in the same process (renew + bundle in one beat is
	// the live case): the seq must count up from its clock-seeded base
	// and survive the wire (JCS numbers are doubles — 2^53 is the cap).
	if err := dev.Beat(ctx); err != nil {
		t.Fatalf("second beat: %v", err)
	}
	presented, err := dev.Present()
	if err != nil || len(presented.Grants) == 0 || presented.Member.Cav.Name != "desk" {
		t.Fatalf("present: %+v %v", presented, err)
	}
	if _, err := dev.Dial(ctx, "nobody", "apid"); !errors.Is(err, nodeagent.ErrUnknownName) {
		t.Fatalf("dial unknown name: %v", err)
	}
	conn, err = dev.Dial(ctx, "aa-bb-cc-dd-ee-ff", "apid")
	if err != nil {
		t.Fatalf("desk → apid by name: %v", err)
	}
	raw, err = conn.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Write([]byte("by name")); err != nil {
		t.Fatal(err)
	}
	_ = raw.CloseWrite()
	if got, err := io.ReadAll(raw); err != nil || string(got) != "by name" {
		t.Fatalf("echo by name: %q, %v", got, err)
	}
	_ = raw.Close()
	_ = conn.Close()

	// The hub over its own facet (359.8.2.4): "hub" resolves from the
	// hub record, not the name map; an admin's stream on hub-http is
	// one HTTP connection to /config, and the node — machines, no
	// hub-http grant — is refused at the preamble.
	if e, err := dev.Resolve(nodeagent.HubName); err != nil || len(e) != 1 || cert.ActorID(e[0].Member.Aud) != m.issuer.ID() {
		t.Fatalf("resolve hub: %+v %v", e, err)
	}
	conn, err = dev.Dial(ctx, nodeagent.HubName, "hub-http")
	if err != nil {
		t.Fatalf("desk → hub-http: %v", err)
	}
	raw, err = conn.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://hub.mesh.internal/config?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Close = true
	if err := req.Write(raw); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(raw), req)
	if err != nil {
		t.Fatalf("hub-http response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "v1alpha1") {
		t.Fatalf("/config over hub-http: %d %s", resp.StatusCode, body)
	}
	_ = raw.Close()
	_ = conn.Close()
	if _, err := a.Dial(ctx, nodeagent.HubName, "hub-http"); !errors.As(err, &refused) {
		t.Fatalf("node → hub-http: %v, want refused", err)
	}

	// Hub redeploy (ipt7, z2go): the old process is gone — its endpoint
	// with it — and the new one behind the same URL is SEALED until the
	// wallet acts, then holds a fresh hubkey. The node dials nothing
	// between beats; its evidence is the pooled connection its last beat
	// left, which the dead process takes with it. That beat meets the
	// sealed hub (503) and is retried flat at MinRebeat, not backed off,
	// so the unseal is one MinRebeat from the node's beat — and the hub's
	// name map, empty since the restart, has the node again.
	oldHub := m.issuer.ID()
	nodeBeats, nodeAttempts := a.Beats(), a.BeatAttempts()
	hcancel()
	if err := m.wan.Close(); err != nil {
		t.Fatal(err)
	}
	m2 := testHubManagerOn(t, []string{wellKnownAddr}, "", irohHubTransport(home, public, "127.0.0.1:0"))
	s2 := &server{root: m2.root, store: deviceflow.NewStore(), sessions: newSessionStore(), adminAddrs: []string{wellKnownAddr}, hub: m2}
	m2.publicURL = ts.URL
	go m2.listen(ctx)
	front.Store(s2.mux())
	waitFor(t, ctx, "node to beat on losing the hub", func() bool { return a.BeatAttempts() > nodeAttempts })
	if a.Beats() != nodeBeats {
		t.Fatalf("node beat a sealed hub: %d beats", a.Beats())
	}
	if e, _ := a.Resolve(nodeagent.HubName); len(e) != 1 || cert.ActorID(e[0].Member.Aud) != oldHub {
		t.Fatalf("node's hub record while the hub is sealed: %+v", e)
	}

	if err := m2.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.unsealIssuer(speakAsSig(t, m2, testKey(t))); err != nil {
		t.Fatal(err)
	}
	if m2.issuer.ID() == oldHub {
		t.Fatal("redeploy kept the hubkey; the scenario needs a rotation")
	}
	go m2.serveHTTPFacet(ctx, s2.hubFacetMux())
	waitFor(t, ctx, "new hub published", func() bool { return m2.issuer.Actor.CurrentLocation() != nil })
	if err := m2.wan.(*irohWan).Online(ctx); err != nil { // at the relay, as fly's loopback relay child is at once
		t.Fatal(err)
	}
	unsealed := time.Now()
	waitFor(t, ctx, "node beat at the unsealed hub", func() bool { return a.Beats() > nodeBeats })
	if since := time.Since(unsealed); since > 4*time.Second {
		t.Fatalf("node beat %s after the unseal; want within ~MinRebeat", since)
	}
	if k := a.Kit(); k.Member.Iss != m2.issuer.ID() {
		t.Fatalf("node's member cert not renewed at the new hubkey: issuer %s", k.Member.Iss)
	}
	if e := m2.issuer.NameMap(); len(issuer.Lookup(e, "aa-bb-cc-dd-ee-ff")) != 1 || issuer.Lookup(e, "aa-bb-cc-dd-ee-ff")[0].Location == nil {
		t.Fatalf("the redeployed hub does not know the node: %+v", e)
	}

	// The desk's record still names the dead key; its next dial to "hub"
	// must not wait for the 6 h beat: the unreachable dial re-beats
	// (well-known → new key, renew at it) and the retry lands on the
	// new hub.

	if e, _ := dev.Resolve(nodeagent.HubName); len(e) != 1 || cert.ActorID(e[0].Member.Aud) != oldHub {
		t.Fatalf("desk should still hold the dead hubkey before dialing: %+v", e)
	}
	beats := dev.Beats()
	dialStart := time.Now()
	conn, err = dev.Dial(ctx, nodeagent.HubName, "hub-http")
	if err != nil {
		t.Fatalf("desk → hub-http after redeploy: %v", err)
	}
	t.Logf("desk → hub after redeploy took %s", time.Since(dialStart))
	if conn.Peer() != m2.issuer.ID() {
		t.Fatalf("dialed %s, want the new hub %s", conn.Peer(), m2.issuer.ID())
	}
	_ = conn.Close()
	if dev.Beats() != beats+1 {
		t.Fatalf("beats: %d, want one rebeat on top of %d", dev.Beats(), beats)
	}
	if e, _ := dev.Resolve(nodeagent.HubName); len(e) != 1 || cert.ActorID(e[0].Member.Aud) != m2.issuer.ID() {
		t.Fatalf("hub record after the rebeat: %+v", e)
	}
	if k := dev.Kit(); k.Member.Iss != m2.issuer.ID() {
		t.Fatalf("member cert not renewed at the new hubkey: issuer %s", k.Member.Iss)
	}

	// Rolling redeploy (z2go, B): the new process takes the URL while
	// the old one lingers, so the node's pooled connection stays healthy
	// and nothing on the node side moves. The desk beats (well-known →
	// third hubkey) and dials the node's apid with grants under it: the
	// admitted bundle's speak-as is wallet-signed and newer than the one
	// the node holds — the node beats at the hub it just learned of.
	m3 := testHubManagerOn(t, []string{wellKnownAddr}, "", irohHubTransport(home, public, "127.0.0.1:0"))
	if err := m3.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := m3.unsealIssuer(speakAsSig(t, m3, testKey(t))); err != nil {
		t.Fatal(err)
	}
	s3 := &server{root: m3.root, store: deviceflow.NewStore(), sessions: newSessionStore(), adminAddrs: []string{wellKnownAddr}, hub: m3}
	m3.publicURL = ts.URL
	go m3.listen(ctx)
	front.Store(s3.mux())
	waitFor(t, ctx, "third hub published", func() bool { return m3.issuer.Actor.CurrentLocation() != nil })
	if err := m3.wan.(*irohWan).Online(ctx); err != nil {
		t.Fatal(err)
	}
	nodeBeats = a.Beats()
	if err := dev.Beat(ctx); err != nil {
		t.Fatalf("desk beat at the third hub: %v", err)
	}
	if k := dev.Kit(); k.Member.Iss != m3.issuer.ID() {
		t.Fatalf("desk not renewed at the third hubkey: issuer %s", k.Member.Iss)
	}
	if a.Beats() != nodeBeats {
		t.Fatalf("node beat with its hub connection intact: %d", a.Beats())
	}
	conn, err = dev.Dial(ctx, "aa-bb-cc-dd-ee-ff", "apid")
	if err != nil {
		t.Fatalf("desk → apid under the third hub: %v", err)
	}
	_ = conn.Close()
	waitFor(t, ctx, "node to beat on the caller's newer speak-as", func() bool { return a.Beats() > nodeBeats })
	if k := a.Kit(); k.Member.Iss != m3.issuer.ID() {
		t.Fatalf("node not renewed at the third hubkey: issuer %s", k.Member.Iss)
	}
	if e := m3.issuer.NameMap(); len(issuer.Lookup(e, "aa-bb-cc-dd-ee-ff")) != 1 {
		t.Fatalf("the third hub does not know the node: %+v", e)
	}

	// The rate limit holds at its default: for a member whose beat is
	// seconds old, a staleness signal is absorbed, not turned into
	// another beat.
	dev2State := nodeagent.State{Dir: t.TempDir()}
	dev2Priv, _, err := dev2State.Key()
	if err != nil {
		t.Fatal(err)
	}
	dev2Kit, err := m3.issuer.Mint(cert.NewEdSigner(dev2Priv).ActorID(), "desk2", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dev2State.SaveKit(dev2Kit); err != nil {
		t.Fatal(err)
	}
	dev2, err := nodeagent.Start(nodeagent.Options{
		Config: nodeagent.Config{Hub: cfg.Hub, Relay: public}, State: dev2State,
		BindAddr: "127.0.0.1:0", Log: log.New(testWriter{t}, "desk2: ", 0), BeatEvery: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev2.Close() })
	if err := dev2.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dev2.Dial(ctx, "nobody", "apid"); !errors.Is(err, nodeagent.ErrUnknownName) {
		t.Fatalf("dial unknown name: %v", err)
	}
	if dev2.Beats() != 1 {
		t.Fatalf("a beat seconds old must absorb the staleness signal: %d beats", dev2.Beats())
	}
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
