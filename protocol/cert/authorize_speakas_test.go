package cert

import (
	"crypto/ed25519"
	"testing"
)

// These tests cover the ADR-0018 speak-as link that authorize.qnt does
// not yet model (handoff loose thread: czi/jp2). They are stated
// explicitly per the brief.

func speakAsFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{signer: map[string]Signer{}, id: map[string]ActorID{}}
	seed := byte(10)
	for _, name := range []string{"WALLET", "HUBKEY_A", "HUBKEY_B", "R", "CALLER"} {
		priv := ed25519.NewKeyFromSeed(bytesFill(seed))
		s := NewEdSigner(priv)
		f.signer[name] = s
		f.id[name] = s.ActorID()
		seed++
	}
	return f
}

// TestMixedEpochAccept: member cert issued by HUBKEY_A, grant issued by
// HUBKEY_B, both under valid speak-as certs from the SAME wallet ⇒
// Accept. Resolution maps both hot keys to the wallet; the group rule
// compares the resolved issuers (ADR-0018 "resolve before compare").
func TestMixedEpochAccept(t *testing.T) {
	f := speakAsFixture(t)
	id := f.id

	saA := f.build(certSpec{iss: "WALLET", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, exp: 100})
	saB := f.build(certSpec{iss: "WALLET", aud: string(id["HUBKEY_B"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, exp: 100})

	consent := f.build(certSpec{iss: "R", aud: string(id["WALLET"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, exp: 100})

	member := f.build(certSpec{iss: "HUBKEY_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 100})
	grant := f.build(certSpec{iss: "HUBKEY_B", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 100})

	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{saA, saB}},
	}
	res := Authorize(in)
	if !res.OK {
		t.Fatal("mixed-epoch bundle rejected; expected Accept")
	}
	if res.Identity.Name != "laptop" {
		t.Fatalf("identity name = %q", res.Identity.Name)
	}
}

// TestExpiredSpeakAsRejects: the member cert's own exp is far in the
// future, but its speak-as has expired ⇒ Reject. Effective expiry is
// min(own, speak-as) (ADR-0018 "verification-time validity").
func TestExpiredSpeakAsRejects(t *testing.T) {
	f := speakAsFixture(t)
	id := f.id

	saExpired := f.build(certSpec{iss: "WALLET", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, exp: testNOW})

	consent := f.build(certSpec{iss: "R", aud: string(id["WALLET"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, exp: 100})

	member := f.build(certSpec{iss: "HUBKEY_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 1_000_000})
	grant := f.build(certSpec{iss: "HUBKEY_A", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 1_000_000})

	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{saExpired}},
	}
	if res := Authorize(in); res.OK {
		t.Fatal("expired speak-as accepted; member.exp is far but speak-as expired")
	}
}

// TestReUnsealExpiredSpeakAsBeforeValid: after re-unseal without a
// redeploy, the bundle can carry a stale speak-as beside a fresh valid
// one for the SAME hot key. resolve must consider all matches and use
// the good (best-exp) one, so an honest caller is accepted regardless of
// ordering (review round 2, fix #2).
func TestReUnsealExpiredSpeakAsBeforeValid(t *testing.T) {
	for _, order := range []string{"expired-first", "valid-first"} {
		f := speakAsFixture(t)
		id := f.id
		saExpired := f.build(certSpec{iss: "WALLET", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
			cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, exp: testNOW})
		saValid := f.build(certSpec{iss: "WALLET", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
			cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, exp: 100})
		consent := f.build(certSpec{iss: "R", aud: string(id["WALLET"]), can: VerbInvoke,
			cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, exp: 100})
		member := f.build(certSpec{iss: "HUBKEY_A", aud: string(id["CALLER"]), can: VerbMember,
			cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 100})
		grant := f.build(certSpec{iss: "HUBKEY_A", aud: "group:admins", can: VerbInvoke,
			cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 100})
		speakAs := []Cert{saExpired, saValid}
		if order == "valid-first" {
			speakAs = []Cert{saValid, saExpired}
		}
		in := Input{
			Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
			Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
			ALPN: "mesh/apid/v1", Peer: id["CALLER"],
			Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: speakAs},
		}
		if res := Authorize(in); !res.OK {
			t.Fatalf("%s: honest caller rejected despite a valid speak-as present", order)
		}
	}
}

// TestSpeakAsVerbMissingRejects: the speak-as does not authorize the
// member verb (cav.verbs lacks "member") ⇒ Reject.
func TestSpeakAsVerbMissingRejects(t *testing.T) {
	f := speakAsFixture(t)
	id := f.id
	saInvokeOnly := f.build(certSpec{iss: "WALLET", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"invoke"}, Groups: []string{"admins"}}, exp: 100})
	consent := f.build(certSpec{iss: "R", aud: string(id["WALLET"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, exp: 100})
	member := f.build(certSpec{iss: "HUBKEY_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 100})
	grant := f.build(certSpec{iss: "HUBKEY_A", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 100})
	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{saInvokeOnly}},
	}
	if res := Authorize(in); res.OK {
		t.Fatal("accepted despite speak-as not authorizing the member verb")
	}
}

// TestSpeakAsGroupNotSubsetRejects: the speak-as's cav.groups does not
// cover the member cert's groups ⇒ Reject (cav.groups ⊇ member groups).
func TestSpeakAsGroupNotSubsetRejects(t *testing.T) {
	f := speakAsFixture(t)
	id := f.id
	saNarrow := f.build(certSpec{iss: "WALLET", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"media"}}, exp: 100})
	consent := f.build(certSpec{iss: "R", aud: string(id["WALLET"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, exp: 100})
	member := f.build(certSpec{iss: "HUBKEY_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 100})
	grant := f.build(certSpec{iss: "HUBKEY_A", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 100})
	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{saNarrow}},
	}
	if res := Authorize(in); res.OK {
		t.Fatal("accepted despite speak-as groups not covering member groups")
	}
}

// TestSpeakAsUnconsentedWalletRejects: the speak-as resolves the hub key
// to a wallet R never consented to ⇒ Reject (step 2b on the resolved
// issuer).
func TestSpeakAsUnconsentedWalletRejects(t *testing.T) {
	f := speakAsFixture(t)
	id := f.id
	// speak-as from HUBKEY_B (acting as a stranger "wallet") — R holds no
	// consent for HUBKEY_B.
	saStranger := f.build(certSpec{iss: "HUBKEY_B", aud: string(id["HUBKEY_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, exp: 100})
	consent := f.build(certSpec{iss: "R", aud: string(id["WALLET"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, exp: 100})
	member := f.build(certSpec{iss: "HUBKEY_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 100})
	grant := f.build(certSpec{iss: "HUBKEY_A", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 100})
	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{saStranger}},
	}
	if res := Authorize(in); res.OK {
		t.Fatal("accepted despite resolved issuer being an unconsented wallet")
	}
}

// TestAttenuateFieldwise and delegable gate.
func TestAttenuate(t *testing.T) {
	parent := Cert{
		Cav: Caveats{Target: []ActorID{"ed:a", "ed:b"}, Facet: []string{"apid", "kube-api"},
			Groups: []string{"admins", "media"}, Delegable: true},
		Exp: 100,
	}
	child := Cert{
		Cav: Caveats{Target: []ActorID{"ed:b", "ed:c"}, Facet: []string{"apid"},
			Groups: []string{"media"}, Delegable: true},
		Exp: 50,
	}
	eff, err := Attenuate(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(eff.Cav.Target, "ed:b") || containsID(eff.Cav.Target, "ed:a") || containsID(eff.Cav.Target, "ed:c") {
		t.Fatalf("target intersection wrong: %v", eff.Cav.Target)
	}
	if len(eff.Cav.Facet) != 1 || eff.Cav.Facet[0] != "apid" {
		t.Fatalf("facet intersection wrong: %v", eff.Cav.Facet)
	}
	if eff.Exp != 50 {
		t.Fatalf("effective exp = %d, want 50", eff.Exp)
	}
	// delegable:false parent ⇒ no link may follow.
	parent.Cav.Delegable = false
	if _, err := Attenuate(parent, child); err == nil {
		t.Fatal("attenuation followed a non-delegable parent")
	}
}
