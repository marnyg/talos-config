package issuer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"pgregory.net/rapid"
)

// ---- fixtures ---------------------------------------------------------------

// tb is the slice of testing.TB both *testing.T and *rapid.T satisfy.
type tb interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

// fakeClock is a settable Unix-seconds clock shared by every party in a
// test, so expiry boundaries are exact.
type fakeClock struct{ now atomic.Int64 }

func newClock() *fakeClock {
	c := &fakeClock{}
	c.now.Store(1_700_000_000)
	return c
}
func (c *fakeClock) Now() int64      { return c.now.Load() }
func (c *fakeClock) Advance(d int64) { c.now.Add(d) }
func (c *fakeClock) Set(t int64)     { c.now.Store(t) }

// wallet is a test owner: a secp256k1 key the hub never sees.
type wallet struct {
	s  cert.EthSigner
	id cert.ActorID
}

func newWallet(t tb) wallet {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	s := cert.NewEthSigner(priv)
	return wallet{s: s, id: s.ActorID()}
}

// signHex is what the owner's wallet does with the unseal page: EIP-191
// personal_sign over the proposal's canonical JSON, rendered 0x-hex.
func (w wallet) signHex(t tb, msg string) string {
	t.Helper()
	sig, err := w.s.Sign([]byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	return "0x" + hex.EncodeToString(sig)
}

// unseal runs the whole owner-side flow against iss for wallet w.
func (w wallet) unseal(t tb, iss *Issuer) {
	t.Helper()
	_, msg, err := iss.Proposal(w.id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := iss.Unseal(w.signHex(t, msg), []cert.ActorID{w.id})
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	if got != w.id {
		t.Fatalf("unseal attributed to %s, want %s", got, w.id)
	}
}

var testGroups = []string{"machines", "admins", "media"}

func newIssuer(t tb, clk *fakeClock, tr actor.Transport) *Issuer {
	t.Helper()
	iss, err := New(testGroups, tr, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

// newNode is a member-to-be: its own Ed25519 key, never derived from
// anything the hub holds (ADR-0015).
func newNode(t tb) cert.EdSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return cert.NewEdSigner(priv)
}

// authorizeAt runs the protocol's connect check with the hub itself as
// the receiver and the kit as the presented bundle — the receiver-side
// view of "does what Mint handed out actually admit N".
func authorizeAt(hub *Issuer, kit Kit, node cert.ActorID, now int64) cert.Result {
	return cert.Authorize(cert.Input{
		Receiver: cert.Receiver{
			ID:       hub.ID(),
			Consents: hub.Actor.Consents,
			SpeakAs:  hub.Actor.SpeakAs,
		},
		AcceptTable: map[string]string{"renew": actor.FacetRenew},
		Now:         now,
		ALPN:        "renew",
		Peer:        node,
		Bundle: cert.Bundle{
			Member:  kit.Member,
			Grants:  []cert.Cert{kit.RenewGrant},
			SpeakAs: []cert.Cert{kit.SpeakAs},
		},
	})
}

// ---- 1. sealed ---------------------------------------------------------------

func TestSealedHoldsNoAuthority(t *testing.T) {
	clk := newClock()
	iss := newIssuer(t, clk, nil)
	node := newNode(t)

	if err := iss.Serving(); !errors.Is(err, ErrSealed) {
		t.Fatalf("Serving on fresh issuer: %v", err)
	}
	if _, err := iss.Mint(node.ActorID(), "n", []string{"machines"}); !errors.Is(err, ErrSealed) {
		t.Fatalf("Mint while sealed: %v", err)
	}
	if iss.SpeakAs() != nil || iss.Wallet() != "" || iss.Runway() != 0 {
		t.Fatal("sealed issuer reports held authority")
	}
	if len(iss.Actor.Consents) != 0 || len(iss.Actor.SpeakAs) != 0 {
		t.Fatal("sealed issuer's actor carries consents/speak-as")
	}
	// The #renew facet is guarded before the protocol handler runs.
	if _, err := iss.Actor.AcceptTable[actor.FacetRenew](context.Background(), &actor.Invocation{}); !errors.Is(err, ErrSealed) {
		t.Fatalf("#renew while sealed: %v", err)
	}
	if sch, _ := iss.ID().Scheme(); sch != "ed:" || iss.ID().Validate() != nil {
		t.Fatalf("hubkey id %q is not a valid ed: id", iss.ID())
	}
	if fp := iss.Fingerprint(); len(fp) != 16 || !strings.HasPrefix(strings.TrimPrefix(string(iss.ID()), "ed:"), fp) {
		t.Fatalf("fingerprint %q", fp)
	}
}

func TestTwoProcessesTwoKeys(t *testing.T) {
	clk := newClock()
	a, b := newIssuer(t, clk, nil), newIssuer(t, clk, nil)
	if a.ID() == b.ID() {
		t.Fatal("two issuers share a hubkey")
	}
}

// ---- 2. proposal --------------------------------------------------------------

func genWallet() *rapid.Generator[cert.ActorID] {
	return rapid.Custom(func(t *rapid.T) cert.ActorID {
		return cert.ActorID("eth:0x" + hex.EncodeToString(rapid.SliceOfN(rapid.Byte(), 20, 20).Draw(t, "addr")))
	})
}

func genGroups() *rapid.Generator[[]string] {
	return rapid.SliceOfNDistinct(rapid.StringMatching(`[a-z][a-z0-9-]{0,11}`), 0, 6, rapid.ID[string])
}

func TestProposalShapeAndStability(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		clk := newClock()
		groups := genGroups().Draw(rt, "groups")
		w := genWallet().Draw(rt, "wallet")
		iss, err := New(groups, nil, clk.Now)
		if err != nil {
			rt.Fatal(err)
		}
		iat := clk.Now()
		c, msg, err := iss.Proposal(w)
		if err != nil {
			rt.Fatal(err)
		}
		// Shape per ADR-0018.
		if c.Iss != w || c.Aud != string(iss.ID()) || c.Can != cert.VerbSpeakAs {
			rt.Fatalf("proposal names the wrong parties: %+v", c)
		}
		if !slices.Equal(c.Cav.Verbs, []string{"member", "invoke"}) || c.Cav.Delegable {
			rt.Fatalf("proposal caveats: %+v", c.Cav)
		}
		want := slices.Sorted(slices.Values(groups))
		if !slices.Equal(c.Cav.Groups, want) {
			rt.Fatalf("proposal groups %v, want sorted %v", c.Cav.Groups, want)
		}
		if c.Iat != iat || c.Exp != iat+SpeakAsTTL {
			rt.Fatalf("proposal time: iat %d exp %d, want %d/%d", c.Iat, c.Exp, iat, iat+SpeakAsTTL)
		}
		if c.Sig != nil {
			rt.Fatal("proposal is signed")
		}
		canon, err := cert.CanonicalBytes(c)
		if err != nil || string(canon) != msg {
			rt.Fatalf("message is not the proposal's canonical JSON")
		}
		// Stability: the clock moves, the page does not.
		clk.Advance(rapid.Int64Range(1, day).Draw(rt, "later"))
		c2, msg2, err := iss.Proposal(w)
		if err != nil {
			rt.Fatal(err)
		}
		if msg2 != msg || c2.Iat != c.Iat {
			rt.Fatal("proposal changed between calls while sealed")
		}
		// Another wallet gets its own proposal naming itself.
		w2 := genWallet().Draw(rt, "wallet2")
		if w2 != w {
			c3, _, _ := iss.Proposal(w2)
			if c3.Iss != w2 || c3.Aud != c.Aud {
				rt.Fatalf("second wallet's proposal: %+v", c3)
			}
		}
	})
}

func TestProposalRejectsBadWallet(t *testing.T) {
	iss := newIssuer(t, newClock(), nil)
	for _, bad := range []cert.ActorID{"", "eth:0xABC", "ed:00", cert.ActorID("eth:0x" + strings.Repeat("G", 40))} {
		if _, _, err := iss.Proposal(bad); err == nil {
			t.Errorf("Proposal(%q) accepted", bad)
		}
	}
	if WalletID("  0xAbCdEf0123456789abcdef0123456789ABCDEF01 ") != "eth:0xabcdef0123456789abcdef0123456789abcdef01" {
		t.Fatal("WalletID does not normalise")
	}
}

// ---- 3. unseal ------------------------------------------------------------------

func TestUnsealAcceptsOnlyAnAllowlistedWalletOverThisProcess(t *testing.T) {
	clk := newClock()
	w, other := newWallet(t), newWallet(t)
	a, b := newIssuer(t, clk, nil), newIssuer(t, clk, nil)

	_, msgA, _ := a.Proposal(w.id)
	sigA := w.signHex(t, msgA)

	// Not on the allowlist: the proposal is never even offered.
	if _, err := a.Unseal(sigA, []cert.ActorID{other.id}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("non-allowlisted wallet: %v", err)
	}
	if a.Serving() == nil {
		t.Fatal("issuer unsealed by a non-allowlisted wallet")
	}
	// Phish-replay (ADR-0018): a signature over process A's proposal is
	// useless against process B — the aud is the hubkey.
	if _, err := b.Unseal(sigA, []cert.ActorID{w.id}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("replayed A's unseal at B: %v", err)
	}
	// Malformed and tampered signatures.
	for _, bad := range []string{"", "0x", "zz", "0x" + strings.Repeat("00", 64)} {
		if _, err := a.Unseal(bad, []cert.ActorID{w.id}); err == nil {
			t.Errorf("Unseal(%q) accepted", bad)
		}
	}
	raw, _ := hex.DecodeString(strings.TrimPrefix(sigA, "0x"))
	raw[7] ^= 0x01
	if _, err := a.Unseal("0x"+hex.EncodeToString(raw), []cert.ActorID{w.id}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("tampered signature: %v", err)
	}
	// The allowlist may be wide; the signature selects the wallet.
	got, err := a.Unseal(sigA, []cert.ActorID{other.id, w.id})
	if err != nil || got != w.id {
		t.Fatalf("unseal: %v (%s)", err, got)
	}
	if err := a.Serving(); err != nil {
		t.Fatalf("Serving after unseal: %v", err)
	}
	sa := a.SpeakAs()
	if sa == nil || sa.Iss != w.id || sa.Aud != string(a.ID()) || cert.Verify(*sa) != nil {
		t.Fatalf("held speak-as: %+v", sa)
	}
	if a.Wallet() != w.id || a.Runway() != SpeakAsTTL {
		t.Fatalf("wallet %s runway %d", a.Wallet(), a.Runway())
	}
	// hold() wired the actor: it answers for the wallet (rule 4) and
	// consents to the wallet for #renew with target wallet (ADR-0024 F).
	if len(a.Actor.SpeakAs) != 1 || a.Actor.SpeakAs[0].Iss != w.id {
		t.Fatalf("actor speak-as: %+v", a.Actor.SpeakAs)
	}
	if len(a.Actor.Consents) != 1 {
		t.Fatalf("actor consents: %+v", a.Actor.Consents)
	}
	c := a.Actor.Consents[0]
	if c.Iss != a.ID() || c.Aud != string(w.id) || c.Can != cert.VerbInvoke ||
		!slices.Equal(c.Cav.Target, []cert.ActorID{w.id}) || !slices.Equal(c.Cav.Facet, []string{actor.FacetRenew}) ||
		!c.Cav.Delegable || c.Exp != sa.Exp || cert.Verify(c) != nil {
		t.Fatalf("consent: %+v", c)
	}
}

func TestUnsealRefusesAnExpiredProposal(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	_, msg, _ := iss.Proposal(w.id)
	sig := w.signHex(t, msg)
	clk.Advance(SpeakAsTTL) // exp <= now
	if _, err := iss.Unseal(sig, []cert.ActorID{w.id}); err == nil || errors.Is(err, ErrNotAllowed) {
		t.Fatalf("expired proposal: %v", err)
	}
	if iss.Serving() == nil {
		t.Fatal("unsealed with an expired speak-as")
	}
}

// ---- 4. mint -----------------------------------------------------------------------

func TestMintProperties(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		clk := newClock()
		w := newWallet(rt)
		iss := newIssuer(rt, clk, nil)
		w.unseal(rt, iss)
		clk.Advance(rapid.Int64Range(0, NagBefore).Draw(rt, "later")) // anywhere before the nag window

		node := newNode(rt)
		N := node.ActorID()
		groups := rapid.SliceOfDistinct(rapid.SampledFrom(testGroups), rapid.ID[string]).Draw(rt, "groups")
		name := rapid.StringMatching(`[a-z][a-z0-9]{0,15}`).Draw(rt, "name")
		now := clk.Now()

		kit, err := iss.Mint(N, name, groups)
		if err != nil {
			rt.Fatalf("mint: %v", err)
		}
		m := kit.Member
		if m.Iss != iss.ID() || m.Aud != string(N) || m.Can != cert.VerbMember || m.Cav.Delegable {
			rt.Fatalf("member: %+v", m)
		}
		if m.Cav.Name != name || !slices.Equal(m.Cav.Groups, slices.Sorted(slices.Values(groups))) {
			rt.Fatalf("member caveats: %+v", m.Cav)
		}
		if m.Iat != now || m.Exp != now+MemberTTL || cert.Verify(m) != nil {
			rt.Fatalf("member time/sig: %+v", m)
		}
		g := kit.RenewGrant
		if g.Iss != iss.ID() || g.Aud != string(N) || g.Can != cert.VerbInvoke || g.Cav.Delegable {
			rt.Fatalf("grant: %+v", g)
		}
		if !slices.Equal(g.Cav.Target, []cert.ActorID{w.id}) || !slices.Equal(g.Cav.Facet, []string{actor.FacetRenew}) {
			rt.Fatalf("grant names %v/%v, want target wallet, facet #renew", g.Cav.Target, g.Cav.Facet)
		}
		if g.Iat != now || g.Exp != now+GrantTTL || cert.Verify(g) != nil {
			rt.Fatalf("grant time/sig: %+v", g)
		}
		if held := iss.SpeakAs(); kit.SpeakAs.Iss != held.Iss || !slices.Equal(kit.SpeakAs.Sig, held.Sig) {
			rt.Fatal("kit carries a speak-as other than the held one")
		}
		// The kit admits N at the hub for #renew, with identity from the
		// member cert only; and no sooner than its own runway allows.
		if res := authorizeAt(iss, kit, N, now); !res.OK || res.Identity.Name != name || res.Identity.Key != N ||
			!slices.Equal(res.Identity.Groups, m.Cav.Groups) {
			rt.Fatalf("kit does not authorize at the hub: %+v", res)
		}
		if res := authorizeAt(iss, kit, N, g.Exp); res.OK {
			rt.Fatal("kit authorizes at the grant's exp")
		}
		if res := authorizeAt(iss, kit, N, g.Exp-1); !res.OK {
			rt.Fatal("kit rejected one second before the grant's exp")
		}
		// Groups outside the speak-as caveat are refused.
		if _, err := iss.Mint(N, name, append(slices.Clone(groups), "not-a-group")); !errors.Is(err, ErrGroup) {
			rt.Fatalf("out-of-caveat group: %v", err)
		}
	})
}

func TestMintRejectsBadInputs(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	w.unseal(t, iss)
	N := newNode(t).ActorID()

	// A member is an iroh EndpointId (ed:), never a wallet (ADR-0015).
	for _, bad := range []cert.ActorID{w.id, "ed:00", "", "group:machines", "*"} {
		if _, err := iss.Mint(bad, "n", nil); !errors.Is(err, ErrNodeID) {
			t.Errorf("Mint(%q): %v", bad, err)
		}
	}
	if _, err := iss.Mint(N, "", nil); err == nil {
		t.Error("nameless member minted")
	}
	if _, err := iss.Mint(N, "n", nil); err != nil {
		t.Errorf("group-less member refused: %v", err)
	}
}

func TestMintedKitFailsClosed(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	w.unseal(t, iss)
	node := newNode(t)
	N := node.ActorID()
	kit, err := iss.Mint(N, "n", []string{"machines"})
	if err != nil {
		t.Fatal(err)
	}
	now := clk.Now()
	if !authorizeAt(iss, kit, N, now).OK {
		t.Fatal("baseline kit rejected")
	}

	// A tampered member signature.
	bad := kit
	bad.Member.Sig = slices.Clone(kit.Member.Sig)
	bad.Member.Sig[3] ^= 0x80
	if authorizeAt(iss, bad, N, now).OK {
		t.Fatal("tampered member accepted")
	}
	// A member cert presented by another key.
	if authorizeAt(iss, kit, newNode(t).ActorID(), now).OK {
		t.Fatal("kit accepted from a peer the member cert does not name")
	}
	// Without the speak-as the hubkey resolves to nobody.
	noSA := kit
	noSA.SpeakAs = cert.Cert{}
	if authorizeAt(iss, noSA, N, now).OK {
		t.Fatal("kit accepted without the speak-as")
	}
	// At the member's exp (the grant renewed in between, say).
	if authorizeAt(iss, kit, N, kit.Member.Exp).OK {
		t.Fatal("kit accepted at member exp")
	}
}

// ---- 5. rotation: the kit survives a redeploy (ADR-0018 xfx) -------------------

func TestKitAuthorizesAtAnyIssuerTheSameWalletUnsealed(t *testing.T) {
	clk := newClock()
	w, w2 := newWallet(t), newWallet(t)
	a := newIssuer(t, clk, nil)
	w.unseal(t, a)
	N := newNode(t).ActorID()
	kit, err := a.Mint(N, "n", []string{"machines"})
	if err != nil {
		t.Fatal(err)
	}

	// Process A dies; B is a fresh key unsealed by the same wallet. What A
	// signed admits at B: B answers for W (rule 4) and the kit's
	// speak-as resolves A → W.
	clk.Advance(day)
	b := newIssuer(t, clk, nil)
	w.unseal(t, b)
	res := authorizeAt(b, kit, N, clk.Now())
	if !res.OK || res.Identity.Name != "n" {
		t.Fatalf("A's kit rejected at B under the same wallet: %+v", res)
	}
	// A different wallet's process: A's kit is a stranger's.
	c := newIssuer(t, clk, nil)
	w2.unseal(t, c)
	if authorizeAt(c, kit, N, clk.Now()).OK {
		t.Fatal("A's kit accepted at a hub another wallet unsealed")
	}
	// Once W's speak-as to A is out of runway, nothing A signed resolves.
	if authorizeAt(b, kit, N, kit.SpeakAs.Exp).OK {
		t.Fatal("A's kit accepted at the speak-as exp")
	}
}

// ---- 6. nag window ------------------------------------------------------------------

func TestNagWindowSealsAndReUnsealIsFresh(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	w.unseal(t, iss)
	N := newNode(t).ActorID()
	exp := iss.SpeakAs().Exp

	// Last second outside the window still serves.
	clk.Set(exp - NagBefore - 1)
	if err := iss.Serving(); err != nil {
		t.Fatalf("Serving at runway == NagBefore+1: %v", err)
	}
	// Runway == NagBefore serves (r < NagBefore nags); one more second nags.
	clk.Set(exp - NagBefore)
	if err := iss.Serving(); err != nil {
		t.Fatalf("Serving at runway == NagBefore: %v", err)
	}
	clk.Advance(1)
	if err := iss.Serving(); !errors.Is(err, ErrNag) {
		t.Fatalf("Serving inside the nag window: %v", err)
	}
	if _, err := iss.Mint(N, "n", nil); !errors.Is(err, ErrNag) {
		t.Fatalf("Mint inside the nag window: %v", err)
	}
	if _, err := iss.Actor.AcceptTable[actor.FacetRenew](context.Background(), &actor.Invocation{}); !errors.Is(err, ErrNag) {
		t.Fatalf("#renew inside the nag window: %v", err)
	}
	// The nag IS a seal for signing, but the speak-as is still held and
	// the wallet still named (the status page shows who to nag).
	if iss.Wallet() != w.id || iss.Runway() != NagBefore-1 {
		t.Fatalf("wallet %s runway %d", iss.Wallet(), iss.Runway())
	}

	// Re-unseal: the proposal is FRESH (not the memo from the first
	// unseal), so the process gets a full 120 d again.
	c, _, err := iss.Proposal(w.id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Iat != clk.Now() || c.Exp != clk.Now()+SpeakAsTTL {
		t.Fatalf("re-unseal offered a stale proposal: iat %d (now %d)", c.Iat, clk.Now())
	}
	w.unseal(t, iss)
	if err := iss.Serving(); err != nil {
		t.Fatalf("Serving after re-unseal: %v", err)
	}
	if iss.Runway() != SpeakAsTTL {
		t.Fatalf("runway after re-unseal %d, want %d", iss.Runway(), SpeakAsTTL)
	}
	if _, err := iss.Mint(N, "n", nil); err != nil {
		t.Fatalf("Mint after re-unseal: %v", err)
	}

	// Past exp the issuer is plainly sealed.
	clk.Set(iss.SpeakAs().Exp)
	if err := iss.Serving(); !errors.Is(err, ErrSealed) {
		t.Fatalf("Serving at exp: %v", err)
	}
}

// ---- 7. end to end: #renew across an Issuer rotation over MemoryNetwork --------

// TestRenewAcrossIssuerRotation is actor.TestRenewAcrossHotKeyRotation
// with the real Issuer in place of hand-built certs: Issuer A, unsealed
// by wallet W, mints node N a kit. A dies. Issuer B — fresh key, no
// memory of A — is unsealed by W and listens. N's beat at B, carrying
// only what Mint gave it, gets both certs re-signed by B.
func TestRenewAcrossIssuerRotation(t *testing.T) {
	clk := newClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	net := actor.NewMemoryNetwork()
	w := newWallet(t)

	// Process A: never on the network; only its signatures survive.
	a := newIssuer(t, clk, nil)
	w.unseal(t, a)
	node := newNode(t)
	N := node.ActorID()
	kit, err := a.Mint(N, "n", []string{"machines", "media"})
	if err != nil {
		t.Fatal(err)
	}

	// Process B: fresh hubkey, bound to the network, unsealed BEFORE it
	// listens (Issuer's contract), same wallet.
	clk.Advance(day)
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
	if b.ID() != B {
		t.Fatal("NewWithKey id mismatch")
	}
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

	// Node N: an ordinary actor holding exactly the kit.
	epN, err := net.Bind(N, "n")
	if err != nil {
		t.Fatal(err)
	}
	n := actor.New(node, epN)
	n.Clock = clk.Now
	n.Grant(B, actor.FacetRenew, kit.RenewGrant, kit.SpeakAs)
	listen(n)

	payload, err := actor.EncodeRenewRequest([]cert.Cert{kit.Member, kit.RenewGrant}, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := clk.Now()
	rep, err := n.Send(ctx, B, actor.FacetRenew, payload)
	if err != nil {
		t.Fatalf("renew at B: %v", err)
	}
	fresh, errs, err := actor.DecodeRenewResponse(rep.Payload)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range errs {
		if e != nil {
			t.Fatalf("item %d refused: %v", i, e)
		}
	}
	m, g := fresh[0], fresh[1]
	if m.Iss != B || m.Aud != string(N) || m.Can != cert.VerbMember || m.Cav.Name != "n" ||
		!slices.Equal(m.Cav.Groups, []string{"machines", "media"}) || m.Iat != now || m.Exp != now+MemberTTL {
		t.Fatalf("member not re-issued by B: %+v", m)
	}
	if g.Iss != B || g.Can != cert.VerbInvoke || !slices.Equal(g.Cav.Target, []cert.ActorID{w.id}) ||
		g.Iat != now || g.Exp != now+GrantTTL {
		t.Fatalf("grant not re-issued by B: %+v", g)
	}
	// The renewed pair, with B's own speak-as, is a kit that admits at B.
	kitB := Kit{Member: m, RenewGrant: g, SpeakAs: *b.SpeakAs()}
	if res := authorizeAt(b, kitB, N, now); !res.OK {
		t.Fatalf("renewed kit does not authorize at B: %+v", res)
	}

	// A process another wallet unsealed refuses the same beat at the chain.
	w2 := newWallet(t)
	_, privC, _ := ed25519.GenerateKey(rand.Reader)
	C := cert.NewEdSigner(privC).ActorID()
	epC, err := net.Bind(C, "hubC")
	if err != nil {
		t.Fatal(err)
	}
	c := NewWithKey(privC, testGroups, epC, clk.Now)
	w2.unseal(t, c)
	listen(c.Actor)
	n.Grant(C, actor.FacetRenew, kit.RenewGrant, kit.SpeakAs)
	if _, err := n.Send(ctx, C, actor.FacetRenew, payload); err == nil {
		t.Fatal("hub unsealed by another wallet renewed A's kit")
	}
}
