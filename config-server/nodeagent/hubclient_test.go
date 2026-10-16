package nodeagent

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/cert"
)

// pinWorld is the receiver side of a node after enrollment, reduced to
// what the pin touches: the node R, its Owner's identity (OLD, the
// epoch-0 root), the root the Owner rotates to (NEW), a hub key the
// Owner speaks through, and a caller the hub mints a member cert for.
type pinWorld struct {
	r, old, next, hub, caller cert.Signer
	consent                   cert.Cert
	now                       int64
}

func seedSigner(b byte) cert.Signer {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = b
	}
	return cert.NewEdSigner(ed25519.NewKeyFromSeed(seed))
}

func mustSign(t *testing.T, c cert.Cert, s cert.Signer) cert.Cert {
	t.Helper()
	out, err := cert.Sign(c, s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newPinWorld(t *testing.T) pinWorld {
	t.Helper()
	w := pinWorld{r: seedSigner(1), old: seedSigner(2), next: seedSigner(3), hub: seedSigner(4), caller: seedSigner(5), now: 1000}
	// What installAuthority signs: the node's consent to the Owner's
	// identity (kit.SpeakAs.Iss), target the node, delegable.
	w.consent = mustSign(t, cert.Cert{Aud: string(w.old.ActorID()), Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{w.r.ActorID()}, Facet: []string{"apid"}, Delegable: true},
		Iat: w.now, Exp: w.now + 86400}, w.r)
	return w
}

// bundleFor is what a caller presents on connect, with the hub key
// vouched for by root (the Owner's current root key, as the hub's
// speak-as has it).
func (w pinWorld) bundleFor(t *testing.T, root cert.Signer) cert.Bundle {
	t.Helper()
	sa := mustSign(t, cert.Cert{Aud: string(w.hub.ActorID()), Can: cert.VerbSpeakAs,
		Cav: cert.Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, Iat: w.now, Exp: w.now + 3600}, root)
	member := mustSign(t, cert.Cert{Aud: string(w.caller.ActorID()), Can: cert.VerbMember,
		Cav: cert.Caveats{Name: "laptop", Groups: []string{"admins"}}, Iat: w.now, Exp: w.now + 3600}, w.hub)
	grant := mustSign(t, cert.Cert{Aud: string(w.caller.ActorID()), Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{w.r.ActorID()}, Facet: []string{"apid"}}, Iat: w.now, Exp: w.now + 3600}, w.hub)
	return cert.Bundle{Member: member, Grants: []cert.Cert{grant}, SpeakAs: []cert.Cert{sa}}
}

// authorize is Agent.Authorize's decision with the given pins, without
// an iroh endpoint.
func (w pinWorld) authorize(b cert.Bundle, pins []*cert.Pin) bool {
	return cert.Authorize(cert.Input{
		Receiver:    cert.Receiver{ID: w.r.ActorID(), Consents: []cert.Cert{w.consent}, Pins: pins},
		AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Now:         w.now,
		ALPN:        "mesh/apid/v1",
		Peer:        w.caller.ActorID(),
		Bundle:      b,
	}).OK
}

// rotation is the Owner's epoch-1 rotate-root OLD → NEW, signed by the
// epoch-0 guardian (OLD itself under the trivial policy).
func (w pinWorld) rotation(t *testing.T, next cert.Policy) []cert.Cert {
	t.Helper()
	p0 := trivialPin(w.old.ActorID()).Policy0
	return []cert.Cert{mustSign(t, cert.Cert{Aud: string(w.old.ActorID()), Can: cert.VerbRotateRoot,
		Cav: cert.Caveats{Epoch: 1, Prev: p0.Hash(), Policy: &next}, Iat: w.now, Exp: w.now + 3600}, w.old)}
}

// TestFoldRotationsFromBundle: a #bundle carrying the Owner's rotation
// is re-verified against the node's own pin; afterwards a speak-as
// under the OLD root resolves to nothing the node consented to and the
// NEW root resolves to the Owner's identity — the consent never moved.
// With no rotations folded (today's bundle) the verdicts are today's.
func TestFoldRotationsFromBundle(t *testing.T) {
	w := newPinWorld(t)
	old, next := w.old.ActorID(), w.next.ActorID()
	trivial := trivialPin(old)
	byOld, byNew := w.bundleFor(t, w.old), w.bundleFor(t, w.next)

	// today: trivial pin == no pin
	if !w.authorize(byOld, nil) || !w.authorize(byOld, []*cert.Pin{trivial}) {
		t.Fatal("old root rejected before any rotation")
	}
	if w.authorize(byNew, nil) || w.authorize(byNew, []*cert.Pin{trivial}) {
		t.Fatal("stranger key accepted before any rotation")
	}

	// The bundle carries the rotation; round-trip it through the wire
	// shape the hub sends.
	p1 := cert.Policy{Root: next, Guardians: []cert.ActorID{next}, K: 1}
	hubSA := mustSign(t, cert.Cert{Aud: string(w.hub.ActorID()), Can: cert.VerbSpeakAs,
		Cav: cert.Caveats{Verbs: []string{"member"}}, Iat: w.now, Exp: w.now + 3600}, w.next)
	raw, err := issuer.EncodeBundle(issuer.Bundle{SpeakAs: hubSA, Rotations: [][]cert.Cert{w.rotation(t, p1)}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := issuer.DecodeBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Rotations) != 1 || len(b.Rotations[0]) != 1 {
		t.Fatalf("rotations round-trip: %v", b.Rotations)
	}

	var reported []error
	folded := foldRotations(trivial, b.Rotations, w.now, func(err error) { reported = append(reported, err) })
	if len(reported) != 0 {
		t.Fatalf("fold reported %v", reported)
	}
	if folded == trivial || len(trivial.Held()) != 0 {
		t.Fatal("fold mutated the installed pin instead of returning a new one")
	}
	if v := folded.View(); v.Epoch != 1 || v.Policy.Root != next || v.Dead {
		t.Fatalf("view after fold = %+v", v)
	}

	// after: the old root is dead, the new root IS the Owner
	if w.authorize(byOld, []*cert.Pin{folded}) {
		t.Fatal("speak-as under the old root accepted after the rotation")
	}
	if !w.authorize(byNew, []*cert.Pin{folded}) {
		t.Fatal("speak-as under the new root rejected after the rotation")
	}

	// Re-folding the same history (next beat, same bundle) is a no-op;
	// an unknown prev is reported and skipped, not held.
	again := foldRotations(folded, b.Rotations, w.now, func(err error) { reported = append(reported, err) })
	if len(reported) != 0 || len(again.Held()) != 1 {
		t.Fatalf("re-fold: %v, held %d", reported, len(again.Held()))
	}
	bogus := w.rotation(t, p1)
	bogus[0].Cav.Prev = p1.Hash() // prev names a policy this pin cannot root at epoch 1
	bogus[0] = mustSign(t, bogus[0], w.old)
	foldRotations(trivial, [][]cert.Cert{bogus}, w.now, func(err error) { reported = append(reported, err) })
	if len(reported) != 1 {
		t.Fatalf("unknown prev: reported %v", reported)
	}

	// Equivocation in the bundle kills the identity at this node: both
	// keys are strangers.
	p1b := cert.Policy{Root: next, Guardians: []cert.ActorID{next, old}, K: 1}
	reported = nil
	dead := foldRotations(trivial, [][]cert.Cert{w.rotation(t, p1), w.rotation(t, p1b)}, w.now, func(err error) { reported = append(reported, err) })
	if len(reported) != 1 || !errors.Is(reported[0], cert.ErrEquivocation) || !dead.View().Dead {
		t.Fatalf("equivocation: reported %v, dead %v", reported, dead.View().Dead)
	}
	if w.authorize(byOld, []*cert.Pin{dead}) || w.authorize(byNew, []*cert.Pin{dead}) {
		t.Fatal("dead pin admitted a key")
	}
}
