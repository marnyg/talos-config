package cert_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
)

// TestRelayAdmissionShapes: spike talos-config-qx5c. Relay admission
// for sovereign actors needs no new verb, caveat or verifier rule: a
// relay R is a receiver, admission is VerifyChain with verb `relay`,
// and the three callers it must tell apart are three chain shapes the
// verifier already folds.
//
//	member    R's own consent names P        → empty chain
//	child     P delegates to C's key         → [P→C]
//	newborn   key unknown until it is born   → [P→"*" + postage], the
//	          birth consent's shape (protocol/spawn) on the relay verb
//
// Nothing here runs a relay; the mechanics are TestRelayTokenGate in
// iroh-transport.
func TestRelayAdmissionShapes(t *testing.T) {
	const facet = "#relay"
	const now = int64(1_000)
	key := func() cert.EdSigner {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return cert.NewEdSigner(priv)
	}
	sign := func(s cert.EdSigner, aud string, cav cert.Caveats, exp int64) cert.Cert {
		c, err := cert.Sign(cert.Cert{Aud: aud, Can: cert.VerbRelay, Cav: cav, Iat: now - 1, Exp: exp}, s)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	relay, parent, child, newborn, stranger := key(), key(), key(), key(), key()
	at := []cert.ActorID{relay.ActorID()}

	// The relay's one consent: its member P, delegable — held by the
	// relay, never presented.
	consent := sign(relay, string(parent.ActorID()), cert.Caveats{Target: at, Facet: []string{facet}, Delegable: true}, now+86_400)
	r := cert.Receiver{ID: relay.ActorID(), Consents: []cert.Cert{consent}}
	admit := func(r cert.Receiver, chain []cert.Cert, who cert.ActorID, when int64) (cert.Cert, error) {
		eff, _, err := cert.VerifyChain(r, cert.VerbRelay, chain, nil, who, facet, when)
		return eff, err
	}

	// member: nothing to present.
	if _, err := admit(r, nil, parent.ActorID(), now); err != nil {
		t.Fatalf("member, empty chain: %v", err)
	}
	if _, err := admit(r, nil, stranger.ActorID(), now); err == nil {
		t.Fatal("stranger, empty chain: admitted")
	}

	// child: one link, bound to its key, bounded by the parent's exp.
	toChild := sign(parent, string(child.ActorID()), cert.Caveats{Target: at, Facet: []string{facet}}, now+600)
	eff, err := admit(r, []cert.Cert{toChild}, child.ActorID(), now)
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if eff.Exp != now+600 {
		t.Fatalf("child eff.Exp = %d, want the link's %d (the connection's deadline)", eff.Exp, now+600)
	}
	if _, err := admit(r, []cert.Cert{toChild}, stranger.ActorID(), now); err == nil {
		t.Fatal("child's chain presented by another key: admitted")
	}
	if _, err := admit(r, []cert.Cert{toChild}, child.ActorID(), now+601); err == nil {
		t.Fatal("child after exp: admitted")
	}
	// Revocation of a member's whole subtree is the relay dropping one
	// consent it holds itself — no list, no wait for expiry.
	if _, err := admit(cert.Receiver{ID: relay.ActorID()}, []cert.Cert{toChild}, child.ActorID(), now); err == nil {
		t.Fatal("child after the relay dropped its parent: admitted")
	}

	// newborn: the parent cannot name a key that does not exist yet, so
	// the intro carries a short wildcard link — legal only with postage.
	req := postage.Require(16)
	birth := sign(parent, "*", cert.Caveats{Target: at, Facet: []string{facet}, Postage: req}, now+120)
	eff, err = admit(r, []cert.Cert{birth}, newborn.ActorID(), now)
	if err != nil {
		t.Fatalf("newborn: %v", err)
	}
	if eff.Cav.Postage != req {
		t.Fatalf("newborn eff postage = %q, want %q", eff.Cav.Postage, req)
	}
	// The stamp binds the bearer link to one key at one relay.
	pre := []byte(string(relay.ActorID()) + "|" + string(newborn.ActorID()))
	stamp, err := postage.Default.Solve(context.Background(), eff.Cav.Postage, pre)
	if err != nil {
		t.Fatal(err)
	}
	if err := postage.Default.Check(eff.Cav.Postage, pre, stamp); err != nil {
		t.Fatalf("newborn stamp: %v", err)
	}
	other := []byte(string(relay.ActorID()) + "|" + string(stranger.ActorID()))
	if err := postage.Default.Check(eff.Cav.Postage, other, stamp); err == nil {
		t.Skip("16-bit stamp collided for another key (1 in 65536); rerun")
	}
	if _, err := admit(r, []cert.Cert{birth}, newborn.ActorID(), now+121); err == nil {
		t.Fatal("newborn after the birth window: admitted")
	}
	free := sign(parent, "*", cert.Caveats{Target: at, Facet: []string{facet}}, now+120)
	if _, err := admit(r, []cert.Cert{free}, newborn.ActorID(), now); err == nil {
		t.Fatal("wildcard link without postage: admitted")
	}
	// A child cannot pass its admission on unless its parent said so.
	onward := sign(child, string(stranger.ActorID()), cert.Caveats{Target: at, Facet: []string{facet}}, now+600)
	if _, err := admit(r, []cert.Cert{toChild, onward}, stranger.ActorID(), now); !errors.Is(err, cert.ErrNotDelegable) {
		t.Fatalf("link under a non-delegable child cert: err = %v, want ErrNotDelegable", err)
	}
}
