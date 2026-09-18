package issuer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/envelope"
)

// testRecipe is a small recipe with one row of each shape the compiler
// distinguishes: group under node, host under node, group under gateway.
const testRecipe = `
node:
  inbound:
    - {facet: apid, group: admins}
    - {facet: kube-api, host: laptop}
gateway:
  inbound:
    - {facet: ingress-http, group: media}
hub:
  inbound: []
`

// filePolicy writes recipe and blocklist into a fresh talos/ dir and
// returns the FilePolicy over it — the production wiring, exercised.
func filePolicy(t *testing.T, recipe string, blocked ...cert.ActorID) PolicySource {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, policy.File), []byte(recipe), 0o600); err != nil {
		t.Fatal(err)
	}
	var bl string
	for _, id := range blocked {
		bl += string(id) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, policy.BlocklistFile), []byte("# test\n"+bl), 0o600); err != nil {
		t.Fatal(err)
	}
	return FilePolicy(dir)
}

// invocation builds what the inbox hands a handler after it has
// verified the caller's chain: the payload, the proof (for its
// speak-as) and the envelope signer.
func invocation(t *testing.T, from cert.ActorID, payload []byte, proof ...cert.Cert) *actor.Invocation {
	t.Helper()
	return &actor.Invocation{Envelope: &envelope.Envelope{Payload: payload, Proof: proof}, From: from}
}

func bundleReq(t *testing.T, member cert.Cert) []byte {
	t.Helper()
	p, err := EncodeBundleRequest(member)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// nodeReceiver is a receiver of kind node that consented to wallet w
// for its whole facet set — the accept side of a compiled grant.
func nodeReceiver(t *testing.T, w cert.ActorID, now int64) (cert.EdSigner, cert.Receiver) {
	t.Helper()
	r := newNode(t)
	consent, err := cert.Sign(cert.Cert{
		Aud: string(w),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{r.ActorID()}, Facet: policy.Facets(policy.KindNode), Delegable: true},
		Iat: now,
		Exp: now + 365*Day,
	}, r)
	if err != nil {
		t.Fatal(err)
	}
	return r, cert.Receiver{ID: r.ActorID(), Consents: []cert.Cert{consent}}
}

// ---- 1. the handler compiles for the verified member ----------------------

func TestBundleCompilesForTheMemberCert(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	iss.Policy = filePolicy(t, testRecipe)
	w.unseal(t, iss)
	N := newNode(t).ActorID()
	kit, err := iss.Mint(N, "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}

	now := clk.Now()
	body, err := iss.bundleHandler(context.Background(), invocation(t, N, bundleReq(t, kit.Member)))
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	b, err := DecodeBundle(body)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly Compile's output, signed by this hubkey, in recipe order.
	recipe, _, _ := iss.Policy()
	want := policy.Compile(recipe, policy.Caller{Key: N, Name: "laptop", Groups: []string{"admins"}}, now)
	if len(b.Grants) != len(want) || len(want) != 2 {
		t.Fatalf("got %d grants, want %d: %+v", len(b.Grants), len(want), b.Grants)
	}
	for i, g := range b.Grants {
		if g.Iss != iss.ID() || g.Aud != want[i].Aud || g.Can != cert.VerbInvoke ||
			!slices.Equal(g.Cav.Facet, want[i].Cav.Facet) || !cert.IsTargetAny(g.Cav.Target) ||
			g.Iat != now || g.Exp != now+policy.GrantTTL || cert.Verify(g) != nil {
			t.Fatalf("grant %d = %+v, want %+v signed by hub", i, g, want[i])
		}
	}
	if len(b.Blocklist) != 0 || b.SpeakAs.Iss != w.id || b.SpeakAs.Aud != string(iss.ID()) {
		t.Fatalf("blocklist %v speak-as %+v", b.Blocklist, b.SpeakAs)
	}

	// The grants admit N at a node that consented to the wallet — for
	// apid (by group) and kube-api (by host) — with the kit's speak-as
	// resolving the hubkey. A media member gets neither.
	_, recv := nodeReceiver(t, w.id, now)
	authz := func(m cert.Cert, grants []cert.Cert, facet string) bool {
		return cert.Authorize(cert.Input{
			Receiver:    recv,
			AcceptTable: policy.AcceptTable(policy.KindNode),
			Now:         now,
			ALPN:        policy.ALPN(facet),
			Peer:        cert.ActorID(m.Aud),
			Bundle:      cert.Bundle{Member: m, Grants: grants, SpeakAs: []cert.Cert{kit.SpeakAs}},
		}).OK
	}
	for _, f := range policy.Facets(policy.KindNode) {
		if !authz(kit.Member, b.Grants, f) {
			t.Fatalf("laptop's bundle does not admit at node %s", f)
		}
	}
	M := newNode(t).ActorID()
	tv, err := iss.Mint(M, "tv", []string{"media"})
	if err != nil {
		t.Fatal(err)
	}
	body, err = iss.bundleHandler(context.Background(), invocation(t, M, bundleReq(t, tv.Member)))
	if err != nil {
		t.Fatal(err)
	}
	tb, err := DecodeBundle(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Grants) != 1 || tb.Grants[0].Cav.Facet[0] != "ingress-http" {
		t.Fatalf("tv grants: %+v", tb.Grants)
	}
	for _, f := range policy.Facets(policy.KindNode) {
		if authz(tv.Member, tb.Grants, f) {
			t.Fatalf("tv's bundle admits at node %s", f)
		}
	}
	// Nor do laptop's grants admit tv (aud group:admins ∌ tv's groups;
	// the host grant names laptop's key).
	if authz(tv.Member, b.Grants, "apid") || authz(tv.Member, b.Grants, "kube-api") {
		t.Fatal("laptop's grants admitted tv")
	}
}

// ---- 2. refusals -------------------------------------------------------------

func TestBundleRefusals(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	N := newNode(t).ActorID()
	ctx := context.Background()

	// Sealed: refused before anything is read.
	iss.Policy = filePolicy(t, testRecipe)
	if _, err := iss.bundleHandler(ctx, invocation(t, N, nil)); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed: %v", err)
	}
	w.unseal(t, iss)
	kit, err := iss.Mint(N, "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}

	// No policy source: a refusal, never an empty bundle.
	iss.Policy = nil
	if _, err := iss.bundleHandler(ctx, invocation(t, N, bundleReq(t, kit.Member))); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("no policy: %v", err)
	}
	// A broken checkout fails closed.
	iss.Policy = filePolicy(t, "node: {inbound: [{facet: ssh, group: admins}]}\n")
	if _, err := iss.bundleHandler(ctx, invocation(t, N, bundleReq(t, kit.Member))); err == nil || errors.Is(err, ErrMember) {
		t.Fatalf("bad recipe: %v", err)
	}
	iss.Policy = filePolicy(t, testRecipe)

	// The member cert must name the caller: another node presenting
	// laptop's cert is refused; so is a non-member cert, an expired one,
	// and one this hub's wallet never vouched for.
	X := newNode(t).ActorID()
	if _, err := iss.bundleHandler(ctx, invocation(t, X, bundleReq(t, kit.Member))); !errors.Is(err, ErrMember) {
		t.Fatalf("aud mismatch: %v", err)
	}
	if _, err := iss.bundleHandler(ctx, invocation(t, N, bundleReq(t, kit.BeatGrant))); !errors.Is(err, ErrMember) {
		t.Fatalf("grant as member: %v", err)
	}
	if _, err := iss.bundleHandler(ctx, invocation(t, N, []byte(`{"member":`))); err == nil {
		t.Fatal("malformed payload accepted")
	}
	clk.Advance(MemberTTL)
	if _, err := iss.bundleHandler(ctx, invocation(t, N, bundleReq(t, kit.Member))); !errors.Is(err, ErrMember) {
		t.Fatalf("expired member: %v", err)
	}
	clk.Advance(-MemberTTL)

	// A hub another wallet unsealed minted N a cert; at this hub, even
	// with that wallet's speak-as in the proof, it is a stranger's.
	w2 := newWallet(t)
	other := newIssuer(t, clk, nil)
	w2.unseal(t, other)
	foreign, err := other.Mint(N, "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.bundleHandler(ctx, invocation(t, N, bundleReq(t, foreign.Member), foreign.SpeakAs)); !errors.Is(err, ErrMember) {
		t.Fatalf("foreign member: %v", err)
	}

	// Blocklisted: #bundle and #renew both refuse; the list still goes
	// out to everyone else.
	iss.Policy = filePolicy(t, testRecipe, N)
	if _, err := iss.bundleHandler(ctx, invocation(t, N, bundleReq(t, kit.Member))); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked bundle: %v", err)
	}
	renewReq, err := actor.EncodeRenewRequest([]cert.Cert{kit.Member}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Actor.AcceptTable[actor.FacetRenew](ctx, invocation(t, N, renewReq)); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked renew: %v", err)
	}
	M := newNode(t).ActorID()
	tv, err := iss.Mint(M, "tv", []string{"media"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := iss.bundleHandler(ctx, invocation(t, M, bundleReq(t, tv.Member)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeBundle(body)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.Blocklist, []cert.ActorID{N}) {
		t.Fatalf("blocklist handed out: %v", b.Blocklist)
	}
}

// ---- 3. wire round trip --------------------------------------------------------

func TestBundleWire(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	w.unseal(t, iss)
	empty := Bundle{SpeakAs: *iss.SpeakAs()}
	raw, err := EncodeBundle(empty)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Grants) != 0 || len(got.Blocklist) != 0 || got.SpeakAs.Sig == nil {
		t.Fatalf("round trip: %+v", got)
	}
	// A tampered grant does not decode.
	N := newNode(t).ActorID()
	kit, _ := iss.Mint(N, "laptop", []string{"admins"})
	g := kit.BeatGrant
	g.Cav.Facet = []string{"apid"}
	if raw, err = EncodeBundle(Bundle{Grants: []cert.Cert{g}, SpeakAs: kit.SpeakAs}); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBundle(raw); err == nil {
		t.Fatal("tampered grant decoded")
	}
}

// ---- 4. end to end: the beat across an Issuer rotation ------------------------

// TestBundleAcrossIssuerRotation is TestRenewAcrossIssuerRotation for
// #bundle: N holds a kit from dead process A; process B (same wallet)
// compiles N's grants from A's member cert without N renewing first,
// because A's speak-as in N's proof resolves A to the wallet B speaks
// for. Over the wire, so the beat grant's #bundle facet is what admits.
func TestBundleAcrossIssuerRotation(t *testing.T) {
	clk := newClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	net := actor.NewMemoryNetwork()
	w := newWallet(t)

	a := newIssuer(t, clk, nil)
	w.unseal(t, a)
	node := newNode(t)
	N := node.ActorID()
	kit, err := a.Mint(N, "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}

	clk.Advance(Day)
	_, privB, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	B := cert.NewEdSigner(privB).ActorID()
	epB, err := net.Bind(B, "hubB")
	if err != nil {
		t.Fatal(err)
	}
	b := NewWithKey(privB, testGroups, epB, clk.Now)
	b.Policy = filePolicy(t, testRecipe)
	w.unseal(t, b)
	listen := func(ac *actor.Actor) {
		done := make(chan error, 1)
		go func() { done <- ac.Listen(ctx) }()
		t.Cleanup(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("Listen: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Listen did not stop")
			}
		})
	}
	listen(b.Actor)

	epN, err := net.Bind(N, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	n := actor.New(node, epN)
	n.Clock = clk.Now
	n.Grant(B, FacetBundle, kit.BeatGrant, kit.SpeakAs)
	listen(n)

	now := clk.Now()
	rep, err := n.Send(ctx, B, FacetBundle, bundleReq(t, kit.Member))
	if err != nil {
		t.Fatalf("bundle at B: %v", err)
	}
	got, err := DecodeBundle(rep.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Grants) != 2 || got.SpeakAs.Aud != string(B) {
		t.Fatalf("bundle: %+v", got)
	}
	for _, g := range got.Grants {
		if g.Iss != B || g.Iat != now {
			t.Fatalf("grant not signed by B at now: %+v", g)
		}
	}
	// A's member cert + B's grants + BOTH speak-as certs admit at a node:
	// the receiver resolves member.iss (A) and grant.iss (B) to the one
	// wallet it consented to.
	_, recv := nodeReceiver(t, w.id, now)
	res := cert.Authorize(cert.Input{
		Receiver:    recv,
		AcceptTable: policy.AcceptTable(policy.KindNode),
		Now:         now,
		ALPN:        policy.ALPN("apid"),
		Peer:        N,
		Bundle:      cert.Bundle{Member: kit.Member, Grants: got.Grants, SpeakAs: []cert.Cert{kit.SpeakAs, got.SpeakAs}},
	})
	if !res.OK || res.Identity.Name != "laptop" {
		t.Fatalf("mixed-generation bundle does not admit: %+v", res)
	}

	// A hub another wallet unsealed refuses the beat at the chain.
	w2 := newWallet(t)
	_, privC, _ := ed25519.GenerateKey(rand.Reader)
	C := cert.NewEdSigner(privC).ActorID()
	epC, err := net.Bind(C, "hubC")
	if err != nil {
		t.Fatal(err)
	}
	c := NewWithKey(privC, testGroups, epC, clk.Now)
	c.Policy = b.Policy
	w2.unseal(t, c)
	listen(c.Actor)
	n.Grant(C, FacetBundle, kit.BeatGrant, kit.SpeakAs)
	var rerr *actor.RemoteError
	if _, err := n.Send(ctx, C, FacetBundle, bundleReq(t, kit.Member)); !errors.As(err, &rerr) || rerr.Code != actor.StatusUnauthorized {
		t.Fatalf("hub unsealed by another wallet served A's member: %v", err)
	}
}
