package enroll

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

	"github.com/marnyg/talos-config/config-server/enrollmsg"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

const t0 int64 = 1_700_000_000

var testGroups = []string{"admins", "media"}

type fakeClock struct{ now atomic.Int64 }

func newClock() *fakeClock      { c := &fakeClock{}; c.now.Store(t0); return c }
func (c *fakeClock) Now() int64 { return c.now.Load() }

type wallet struct {
	s  cert.EthSigner
	id cert.ActorID
}

func newWallet(t *testing.T) wallet {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	s := cert.NewEthSigner(priv)
	return wallet{s: s, id: s.ActorID()}
}

func (w wallet) signHex(t *testing.T, msg string) string {
	t.Helper()
	sig, err := w.s.Sign([]byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	return "0x" + hex.EncodeToString(sig)
}

func (w wallet) unseal(t *testing.T, iss *issuer.Issuer) {
	t.Helper()
	_, msg, err := iss.Proposal(w.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Unseal(w.signHex(t, msg), []cert.ActorID{w.id}); err != nil {
		t.Fatalf("unseal: %v", err)
	}
}

// hub is a listening Issuer + an admitted Enroll on one MemoryNetwork.
type hub struct {
	ctx    context.Context
	clk    *fakeClock
	net    *actor.MemoryNetwork
	issuer *issuer.Issuer
	enroll *Enroll
}

func newHub(t *testing.T) *hub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clk := newClock()
	net := actor.NewMemoryNetwork()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := cert.NewEdSigner(priv).ActorID()
	ep, err := net.Bind(id, "hub")
	if err != nil {
		t.Fatal(err)
	}
	iss := issuer.NewWithKey(priv, testGroups, ep, clk.Now)
	// Listen BEFORE unseal: the Issuer refuses until held (its contract
	// since actor.Hold).
	done := make(chan error, 1)
	go func() { done <- iss.Listen(ctx) }()
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
	e, err := New(net, iss.ID(), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := iss.Admit(e.ID()); err != nil {
		t.Fatal(err)
	}
	return &hub{ctx: ctx, clk: clk, net: net, issuer: iss, enroll: e}
}

// approved is a wallet-approved v2 enrollment for a fresh node.
func approved(t *testing.T, w wallet, group string) (issuer.MintDeviceRequest, cert.ActorID) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	node := cert.NewEdSigner(priv).ActorID()
	req := issuer.MintDeviceRequest{
		Node:        node,
		Name:        "tv",
		Group:       group,
		Fingerprint: strings.Repeat("ab", 32),
		Nonce:       "n0nce",
	}
	req.Signature = w.signHex(t, enrollmsg.V2(req.Name, req.Group, req.Fingerprint, string(req.Node), req.Nonce))
	return req, node
}

// admits runs the protocol's connect check with the Issuer as receiver
// and the kit as the presented bundle.
func admits(h *hub, kit issuer.Kit, node cert.ActorID) bool {
	consents, speakAs := h.issuer.Actor.Authority()
	return cert.Authorize(cert.Input{
		Receiver: cert.Receiver{
			ID:       h.issuer.ID(),
			Consents: consents,
			SpeakAs:  speakAs,
		},
		AcceptTable: map[string]string{"renew": actor.FacetRenew},
		Now:         h.clk.Now(),
		ALPN:        "renew",
		Peer:        node,
		Bundle: cert.Bundle{
			Member:  kit.Member,
			Grants:  []cert.Cert{kit.BeatGrant},
			SpeakAs: []cert.Cert{kit.SpeakAs},
		},
	}).OK
}

func TestMintDeviceHappyPath(t *testing.T) {
	h := newHub(t)
	w := newWallet(t)
	w.unseal(t, h.issuer)
	req, node := approved(t, w, "media")

	kit, err := h.enroll.MintDevice(h.ctx, req)
	if err != nil {
		t.Fatalf("mint-device: %v", err)
	}
	m := kit.Member
	if m.Iss != h.issuer.ID() || m.Aud != string(node) || m.Can != cert.VerbMember ||
		m.Cav.Name != "tv" || !slices.Equal(m.Cav.Groups, []string{"media"}) ||
		m.Exp != h.clk.Now()+issuer.MemberTTL {
		t.Fatalf("member: %+v", m)
	}
	if !slices.Equal(kit.BeatGrant.Cav.Target, []cert.ActorID{w.id}) {
		t.Fatalf("renew grant names %v, want the wallet", kit.BeatGrant.Cav.Target)
	}
	if kit.SpeakAs.Iss != w.id || kit.SpeakAs.Aud != string(h.issuer.ID()) {
		t.Fatalf("speak-as: %+v", kit.SpeakAs)
	}
	if !admits(h, kit, node) {
		t.Fatal("kit does not admit the node at the hub")
	}
	// Wire round trip is lossless.
	b, err := issuer.EncodeKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	back, err := issuer.DecodeKit(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.Member.Aud != kit.Member.Aud || !slices.Equal(back.Member.Sig, kit.Member.Sig) {
		t.Fatal("kit did not survive the wire")
	}
}

func TestMintDeviceRefusals(t *testing.T) {
	h := newHub(t)
	w := newWallet(t)

	// Sealed: the caller is authorized by nobody (no consents held).
	req, _ := approved(t, w, "media")
	if _, err := h.enroll.MintDevice(h.ctx, req); err == nil {
		t.Fatal("sealed issuer minted")
	}

	w.unseal(t, h.issuer)

	// Another wallet's approval.
	other := newWallet(t)
	req2, _ := approved(t, other, "media")
	_, err := h.enroll.MintDevice(h.ctx, req2)
	if err == nil || !strings.Contains(err.Error(), "not signed by the wallet") {
		t.Fatalf("other wallet's approval: %v", err)
	}

	// Enroll (or an attacker holding its key) swaps the node after the
	// wallet signed: the signature no longer covers the request.
	req3, _ := approved(t, w, "media")
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	req3.Node = cert.NewEdSigner(priv).ActorID()
	if _, err := h.enroll.MintDevice(h.ctx, req3); err == nil || !strings.Contains(err.Error(), "not signed by the wallet") {
		t.Fatalf("swapped node: %v", err)
	}

	// A group the speak-as does not delegate.
	req4, _ := approved(t, w, "machines")
	if _, err := h.enroll.MintDevice(h.ctx, req4); err == nil || !strings.Contains(err.Error(), "group") {
		t.Fatalf("undelegated group: %v", err)
	}

	// A sibling the Issuer never admitted.
	stranger, err := New(h.net, h.issuer.ID(), h.clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	req5, _ := approved(t, w, "media")
	_, err = stranger.MintDevice(h.ctx, req5)
	var re *actor.RemoteError
	if !errors.As(err, &re) || re.Code != actor.StatusUnauthorized {
		t.Fatalf("unadmitted sibling: %v", err)
	}
}

// TestAdmitSurvivesReUnseal: a second unseal (the nag-window path)
// re-holds the authority set on the running actor; Enroll's consent is
// re-signed with it.
func TestAdmitSurvivesReUnseal(t *testing.T) {
	h := newHub(t)
	w := newWallet(t)
	w.unseal(t, h.issuer)
	w.unseal(t, h.issuer)
	req, node := approved(t, w, "admins")
	kit, err := h.enroll.MintDevice(h.ctx, req)
	if err != nil {
		t.Fatalf("after re-unseal: %v", err)
	}
	if !admits(h, kit, node) {
		t.Fatal("kit does not admit after re-unseal")
	}
}
