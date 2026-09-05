package cert

import (
	"crypto/ed25519"
	"testing"
)

// Tests for Result.Verified rootedness (review round 2, fix #1): only
// certs on a chain rooted at the receiver may feed the caller's
// clock.Mark. A stranger that can merely connect must not be able to
// push the mark forward (denial by strangers, excluded by ADR-0019).

func markFixture(names ...string) fixture {
	f := fixture{signer: map[string]Signer{}, id: map[string]ActorID{}}
	seed := byte(30)
	for _, n := range names {
		priv := ed25519.NewKeyFromSeed(bytesFill(seed))
		s := NewEdSigner(priv)
		f.signer[n] = s
		f.id[n] = s.ActorID()
		seed++
	}
	return f
}

func verifiedHas(res Result, want Cert) bool {
	wc := certKey(want)
	for _, c := range res.Verified {
		if certKey(c) == wc {
			return true
		}
	}
	return false
}

// TestVerifiedExcludesStranger: a stranger's validly self-signed member
// cert with a far-future iat verifies, but 2b fails; it must NOT appear
// in Verified, so ObserveAll cannot be poisoned by any peer that can
// connect.
func TestVerifiedExcludesStranger(t *testing.T) {
	f := markFixture("R", "OWNER1", "CALLER", "STRANGER")
	id := f.id
	consent := f.build(certSpec{iss: "R", aud: string(id["OWNER1"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, iat: 1, exp: 100})
	strangerMember := f.build(certSpec{iss: "STRANGER", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "evil"}, iat: 1_000_000, exp: 1_000_100})

	if err := Verify(strangerMember); err != nil {
		t.Fatal("stranger member should have a VALID self-signature")
	}
	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: strangerMember, Grants: nil},
	}
	res := Authorize(in)
	if res.OK {
		t.Fatal("stranger member accepted")
	}
	if verifiedHas(res, strangerMember) {
		t.Fatal("mark poisoning: stranger cert appears in Verified")
	}
	for _, c := range res.Verified {
		if c.Iss == id["STRANGER"] {
			t.Fatalf("mark poisoning: cert from stranger in Verified (iat=%d)", c.Iat)
		}
	}
	// The R-signed consent, however, IS rooted.
	if !verifiedHas(res, consent) {
		t.Fatal("R-signed consent missing from Verified")
	}
}

// TestVerifiedIncludesExpiredButRooted: an expired member cert from a
// consented issuer is rejected, but it still proves its iat passed, so it
// MUST appear in Verified.
func TestVerifiedIncludesExpiredButRooted(t *testing.T) {
	f := markFixture("R", "OWNER1", "CALLER")
	id := f.id
	consent := f.build(certSpec{iss: "R", aud: string(id["OWNER1"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, iat: 1, exp: 100})
	expiredMember := f.build(certSpec{iss: "OWNER1", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, iat: 50, exp: testNOW /* expired */})

	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: expiredMember, Grants: nil},
	}
	res := Authorize(in)
	if res.OK {
		t.Fatal("expired member should be rejected")
	}
	if !verifiedHas(res, expiredMember) {
		t.Fatal("expired-but-rooted member missing from Verified (mark must still advance)")
	}
}

// TestVerifiedExcludesStrangerSpeakAs: a speak-as signed by an
// unconsented hot key must not enter Verified even though its signature
// verifies.
func TestVerifiedExcludesStrangerSpeakAs(t *testing.T) {
	f := markFixture("WALLET", "HUBKEY_A", "HUBKEY_B", "R", "CALLER")
	id := f.id
	consent := f.build(certSpec{iss: "R", aud: string(id["WALLET"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, iat: 1, exp: 100})
	// speak-as signed by HUBKEY_B (unconsented), far-future iat.
	saStranger := f.build(certSpec{iss: "HUBKEY_B", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, iat: 9_000_000, exp: 9_000_100})
	member := f.build(certSpec{iss: "HUBKEY_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, iat: 1_000_000, exp: 1_000_100})

	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: nil, SpeakAs: []Cert{saStranger}},
	}
	res := Authorize(in)
	if res.OK {
		t.Fatal("member via unconsented-hot-key speak-as accepted")
	}
	if verifiedHas(res, saStranger) || verifiedHas(res, member) {
		t.Fatal("mark poisoning: stranger speak-as/member reached Verified")
	}
}
