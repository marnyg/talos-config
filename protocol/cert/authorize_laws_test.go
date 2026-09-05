package cert

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"pgregory.net/rapid"
)

// This file ports verification/quint/authorize.qnt 1:1 — every inv* in
// its invAll, over REAL Ed25519 signatures instead of the model's
// `forged` flag. The generator mirrors the model's genNear: a correct
// bundle with at most two injected faults, because pure-random bundles
// reach Accept with probability ~2^-15 and every law would hold
// vacuously (the model documents this).
//
// NOW and the closed world match authorize.qnt.

const testNOW = int64(5)

// principals — the model's OWNER1, OWNER2, R, OTHER_R, CALLER, ROGUE,
// plus a distinct FORGER key used to produce invalid signatures.
type fixture struct {
	signer map[string]Signer
	id     map[string]ActorID
}

func newFixture(t *rapid.T) fixture {
	f := fixture{signer: map[string]Signer{}, id: map[string]ActorID{}}
	for _, name := range []string{"OWNER1", "OWNER2", "R", "OTHER_R", "CALLER", "ROGUE", "FORGER"} {
		_, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		s := NewEdSigner(priv)
		f.signer[name] = s
		f.id[name] = s.ActorID()
	}
	return f
}

// certSpec is an unsigned cert plus how to sign it.
type certSpec struct {
	iss     string // principal name claimed as issuer
	aud     string // literal aud (actor id or "group:x")
	can     Verb
	cav     Caveats
	exp     int64
	forged  bool // sign with FORGER but claim iss ⇒ signature fails
	unknown bool // set Cav.Unknown after signing (verifier judgment)
}

func (f fixture) build(s certSpec) Cert {
	signer := f.signer[s.iss]
	if s.forged {
		signer = f.signer["FORGER"]
	}
	c := Cert{Aud: s.aud, Can: s.can, Cav: s.cav, Iat: 1, Exp: s.exp}
	out, err := Sign(c, signer)
	if err != nil {
		panic(err)
	}
	out.Iss = f.id[s.iss] // claim the intended issuer regardless of who signed
	out.Cav.Unknown = out.Cav.Unknown || s.unknown
	return out
}

// fault enumerates every fault in authorize.qnt's genNear.
type fault int

const (
	fNone fault = iota
	fAlpnBogus
	fMemberForged
	fMemberExpired
	fMemberVerb
	fMemberAudRogue
	fMemberUnknown
	fMemberIssuerRogue
	fMemberNoGroups
	fBlocklisted
	fConsentSignedByOtherR
	fConsentNotDelegable
	fConsentForged
	fConsentExpired
	fConsentFacetKubeOnly
	fConsentTargetOtherR
	fNoConsentOwner2
	fGrantForged
	fGrantExpired
	fGrantVerbOther
	fGrantUnknown
	fGrantIssuerOwner2
	fGrantAudRogueKey
	fGrantAudMedia
	fGrantTargetOtherR
	fGrantFacetKubeOnly
	numFaults
)

type scenario struct {
	in     Input
	att    Input // same, but with the grant attenuated one link
	member Cert
	grant  Cert
	consents []Cert
	alpn   string
	facet  string
	f      map[fault]bool
}

func has(f map[fault]bool, x fault) bool { return f[x] }

// genNear builds a near-valid scenario with up to two faults — the Go
// analogue of authorize.qnt's genNear action.
func genNear(t *rapid.T, f fixture) scenario {
	f1 := fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f1"))
	f2 := fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f2"))
	audKind := rapid.IntRange(0, 1).Draw(t, "audKind")
	attKind := rapid.IntRange(0, 3).Draw(t, "att")

	flt := map[fault]bool{f1: true, f2: true}

	id := f.id
	facets := []string{"apid", "kube-api"}

	// consent1 (R → OWNER1) with its faults.
	consent1Iss := "R"
	if has(flt, fConsentSignedByOtherR) {
		consent1Iss = "OTHER_R"
	}
	consent1Target := []ActorID{id["R"]}
	if has(flt, fConsentTargetOtherR) {
		consent1Target = []ActorID{id["OTHER_R"]}
	}
	consent1Facet := facets
	if has(flt, fConsentFacetKubeOnly) {
		consent1Facet = []string{"kube-api"}
	}
	consent1Exp := int64(10)
	if has(flt, fConsentExpired) {
		consent1Exp = testNOW
	}
	consent1 := f.build(certSpec{
		iss: consent1Iss, aud: string(id["OWNER1"]), can: VerbInvoke,
		cav: Caveats{Target: consent1Target, Facet: consent1Facet, Delegable: !has(flt, fConsentNotDelegable)},
		exp: consent1Exp, forged: has(flt, fConsentForged),
	})
	consent2 := f.build(certSpec{
		iss: "R", aud: string(id["OWNER2"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: facets, Delegable: true},
		exp: 10,
	})
	consents := []Cert{consent1, consent2}
	if has(flt, fNoConsentOwner2) {
		consents = []Cert{consent1}
	}

	// member cert with its faults.
	memberIss := "OWNER1"
	if has(flt, fMemberIssuerRogue) {
		memberIss = "ROGUE"
	}
	memberAud := id["CALLER"]
	if has(flt, fMemberAudRogue) {
		memberAud = id["ROGUE"]
	}
	memberCan := VerbMember
	if has(flt, fMemberVerb) {
		memberCan = VerbInvoke
	}
	memberGroups := []string{"admins"}
	if has(flt, fMemberNoGroups) {
		memberGroups = nil
	}
	memberExp := int64(10)
	if has(flt, fMemberExpired) {
		memberExp = testNOW
	}
	member := f.build(certSpec{
		iss: memberIss, aud: string(memberAud), can: memberCan,
		cav:     Caveats{Groups: memberGroups, Name: "laptop"},
		exp:     memberExp,
		forged:  has(flt, fMemberForged),
		unknown: has(flt, fMemberUnknown),
	})

	// grant with its faults.
	grantIss := "OWNER1"
	if has(flt, fGrantIssuerOwner2) {
		grantIss = "OWNER2"
	}
	var grantAud string
	switch {
	case has(flt, fGrantAudRogueKey):
		grantAud = string(id["ROGUE"])
	case has(flt, fGrantAudMedia):
		grantAud = "group:media"
	case audKind == 0:
		grantAud = string(id["CALLER"])
	default:
		grantAud = "group:admins"
	}
	grantCan := VerbInvoke
	if has(flt, fGrantVerbOther) {
		grantCan = VerbRelay
	}
	grantTarget := []ActorID{id["R"]}
	if has(flt, fGrantTargetOtherR) {
		grantTarget = []ActorID{id["OTHER_R"]}
	}
	grantFacet := []string{"apid"}
	if has(flt, fGrantFacetKubeOnly) {
		grantFacet = []string{"kube-api"}
	}
	grantExp := int64(10)
	if has(flt, fGrantExpired) {
		grantExp = testNOW
	}
	grantSpec := certSpec{
		iss: grantIss, aud: grantAud, can: grantCan,
		cav:     Caveats{Target: grantTarget, Facet: grantFacet},
		exp:     grantExp,
		forged:  has(flt, fGrantForged),
		unknown: has(flt, fGrantUnknown),
	}
	grant := f.build(grantSpec)

	// the same grant, attenuated one link — re-signed by its issuer so
	// the signature stays valid (attenuation is the issuer adding a
	// caveat, monotone by construction).
	attSpec := grantSpec
	attSpec.cav = Caveats{
		Target: append([]ActorID(nil), grantSpec.cav.Target...),
		Facet:  append([]string(nil), grantSpec.cav.Facet...),
	}
	switch attKind {
	case 0: // ShrinkTarget: drop R
		attSpec.cav.Target = removeID(attSpec.cav.Target, id["R"])
	case 1: // ShrinkFacet: drop apid
		attSpec.cav.Facet = removeStr(attSpec.cav.Facet, "apid")
	case 2: // AddUnknownCaveat
		attSpec.unknown = true
	case 3: // ShortenExpiry
		attSpec.exp = 0
	}
	grantAtt := f.build(attSpec)

	alpn := "mesh/apid/v1"
	if has(flt, fAlpnBogus) {
		alpn = "mesh/bogus/v1"
	}
	table := map[string]string{"mesh/apid/v1": "apid", "mesh/kube/v1": "kube-api"}
	blocklist := map[ActorID]bool{}
	if has(flt, fBlocklisted) {
		blocklist[id["CALLER"]] = true
	}

	base := Input{
		Receiver: id["R"], AcceptTable: table, Consents: consents,
		Blocklist: blocklist, Now: testNOW, ALPN: alpn, Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant}},
	}
	att := base
	att.Bundle = Bundle{Member: member, Grants: []Cert{grantAtt}}

	return scenario{in: base, att: att, member: member, grant: grant,
		consents: consents, alpn: alpn, facet: "apid", f: flt}
}

func TestAuthorizeLaws(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := newFixture(t)
		s := genNear(t, f)
		res := Authorize(s.in)
		resAtt := Authorize(s.att)
		checkLaws(t, f, s, res, resAtt)
	})
}

// TestGenNearReachesAccept proves the boundary-biased generator is not
// vacuous: over a modest sample it produces Accepts, so the fail-closed
// laws above are actually exercised against a live happy path (the model
// documents that pure-random generation never accepts).
func TestGenNearReachesAccept(t *testing.T) {
	accepts := 0
	rapid.Check(t, func(t *rapid.T) {
		f := newFixture(t)
		s := genNear(t, f)
		if Authorize(s.in).OK {
			accepts++
		}
	})
	if accepts == 0 {
		t.Fatal("genNear never reached Accept — laws would hold vacuously")
	}
	t.Logf("genNear reached Accept %d times", accepts)
}

// sigValid ports the model's not(forged): the signature verifies.
func sigValid(c Cert) bool { return Verify(c) == nil }

// sigOKlaw ports sigOk(c, NOW): verifies, unexpired, no unknown caveat.
func sigOKlaw(c Cert) bool { return sigValid(c) && c.Exp > testNOW && !c.Cav.Unknown }

func checkLaws(t *rapid.T, f fixture, s scenario, res, resAtt Result) {
	id := f.id
	table := s.in.AcceptTable
	m := s.member
	g := s.grant

	// invUnknownAlpnRejects
	if _, ok := table[s.alpn]; !ok && res.OK {
		t.Fatalf("invUnknownAlpnRejects: accepted unknown ALPN %q", s.alpn)
	}
	// invReceiverRooted: no valid R-signed consent ⇒ reject.
	rRooted := false
	for _, c := range s.consents {
		if c.Iss == id["R"] && sigOKlaw(c) {
			rRooted = true
		}
	}
	if !rRooted && res.OK {
		t.Fatal("invReceiverRooted: accepted with no live R-signed consent")
	}
	// invConsentMustDelegate: all consents non-delegable ⇒ reject.
	allNonDeleg := true
	for _, c := range s.consents {
		if c.Cav.Delegable {
			allNonDeleg = false
		}
	}
	if allNonDeleg && res.OK {
		t.Fatal("invConsentMustDelegate: accepted with no delegable consent")
	}
	// invConsentTargetsSelf: no consent targets R ⇒ reject.
	anyTargetsR := false
	for _, c := range s.consents {
		if containsID(c.Cav.Target, id["R"]) {
			anyTargetsR = true
		}
	}
	if !anyTargetsR && res.OK {
		t.Fatal("invConsentTargetsSelf: accepted with no consent targeting R")
	}
	// invMemberRequired
	badMember := !sigValid(m) || m.Exp <= testNOW || m.Can != VerbMember ||
		m.Aud != string(id["CALLER"]) || m.Cav.Unknown
	if badMember && res.OK {
		t.Fatal("invMemberRequired: accepted an invalid member cert")
	}
	// invMemberIssuerConsented: accept ⇒ a live R-consent for member.iss.
	if res.OK {
		ok := false
		for _, c := range s.consents {
			if c.Iss == id["R"] && c.Aud == string(m.Iss) && sigOKlaw(c) {
				ok = true
			}
		}
		if !ok {
			t.Fatal("invMemberIssuerConsented: accepted with member issuer not consented")
		}
	}
	// invBlocklistRejects
	if s.in.Blocklist[id["CALLER"]] && res.OK {
		t.Fatal("invBlocklistRejects: accepted a blocklisted peer")
	}
	// invGrantsFailClosed: every grant broken ⇒ reject.
	grantBroken := !sigValid(g) || g.Exp <= testNOW || g.Can != VerbInvoke || g.Cav.Unknown
	if grantBroken && res.OK {
		t.Fatal("invGrantsFailClosed: accepted with only broken grants")
	}
	// invCrossIssuerGroupRejects: only group grants from a different
	// issuer than the member cert ⇒ reject.
	if grp, isGroup := trimGroup(g.Aud); isGroup && g.Iss != m.Iss && res.OK {
		_ = grp
		t.Fatal("invCrossIssuerGroupRejects: cross-issuer group grant accepted")
	}
	// invTargetIsReceiver
	if !containsID(g.Cav.Target, id["R"]) && res.OK {
		t.Fatal("invTargetIsReceiver: accepted a grant not targeting R")
	}
	// invFacetMatchesAlpn
	if fct, ok := table[s.alpn]; ok && !containsStr(g.Cav.Facet, fct) && res.OK {
		t.Fatal("invFacetMatchesAlpn: accepted a grant whose facet omits the ALPN's")
	}
	// invIdentityFromMember
	if res.OK {
		want := Identity{Key: id["CALLER"], Name: m.Cav.Name, Groups: m.Cav.Groups}
		if !identityEqual(res.Identity, want) {
			t.Fatalf("invIdentityFromMember: got %+v want %+v", res.Identity, want)
		}
	}
	// invMonotone: attenuated accepts ⇒ base accepts.
	if resAtt.OK && !res.OK {
		t.Fatal("invMonotone: attenuated bundle accepted but base rejected")
	}
}

func identityEqual(a, b Identity) bool {
	if a.Key != b.Key || a.Name != b.Name || len(a.Groups) != len(b.Groups) {
		return false
	}
	for i := range a.Groups {
		if a.Groups[i] != b.Groups[i] {
			return false
		}
	}
	return true
}

func trimGroup(aud string) (string, bool) {
	const p = "group:"
	if len(aud) > len(p) && aud[:len(p)] == p {
		return aud[len(p):], true
	}
	return "", false
}

func removeID(xs []ActorID, x ActorID) []ActorID {
	var out []ActorID
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func removeStr(xs []string, x string) []string {
	var out []string
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

// TestAuthorizeHappyPath is the model's happyPathTest: the all-FNone
// scenario must actually accept, else every law above holds vacuously.
func TestAuthorizeHappyPath(t *testing.T) {
	// deterministic keys for readability
	f := fixture{signer: map[string]Signer{}, id: map[string]ActorID{}}
	seed := byte(1)
	for _, name := range []string{"OWNER1", "OWNER2", "R", "OTHER_R", "CALLER", "ROGUE", "FORGER"} {
		priv := ed25519.NewKeyFromSeed(bytesFill(seed))
		s := NewEdSigner(priv)
		f.signer[name] = s
		f.id[name] = s.ActorID()
		seed++
	}
	id := f.id
	consent := f.build(certSpec{iss: "R", aud: string(id["OWNER1"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid", "kube-api"}, Delegable: true}, exp: 10})
	member := f.build(certSpec{iss: "OWNER1", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 10})
	for _, audKind := range []string{string(id["CALLER"]), "group:admins"} {
		grant := f.build(certSpec{iss: "OWNER1", aud: audKind, can: VerbInvoke,
			cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 10})
		in := Input{Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
			Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
			ALPN: "mesh/apid/v1", Peer: id["CALLER"], Bundle: Bundle{Member: member, Grants: []Cert{grant}}}
		res := Authorize(in)
		if !res.OK {
			t.Fatalf("happy path (aud=%s) rejected", audKind)
		}
		if res.Identity.Name != "laptop" {
			t.Fatalf("identity name = %q", res.Identity.Name)
		}
		if len(res.Verified) == 0 {
			t.Fatal("no verified certs returned for low-water mark")
		}
	}
}

func bytesFill(b byte) []byte {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = b
	}
	return s
}

var _ = hex.EncodeToString
