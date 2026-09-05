package cert

import (
	"crypto/ed25519"
	"testing"

	"pgregory.net/rapid"
)

// This file ports the eight speak-as laws of verification/quint/
// authorize.qnt (ADR-0018, rulings 9l3/zpf → decisions 4oz/w5s) 1:1,
// over the same near-valid scenario shape (genNear in
// authorize_laws_test.go) and real Ed25519 signatures. Each law is one
// rapid property named after the model's inv*. Laws are stated over RAW
// scenario fields — never via resolve — so a bug seeded in Authorize
// cannot hide behind the same bug in a law.

// law is one model invariant: hit reports whether the law's antecedent
// held for this scenario (for non-vacuity accounting), ok whether the
// law holds.
type law struct {
	name  string
	check func(id map[string]ActorID, s scenario, res Result) (hit, ok bool)
}

// speakAsFor: the bundle's speak-as certs naming signer k (raw aud match).
func speakAsFor(speakAs []Cert, k ActorID) []Cert {
	var out []Cert
	for _, s := range speakAs {
		if s.Aud == string(k) {
			out = append(out, s)
		}
	}
	return out
}

// allSpeakAs reports whether every speak-as naming k satisfies pred —
// the model's `speakAs.forall(s => not(isKey(s.aud, k)) or pred(s))`.
func allSpeakAs(speakAs []Cert, k ActorID, pred func(Cert) bool) bool {
	for _, s := range speakAsFor(speakAs, k) {
		if !pred(s) {
			return false
		}
	}
	return true
}

var speakAsLaws = []law{
	// ADR-0018 law 2 — verification-time validity: a hub-signed member
	// cert whose every speak-as is expired is rejected, whatever
	// member.exp says.
	{"invSpeakAsExpiredRejects", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		m := s.member
		ante := isHub(id, m.Iss) && allSpeakAs(s.speakAs, m.Iss, func(sa Cert) bool { return sa.Exp <= testNOW })
		return ante, !ante || !res.OK
	}},
	// ADR-0018 law 3 — a speak-as whose cav.verbs omits `member` cannot
	// resolve a member cert.
	{"invSpeakAsVerbScoped", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		m := s.member
		ante := isHub(id, m.Iss) && allSpeakAs(s.speakAs, m.Iss, func(sa Cert) bool { return !containsStr(sa.Cav.Verbs, "member") })
		return ante, !ante || !res.OK
	}},
	// ADR-0018 law 4 — a speak-as whose cav.groups does not cover the
	// member's groups cannot resolve it (literal caveats).
	{"invSpeakAsGroupsCoverMember", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		m := s.member
		ante := isHub(id, m.Iss) && allSpeakAs(s.speakAs, m.Iss, func(sa Cert) bool { return !subset(m.Cav.Groups, sa.Cav.Groups) })
		return ante, !ante || !res.OK
	}},
	// ADR-0018 law 5 — receiver-rooted survives the extra link: a
	// speak-as from a wallet R never consented to resolves to nothing.
	{"invSpeakAsIssuerConsented", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		m := s.member
		ante := isHub(id, m.Iss) && allSpeakAs(s.speakAs, m.Iss, func(sa Cert) bool { return !liveConsentTo(id, s.consents, sa.Iss) })
		return ante, !ante || !res.OK
	}},
	// Fail-closed on the link itself: a forged speak-as, a cert with
	// another verb, or one carrying an unknown caveat resolves nothing.
	{"invSpeakAsSigRequired", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		m := s.member
		ante := isHub(id, m.Iss) && allSpeakAs(s.speakAs, m.Iss, func(sa Cert) bool {
			return !sigValid(sa) || sa.Can != VerbSpeakAs || sa.Cav.Unknown
		})
		return ante, !ante || !res.OK
	}},
	// The grant's issuer resolves the same way: a hub-signed grant with
	// no live, consented, `invoke`-covering speak-as admits nothing.
	{"invGrantSpeakAsRequired", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		g := s.grant
		ante := isHub(id, g.Iss) && allSpeakAs(s.speakAs, g.Iss, func(sa Cert) bool {
			return sa.Exp <= testNOW || !sigValid(sa) || sa.Can != VerbSpeakAs || sa.Cav.Unknown ||
				!containsStr(sa.Cav.Verbs, "invoke") || !liveConsentTo(id, s.consents, sa.Iss)
		})
		return ante, !ante || !res.OK
	}},
	// Literal caveats on the grant side (decision w5s): a hot key may
	// address group:g only under a speak-as whose cav.groups contains g.
	{"invGrantSpeakAsGroupsCoverAud", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		g := s.grant
		grp, isGroup := trimGroup(g.Aud)
		ante := isHub(id, g.Iss) && isGroup &&
			allSpeakAs(s.speakAs, g.Iss, func(sa Cert) bool { return !containsStr(sa.Cav.Groups, grp) })
		return ante, !ante || !res.OK
	}},
	// FINDING 2026-09-06 (9l3, decision 4oz): a group match must be
	// rooted in ONE consented sovereign that vouches for BOTH the
	// grant's signer and the member's signer — overlap of resolved sets
	// is not enough (a stranger wallet can vouch for any hub key).
	// Antecedent counted: an Accept on a hub-signed group grant, the
	// only case where the law can bite.
	{"invGroupMatchRootedInChain", func(id map[string]ActorID, s scenario, res Result) (bool, bool) {
		if !res.OK {
			return false, true
		}
		g, m := s.grant, s.member
		grp, isGroup := trimGroup(g.Aud)
		hit := isGroup && s.hubSigned
		if !isGroup {
			return hit, g.Aud == string(id["CALLER"])
		}
		if !containsStr(m.Cav.Groups, grp) {
			return hit, false
		}
		for _, w := range wallets(id) {
			if !liveConsentTo(id, s.consents, w) {
				continue
			}
			vouchG := w == g.Iss
			vouchM := w == m.Iss
			for _, sa := range s.speakAs {
				vouchG = vouchG || vouches(sa, w, g.Iss)
				vouchM = vouchM || vouches(sa, w, m.Iss)
			}
			if vouchG && vouchM {
				return hit, true
			}
		}
		return hit, false
	}},
}

// TestSpeakAsLaws runs each of the eight speak-as laws as its own rapid
// property over genNear.
func TestSpeakAsLaws(t *testing.T) {
	for _, l := range speakAsLaws {
		l := l
		t.Run(l.name, func(t *testing.T) {
			rapid.Check(t, func(t *rapid.T) {
				f := newFixture(t)
				s := genNear(t, f)
				res := Authorize(s.in)
				if _, ok := l.check(f.id, s, res); !ok {
					t.Fatalf("%s violated (faults %v, hubSigned %v, aud %s)", l.name, faultNames(s.f), s.hubSigned, s.grant.Aud)
				}
			})
		})
	}
}

func faultNames(f map[fault]bool) []int {
	var out []int
	for k := range f {
		out = append(out, int(k))
	}
	return out
}

// mixedEpochScenario is the model's initMixedEpoch: member cert by
// HUB_A, group grant by HUB_B, two valid speak-as from the same wallet,
// consent to the wallet. This is the shape every bundle has after a
// redeploy. The attenuated twin expires the member's speak-as alone.
func mixedEpochScenario(f fixture) (base, att Input) {
	id := f.id
	sa := func(hub string, exp int64) Cert {
		return f.build(certSpec{iss: "OWNER1", aud: string(id[hub]), can: VerbSpeakAs,
			cav: Caveats{Verbs: modelVerbs, Groups: modelGroups}, exp: exp})
	}
	consent := f.build(certSpec{iss: "R", aud: string(id["OWNER1"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid", "kube-api"}, Delegable: true}, exp: 10})
	member := f.build(certSpec{iss: "HUB_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 10})
	grant := f.build(certSpec{iss: "HUB_B", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 10})
	base = Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{sa("HUB_A", 10), sa("HUB_B", 10)}},
	}
	att = base
	att.Bundle = Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{sa("HUB_A", testNOW), sa("HUB_B", 10)}}
	return base, att
}

// TestMixedEpoch is the model's mixedEpochTest (ADR-0018 law 1): mixed
// epochs accept; expiring the member's speak-as alone (law 2) rejects,
// with member.exp untouched.
func TestMixedEpoch(t *testing.T) {
	f := detFixture(10)
	base, att := mixedEpochScenario(f)
	res := Authorize(base)
	if !res.OK {
		t.Fatal("mixed-epoch bundle rejected; expected Accept")
	}
	if res.Identity.Name != "laptop" {
		t.Fatalf("identity name = %q", res.Identity.Name)
	}
	if Authorize(att).OK {
		t.Fatal("accepted with the member's speak-as expired (member.exp is live)")
	}
}

// TestRogueVouchesBothHubsRejects is the 9l3 FINDING verbatim: R
// consents to OWNER1 and OWNER2; CALLER holds OWNER2's `admins` member
// cert (signed by HUB_A, under OWNER2→HUB_A); OWNER1's hub HUB_B granted
// group:admins at R (under OWNER1→HUB_B); the bundle adds self-signed
// ROGUE→HUB_A and ROGUE→HUB_B. Resolved sets {HUB_A, OWNER2, ROGUE} and
// {HUB_B, OWNER1, ROGUE} overlap on ROGUE — set overlap would Accept and
// let OWNER2's member reach OWNER1's admins facet. The ruled rule (one
// consented w vouches for both ends) rejects: OWNER1 does not vouch for
// HUB_A, OWNER2 not for HUB_B, ROGUE is not consented.
func TestRogueVouchesBothHubsRejects(t *testing.T) {
	f := detFixture(20)
	id := f.id
	sa := func(wallet, hub string) Cert {
		return f.build(certSpec{iss: wallet, aud: string(id[hub]), can: VerbSpeakAs,
			cav: Caveats{Verbs: modelVerbs, Groups: modelGroups}, exp: 10})
	}
	consent := func(owner string) Cert {
		return f.build(certSpec{iss: "R", aud: string(id[owner]), can: VerbInvoke,
			cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid", "kube-api"}, Delegable: true}, exp: 10})
	}
	member := f.build(certSpec{iss: "HUB_A", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 10})
	grant := f.build(certSpec{iss: "HUB_B", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 10})
	in := Input{
		Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent("OWNER1"), consent("OWNER2")}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"],
		Bundle: Bundle{Member: member, Grants: []Cert{grant},
			SpeakAs: []Cert{sa("OWNER2", "HUB_A"), sa("OWNER1", "HUB_B"), sa("ROGUE", "HUB_A"), sa("ROGUE", "HUB_B")}},
	}
	if Authorize(in).OK {
		t.Fatal("ROGUE bridged HUB_A and HUB_B: OWNER2's member reached OWNER1's admins facet")
	}
	// Control: without the rogue links the bundle is still rejected
	// (no common consented wallet), and with OWNER1 vouching for HUB_A
	// too it is accepted — so the rejection above is the group rule,
	// not a broken fixture.
	in.Bundle.SpeakAs = []Cert{sa("OWNER2", "HUB_A"), sa("OWNER1", "HUB_B")}
	if Authorize(in).OK {
		t.Fatal("no common consented wallet, yet accepted")
	}
	in.Bundle.SpeakAs = []Cert{sa("OWNER2", "HUB_A"), sa("OWNER1", "HUB_B"), sa("OWNER1", "HUB_A")}
	if !Authorize(in).OK {
		t.Fatal("OWNER1 vouches for both hub keys, yet rejected")
	}
}

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

// TestReUnsealExpiredSpeakAsBeforeValid: after re-unseal without a
// redeploy, the bundle can carry a stale speak-as beside a fresh valid
// one for the SAME hot key. Kept as documentation of a Go-specific
// regression (review round 2, fix #2): the set-valued resolve subsumes
// it — the stale link contributes nothing, the fresh one still vouches,
// regardless of ordering.
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
