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
// the two hub process keys HUB_A, HUB_B (ADR-0018 epochs), plus a
// distinct FORGER key used to produce invalid signatures.
type fixture struct {
	signer map[string]Signer
	id     map[string]ActorID
}

// principalNames is the model's closed world of keys.
var principalNames = []string{"OWNER1", "OWNER2", "R", "OTHER_R", "CALLER", "ROGUE", "HUB_A", "HUB_B", "FORGER"}

// detFixture builds principalNames with deterministic seeds (readable,
// reproducible) for the hand-written scenario tests.
func detFixture(seed byte) fixture {
	f := fixture{signer: map[string]Signer{}, id: map[string]ActorID{}}
	for _, name := range principalNames {
		priv := ed25519.NewKeyFromSeed(bytesFill(seed))
		s := NewEdSigner(priv)
		f.signer[name] = s
		f.id[name] = s.ActorID()
		seed++
	}
	return f
}

// Model constants: the speak-as caveat universe (ADR-0018: literal).
var (
	modelVerbs  = []string{"member", "invoke"}
	modelGroups = []string{"admins", "media"}
)

func newFixture(t *rapid.T) fixture {
	f := fixture{signer: map[string]Signer{}, id: map[string]ActorID{}}
	for _, name := range principalNames {
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
	iat     int64 // defaults to 1 when zero
	exp     int64
	forged  bool // sign with FORGER but claim iss ⇒ signature fails
	unknown bool // set Cav.Unknown after signing (verifier judgment)
}

func (f fixture) build(s certSpec) Cert {
	signer := f.signer[s.iss]
	if s.forged {
		signer = f.signer["FORGER"]
	}
	iat := s.iat
	if iat == 0 {
		iat = 1
	}
	c := Cert{Aud: s.aud, Can: s.can, Cav: s.cav, Iat: iat, Exp: s.exp}
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
	// speak-as faults (hub-signed scenarios; no-ops otherwise)
	fSpeakAsAMissing
	fSpeakAsAExpired
	fSpeakAsAForged
	fSpeakAsAUnknown
	fSpeakAsAWrongVerb
	fSpeakAsAVerbsInvokeOnly
	fSpeakAsAGroupsMedia
	fSpeakAsAFromOwner2
	fSpeakAsAAudHubB
	fSpeakAsBMissing
	fSpeakAsBExpired
	fSpeakAsBVerbsMemberOnly
	fSpeakAsBGroupsMedia
	fSpeakAsBFromOwner2
	fSpeakAsRogueVouchesBoth
	numFaults
)

// attenuation kinds — the model's Att. The first four attenuate the
// grant link; the last three every speak-as link.
const (
	attShrinkTarget = iota
	attShrinkFacet
	attAddUnknownCaveat
	attShortenExpiry
	attShrinkSpeakAsGroups
	attShrinkSpeakAsVerbs
	attShortenSpeakAsExpiry
	numAtts
)

type scenario struct {
	in        Input
	att       Input // same, but with one link (grant or speak-as) attenuated
	member    Cert
	grant     Cert
	speakAs   []Cert // the bundle's speak-as links (ADR-0018)
	consents  []Cert
	alpn      string
	facet     string
	hubSigned bool // member by HUB_A, grant by HUB_B (post-redeploy shape)
	f         map[fault]bool
}

func has(f map[fault]bool, x fault) bool { return f[x] }

// genNear builds a near-valid scenario with up to two faults — the Go
// analogue of authorize.qnt's genNear action.
func genNear(t *rapid.T, f fixture) scenario {
	f1 := fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f1"))
	f2 := fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f2"))
	audKind := rapid.IntRange(0, 1).Draw(t, "audKind")
	attKind := rapid.IntRange(0, numAtts-1).Draw(t, "att")
	attG := rapid.SampledFrom(modelGroups).Draw(t, "attG")
	// false: wallets sign directly (pre-ADR-0018 shape); true: member by
	// HUB_A, grant by HUB_B, speak-as links from OWNER1 (post-redeploy).
	hubSigned := rapid.Bool().Draw(t, "hubSigned")

	flt := map[fault]bool{f1: true, f2: true}

	id := f.id
	facets := []string{"apid", "kube-api"}
	memberSigner, grantSigner := "OWNER1", "OWNER1"
	if hubSigned {
		memberSigner, grantSigner = "HUB_A", "HUB_B"
	}

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
	memberIss := memberSigner
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
	grantIss := grantSigner
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
	case attShrinkTarget: // drop R
		attSpec.cav.Target = removeID(attSpec.cav.Target, id["R"])
	case attShrinkFacet: // drop apid
		attSpec.cav.Facet = removeStr(attSpec.cav.Facet, "apid")
	case attAddUnknownCaveat:
		attSpec.unknown = true
	case attShortenExpiry:
		attSpec.exp = 0
	}
	grantAtt := f.build(attSpec)

	// speak-as links (hub-signed scenarios only): sA for the member's
	// signer (HUB_A), sB for the grant's (HUB_B), both from OWNER1 unless
	// faulted; ROGUE's two links are the 9l3 bundle.
	var saSpecs []certSpec
	if hubSigned {
		sAIss := "OWNER1"
		if has(flt, fSpeakAsAFromOwner2) {
			sAIss = "OWNER2"
		}
		sAAud := id["HUB_A"]
		if has(flt, fSpeakAsAAudHubB) {
			sAAud = id["HUB_B"]
		}
		sAVerbs := modelVerbs
		if has(flt, fSpeakAsAVerbsInvokeOnly) {
			sAVerbs = []string{"invoke"}
		}
		sAGroups := modelGroups
		if has(flt, fSpeakAsAGroupsMedia) {
			sAGroups = []string{"media"}
		}
		sAExp := int64(10)
		if has(flt, fSpeakAsAExpired) {
			sAExp = testNOW
		}
		sACan := VerbSpeakAs
		if has(flt, fSpeakAsAWrongVerb) {
			sACan = VerbInvoke
		}
		sA := certSpec{iss: sAIss, aud: string(sAAud), can: sACan,
			cav:     Caveats{Verbs: sAVerbs, Groups: sAGroups},
			exp:     sAExp,
			forged:  has(flt, fSpeakAsAForged),
			unknown: has(flt, fSpeakAsAUnknown)}
		sBIss := "OWNER1"
		if has(flt, fSpeakAsBFromOwner2) {
			sBIss = "OWNER2"
		}
		sBVerbs := modelVerbs
		if has(flt, fSpeakAsBVerbsMemberOnly) {
			sBVerbs = []string{"member"}
		}
		sBGroups := modelGroups
		if has(flt, fSpeakAsBGroupsMedia) {
			sBGroups = []string{"media"}
		}
		sBExp := int64(10)
		if has(flt, fSpeakAsBExpired) {
			sBExp = testNOW
		}
		sB := certSpec{iss: sBIss, aud: string(id["HUB_B"]), can: VerbSpeakAs,
			cav: Caveats{Verbs: sBVerbs, Groups: sBGroups}, exp: sBExp}
		if !has(flt, fSpeakAsAMissing) {
			saSpecs = append(saSpecs, sA)
		}
		if !has(flt, fSpeakAsBMissing) {
			saSpecs = append(saSpecs, sB)
		}
		if has(flt, fSpeakAsRogueVouchesBoth) {
			for _, hub := range []string{"HUB_A", "HUB_B"} {
				saSpecs = append(saSpecs, certSpec{iss: "ROGUE", aud: string(id[hub]), can: VerbSpeakAs,
					cav: Caveats{Verbs: modelVerbs, Groups: modelGroups}, exp: 10})
			}
		}
	}
	speakAs := make([]Cert, 0, len(saSpecs))
	speakAsAtt := make([]Cert, 0, len(saSpecs))
	for _, sp := range saSpecs {
		speakAs = append(speakAs, f.build(sp))
		// attenuateSpeakAs: the issuer re-signs with one caveat added;
		// grant-side attenuations leave the link unchanged.
		ap := sp
		ap.cav = Caveats{
			Verbs:  append([]string(nil), sp.cav.Verbs...),
			Groups: append([]string(nil), sp.cav.Groups...),
		}
		switch attKind {
		case attShrinkSpeakAsGroups:
			ap.cav.Groups = removeStr(ap.cav.Groups, attG)
		case attShrinkSpeakAsVerbs:
			ap.cav.Verbs = removeStr(ap.cav.Verbs, "member")
		case attShortenSpeakAsExpiry:
			ap.exp = 0
		}
		speakAsAtt = append(speakAsAtt, f.build(ap))
	}

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
		Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: speakAs},
	}
	att := base
	att.Bundle = Bundle{Member: member, Grants: []Cert{grantAtt}, SpeakAs: speakAsAtt}

	return scenario{in: base, att: att, member: member, grant: grant, speakAs: speakAs,
		consents: consents, alpn: alpn, facet: "apid", hubSigned: hubSigned, f: flt}
}

// scenarioGen wraps genNear as a rapid generator so coverage tests can
// draw deterministic examples (Generator.Example) outside rapid.Check.
func scenarioGen(f fixture) *rapid.Generator[scenario] {
	return rapid.Custom(func(t *rapid.T) scenario { return genNear(t, f) })
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
// vacuous (the model documents that pure-random generation never
// accepts): over a fixed, deterministic sample it (a) reaches Accept
// both with wallets signing directly AND with a hub-signed member plus
// a hub-signed grant (the post-redeploy shape), and (b) hits each
// speak-as law's antecedent a meaningful number of times, so no law in
// TestSpeakAsLaws holds vacuously.
func TestGenNearReachesAccept(t *testing.T) {
	const samples = 2000
	const minHits = 5
	f := detFixture(40)
	gen := scenarioGen(f)
	var accepts, hubAccepts, hubGroupAccepts int
	hits := map[string]int{}
	for i := 0; i < samples; i++ {
		s := gen.Example(i)
		res := Authorize(s.in)
		if res.OK {
			accepts++
			if s.hubSigned {
				hubAccepts++
				if _, isGroup := trimGroup(s.grant.Aud); isGroup {
					hubGroupAccepts++
				}
			}
		}
		for _, l := range speakAsLaws {
			hit, ok := l.check(f.id, s, res)
			if !ok {
				t.Fatalf("%s violated on example %d", l.name, i)
			}
			if hit {
				hits[l.name]++
			}
		}
	}
	t.Logf("genNear over %d samples: %d accepts, %d hub-signed (%d with a group grant)", samples, accepts, hubAccepts, hubGroupAccepts)
	if accepts == 0 {
		t.Fatal("genNear never reached Accept — laws would hold vacuously")
	}
	if hubAccepts < minHits {
		t.Fatalf("hub-signed member + hub-signed grant accepted only %d times", hubAccepts)
	}
	if hubGroupAccepts == 0 {
		t.Fatal("hub-signed GROUP grant never accepted — invGroupMatchRootedInChain would hold vacuously")
	}
	for _, l := range speakAsLaws {
		t.Logf("%-32s antecedent hit %d times", l.name, hits[l.name])
		if hits[l.name] < minHits {
			t.Errorf("%s: antecedent hit only %d times (< %d)", l.name, hits[l.name], minHits)
		}
	}
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
	// invMemberIssuerConsented (2b on the RESOLVED issuer): accept ⇒ some
	// wallet R holds a live consent for vouches for the member cert —
	// the signer itself, or via a live speak-as covering `member` and
	// the member's groups.
	if res.OK {
		ok := false
		for _, w := range wallets(id) {
			if !liveConsentTo(id, s.consents, w) {
				continue
			}
			if w == m.Iss {
				ok = true
			}
			for _, sa := range s.speakAs {
				if vouches(sa, w, m.Iss) && containsStr(sa.Cav.Verbs, "member") && subset(m.Cav.Groups, sa.Cav.Groups) {
					ok = true
				}
			}
		}
		if !ok {
			t.Fatal("invMemberIssuerConsented: accepted with no consented wallet vouching for the member")
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
	// invCrossIssuerGroupRejects, on RESOLVED issuers: a group grant
	// whose signer shares no POSSIBLE sovereign with the member's signer
	// (loosest reading: the key itself plus every speak-as iss naming
	// it, ignoring all checks) can never resolve ⇒ reject.
	if _, isGroup := trimGroup(g.Aud); isGroup && res.OK {
		if len(intersectID(attributable(s.speakAs, g.Iss), attributable(s.speakAs, m.Iss))) == 0 {
			t.Fatal("invCrossIssuerGroupRejects: cross-issuer group grant accepted")
		}
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

// --- raw-field helpers shared by the laws (never via resolve, so a bug
// seeded in Authorize cannot hide behind the same bug in a law) ---

// wallets is the model's WALLETS: the sovereign roots receivers consent to.
func wallets(id map[string]ActorID) []ActorID {
	return []ActorID{id["OWNER1"], id["OWNER2"], id["ROGUE"]}
}

// isHub is the model's HUBS.contains(k).
func isHub(id map[string]ActorID, k ActorID) bool {
	return k == id["HUB_A"] || k == id["HUB_B"]
}

// liveConsentTo ports the model's liveConsentTo: a live R-signed consent
// naming sovereign w (raw fields; delegability and target are separate laws).
func liveConsentTo(id map[string]ActorID, consents []Cert, w ActorID) bool {
	for _, c := range consents {
		if c.Iss == id["R"] && c.Aud == string(w) && sigOKlaw(c) {
			return true
		}
	}
	return false
}

// vouches ports the model's vouches: speak-as s lets wallet w vouch for
// signer k (raw fields).
func vouches(s Cert, w, k ActorID) bool {
	return s.Iss == w && s.Aud == string(k) && s.Can == VerbSpeakAs && sigOKlaw(s)
}

// attributable ports the model's attributable: everything signer k could
// POSSIBLY be attributed to, ignoring every check.
func attributable(speakAs []Cert, k ActorID) []ActorID {
	out := []ActorID{k}
	for _, s := range speakAs {
		if s.Aud == string(k) && !containsID(out, s.Iss) {
			out = append(out, s.Iss)
		}
	}
	return out
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
	f := detFixture(1) // deterministic keys for readability
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
