package cert

import (
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// This file ports the N-link chain half of verification/quint/authorize.qnt
// (protocol ADR-0001, talos-config-0bc.2.1) 1:1 — every invChain* /
// invPostage* / invEndpointsIntersect / invStarFailsClosedWithoutPostage /
// invAudSpeakAsCoversVerb / invEffectiveIsIntersection law in its invAll,
// plus the four witness runs — over REAL Ed25519 signatures. The
// generator mirrors the model's nearChain: a correct 2-link caller chain
// [OWNER1→OWNER2, OWNER2→aud] under consent(R→OWNER1), hub-signed or not,
// carrying `invoke` or `publish` throughout (xwu: the chain's verb is the
// root consent's), in five kinds, with up to two of the 32 chain faults
// injected; the consents come from the connection-level genNear
// (buildConsents) so consent faults reach the chain through the shared
// root.
//
// Laws are stated over RAW fields (never via VerifyChain's helpers) and
// over the SET of verdicts (the package-internal verifyChain — the
// model's Set[Verdict]); the public VerifyChain is checked to agree.

// Model constants: caveat vocabulary v2 values.
var modelEndpoints = []string{"quic:a", "quic:b"}

const (
	noPostage  = ""
	postagePoW = "pow:20"
	postagePay = "pay:1"
)

// cfault enumerates the model's CFAULTS (nearChain's fault set). Kept
// apart from `fault` so the connection-level sweep is untouched.
type cfault int

const (
	cfNone cfault = iota
	cfLink1Forged
	cfLink1Expired
	cfLink1Unknown
	cfLink1NotDelegable
	cfLink1IssRogue
	cfLink1AudRogue
	cfLink1TargetOtherR
	cfLink1FacetKubeOnly
	cfLink1EndpointsA
	cfLink1Postage
	cfLink1VerbOtherChain // link 1 carries the OTHER chain verb (well-formed, wrong root)
	cfLink2Forged
	cfLink2Expired
	cfLink2Unknown
	cfLink2IssRogue
	cfLink2VerbOther
	cfLink2AudRogue
	cfLink2FacetKubeOnly
	cfLink2EndpointsB
	cfLink2Postage
	cfPostageConflict
	cfStarNoPostage
	cfSignerRogue
	cfDropLink1
	cfAudSpeakAsMissing
	cfAudSpeakAsExpired
	cfAudSpeakAsForged
	cfAudSpeakAsNoVerb // speak-as covers member only, not the chain's verb
	cfAudSpeakAsFromRogue
	cfHubANoSpeakAs
	cfHubBSpeakAsExpired
	cfHubBSpeakAsVerbless // likewise, on the HUB_B link
	numCFaults
)

// chainVerbs is the model's CHAIN_VERBS: what a receiver may expect a
// chain to carry. Publish stands for every non-invoke verb (M3).
var chainVerbs = []Verb{VerbInvoke, VerbPublish}

// otherChainVerb is the model's otherChainVerb (for cfLink1VerbOtherChain).
func otherChainVerb(v Verb) Verb {
	if v == VerbInvoke {
		return VerbPublish
	}
	return VerbInvoke
}

// chainKind is the model's ChainKind: how the last link binds its presenter.
type chainKind int

const (
	kindKey         chainKind = iota // aud: CALLER, presented by CALLER
	kindSpeakAs                      // aud: CALLER, presented by CALLER_HOT under speak-as CALLER→CALLER_HOT
	kindStar                         // aud: "*" with postage, presented by ROGUE
	kindGroup                        // aud: group:admins (the ErrGroupAud sentinel), presented by CALLER
	kindConsentOnly                  // no caller link: OWNER1 presents R's consent itself
	numKinds
)

// chainParams are the nondet choices of the model's genNear chain half.
type chainParams struct {
	cf1, cf2   cfault
	kind       chainKind
	verb       Verb   // the verb the chain carries and R expects (model: cv / cverb)
	hubSigned  bool   // link1 by HUB_A, link2 by HUB_B under speak-as from OWNER1/OWNER2
	f1, f2     fault  // connection-level faults; only the consent ones bite here
	cPostage   string // postage on consent1 itself
	attI       int    // which chain link the attenuation perturbs (0 or 1)
	attKind    int    // one of the att* kinds
	attT       string // principal dropped from target (attShrinkTarget)
	attF, attG string // facet / speak-as group dropped
	attE       string // endpoint dropped (attShrinkEndpoints)
}

type chainScenario struct {
	p          chainParams
	consents   []Cert
	chain      []Cert
	chainAtt   []Cert
	speakAs    []Cert
	speakAsAtt []Cert
	signer     ActorID
	facet      string
	verb       Verb
	f          map[cfault]bool
}

func hasC(f map[cfault]bool, x cfault) bool { return f[x] }

func genChain(t *rapid.T, f fixture) chainScenario {
	return buildChainScenario(f, chainParams{
		cf1:       cfault(rapid.IntRange(0, int(numCFaults)-1).Draw(t, "cf1")),
		cf2:       cfault(rapid.IntRange(0, int(numCFaults)-1).Draw(t, "cf2")),
		kind:      chainKind(rapid.IntRange(0, int(numKinds)-1).Draw(t, "kind")),
		verb:      rapid.SampledFrom(chainVerbs).Draw(t, "cv"),
		hubSigned: rapid.Bool().Draw(t, "cHub"),
		f1:        fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f1")),
		f2:        fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f2")),
		cPostage:  rapid.SampledFrom([]string{noPostage, postagePoW}).Draw(t, "cPostage"),
		attI:      rapid.IntRange(0, 1).Draw(t, "attI"),
		attKind:   rapid.IntRange(0, numAtts-1).Draw(t, "att"),
		attT:      rapid.SampledFrom([]string{"R", "OTHER_R"}).Draw(t, "attT"),
		attF:      rapid.SampledFrom([]string{"apid", "kube-api"}).Draw(t, "attF"),
		attG:      rapid.SampledFrom(modelGroups).Draw(t, "attG"),
		attE:      rapid.SampledFrom(modelEndpoints).Draw(t, "attE"),
	})
}

// buildChainScenario is the deterministic body of the model's nearChain
// plus the shared consents and the attenuated twin.
func buildChainScenario(f fixture, p chainParams) chainScenario {
	id := f.id
	flt := map[cfault]bool{p.cf1: true, p.cf2: true}
	facets := []string{"apid", "kube-api"}
	verb := p.verb
	if verb == "" {
		verb = VerbInvoke
	}
	consents := buildConsents(f, map[fault]bool{p.f1: true, p.f2: true}, p.cPostage, verb)

	// postage placement: on link1 by fault; on link2 by fault or for the
	// `*` kind (unless FStarNoPostage); conflicting value by fault.
	p1 := noPostage
	if hasC(flt, cfLink1Postage) {
		p1 = postagePoW
	}
	p2 := noPostage
	if hasC(flt, cfLink2Postage) || (p.kind == kindStar && !hasC(flt, cfStarNoPostage)) {
		p2 = postagePoW
	}
	// Deviation from the model's nearChain (generator only, not a law):
	// there the conflict fault needs two more faults to bite (postage on
	// link1 AND on link2 / the `*` kind), so invPostageConflictRejects'
	// antecedent is hit ~1 in 3000 near-valid draws. Here the one fault
	// puts disagreeing postage on both links so the law is not vacuous.
	if hasC(flt, cfPostageConflict) {
		p1, p2 = postagePoW, postagePay
	}

	// link1: OWNER1 (or HUB_A) → OWNER2, delegable.
	l1Iss := "OWNER1"
	if p.hubSigned {
		l1Iss = "HUB_A"
	}
	if hasC(flt, cfLink1IssRogue) {
		l1Iss = "ROGUE"
	}
	l1Aud := id["OWNER2"]
	if hasC(flt, cfLink1AudRogue) {
		l1Aud = id["ROGUE"]
	}
	l1Target := []ActorID{id["R"]}
	if hasC(flt, cfLink1TargetOtherR) {
		l1Target = []ActorID{id["OTHER_R"]}
	}
	l1Facet := facets
	if hasC(flt, cfLink1FacetKubeOnly) {
		l1Facet = []string{"kube-api"}
	}
	l1Ep := modelEndpoints
	if hasC(flt, cfLink1EndpointsA) {
		l1Ep = []string{"quic:a"}
	}
	l1Exp := int64(10)
	if hasC(flt, cfLink1Expired) {
		l1Exp = testNOW
	}
	l1Can := verb
	if hasC(flt, cfLink1VerbOtherChain) {
		l1Can = otherChainVerb(verb)
	}
	l1 := certSpec{iss: l1Iss, aud: string(l1Aud), can: l1Can,
		cav: Caveats{Target: l1Target, Facet: l1Facet, Delegable: !hasC(flt, cfLink1NotDelegable),
			Endpoints: l1Ep, Postage: p1},
		exp: l1Exp, forged: hasC(flt, cfLink1Forged), unknown: hasC(flt, cfLink1Unknown)}

	// link2: OWNER2 (or HUB_B) → aud by kind, not delegable.
	l2Iss := "OWNER2"
	if p.hubSigned {
		l2Iss = "HUB_B"
	}
	if hasC(flt, cfLink2IssRogue) {
		l2Iss = "ROGUE"
	}
	var l2Aud string
	switch p.kind {
	case kindStar:
		l2Aud = AudAny
	case kindGroup:
		l2Aud = "group:admins"
	default:
		l2Aud = string(id["CALLER"])
		if hasC(flt, cfLink2AudRogue) {
			l2Aud = string(id["ROGUE"])
		}
	}
	l2Can := verb
	if hasC(flt, cfLink2VerbOther) {
		l2Can = VerbRelay // OtherVerb: outside CHAIN_VERBS
	}
	l2Facet := []string{"apid"}
	if hasC(flt, cfLink2FacetKubeOnly) {
		l2Facet = []string{"kube-api"}
	}
	l2Ep := modelEndpoints
	if hasC(flt, cfLink2EndpointsB) {
		l2Ep = []string{"quic:b"}
	}
	l2Exp := int64(10)
	if hasC(flt, cfLink2Expired) {
		l2Exp = testNOW
	}
	l2 := certSpec{iss: l2Iss, aud: l2Aud, can: l2Can,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: l2Facet, Endpoints: l2Ep, Postage: p2},
		exp: l2Exp, forged: hasC(flt, cfLink2Forged), unknown: hasC(flt, cfLink2Unknown)}

	var chainSpecs []certSpec
	switch {
	case p.kind == kindConsentOnly:
	case hasC(flt, cfDropLink1):
		chainSpecs = []certSpec{l2}
	default:
		chainSpecs = []certSpec{l1, l2}
	}

	// speak-as: hub links (hub-signed only) and the aud-side CALLER→CALLER_HOT (kindSpeakAs only).
	var saSpecs []certSpec
	if p.hubSigned {
		if !hasC(flt, cfHubANoSpeakAs) {
			saSpecs = append(saSpecs, certSpec{iss: "OWNER1", aud: string(id["HUB_A"]), can: VerbSpeakAs,
				cav: Caveats{Verbs: []string{string(verb)}}, exp: 10})
		}
		hubBVerbs := []string{string(verb)}
		if hasC(flt, cfHubBSpeakAsVerbless) {
			hubBVerbs = []string{"member"}
		}
		hubBExp := int64(10)
		if hasC(flt, cfHubBSpeakAsExpired) {
			hubBExp = testNOW
		}
		saSpecs = append(saSpecs, certSpec{iss: "OWNER2", aud: string(id["HUB_B"]), can: VerbSpeakAs,
			cav: Caveats{Verbs: hubBVerbs}, exp: hubBExp})
	}
	if p.kind == kindSpeakAs && !hasC(flt, cfAudSpeakAsMissing) {
		audIss := "CALLER"
		if hasC(flt, cfAudSpeakAsFromRogue) {
			audIss = "ROGUE"
		}
		audVerbs := []string{string(verb)}
		if hasC(flt, cfAudSpeakAsNoVerb) {
			audVerbs = []string{"member"}
		}
		audExp := int64(10)
		if hasC(flt, cfAudSpeakAsExpired) {
			audExp = testNOW
		}
		saSpecs = append(saSpecs, certSpec{iss: audIss, aud: string(id["CALLER_HOT"]), can: VerbSpeakAs,
			cav: Caveats{Verbs: audVerbs}, exp: audExp, forged: hasC(flt, cfAudSpeakAsForged)})
	}

	// signer by kind.
	var signer ActorID
	switch p.kind {
	case kindKey:
		signer = id["CALLER"]
	case kindSpeakAs:
		signer = id["CALLER_HOT"]
	case kindStar:
		signer = id["ROGUE"]
	case kindGroup:
		signer = id["CALLER"]
	case kindConsentOnly:
		signer = id["OWNER1"]
	}
	if hasC(flt, cfSignerRogue) && p.kind != kindStar && p.kind != kindGroup {
		signer = id["ROGUE"]
	}

	// build, then the attenuated twin: chain link attI re-signed with one
	// caveat added (addChainCaveat), every speak-as with one added
	// (addSpeakAsCaveat); a no-op when the kind does not apply.
	chain := make([]Cert, 0, len(chainSpecs))
	chainAtt := make([]Cert, 0, len(chainSpecs))
	for i, sp := range chainSpecs {
		chain = append(chain, f.build(sp))
		ap := sp
		if i == p.attI {
			ap.cav = Caveats{
				Target:    append([]ActorID(nil), sp.cav.Target...),
				Facet:     append([]string(nil), sp.cav.Facet...),
				Endpoints: append([]string(nil), sp.cav.Endpoints...),
				Delegable: sp.cav.Delegable,
				Postage:   sp.cav.Postage,
			}
			switch p.attKind {
			case attShrinkTarget:
				ap.cav.Target = removeID(ap.cav.Target, id[p.attT])
			case attShrinkFacet:
				ap.cav.Facet = removeStr(ap.cav.Facet, p.attF)
			case attShrinkEndpoints:
				ap.cav.Endpoints = removeStr(ap.cav.Endpoints, p.attE)
			case attAddUnknownCaveat:
				ap.unknown = true
			case attShortenExpiry:
				ap.exp = 0
			}
		}
		chainAtt = append(chainAtt, f.build(ap))
	}
	speakAs := make([]Cert, 0, len(saSpecs))
	speakAsAtt := make([]Cert, 0, len(saSpecs))
	for _, sp := range saSpecs {
		speakAs = append(speakAs, f.build(sp))
		ap := sp
		ap.cav = Caveats{
			Verbs:  append([]string(nil), sp.cav.Verbs...),
			Groups: append([]string(nil), sp.cav.Groups...),
		}
		switch p.attKind {
		case attShrinkSpeakAsGroups:
			ap.cav.Groups = removeStr(ap.cav.Groups, p.attG)
		case attShrinkSpeakAsVerbs:
			ap.cav.Verbs = removeStr(ap.cav.Verbs, "member")
		case attShortenSpeakAsExpiry:
			ap.exp = 0
		}
		speakAsAtt = append(speakAsAtt, f.build(ap))
	}

	return chainScenario{p: p, consents: consents, chain: chain, chainAtt: chainAtt,
		speakAs: speakAs, speakAsAtt: speakAsAtt, signer: signer, facet: "apid", verb: verb, f: flt}
}

// chainResult is the model's chainRes: the verdict SET, plus the public
// VerifyChain's answer for the agreement check.
type chainResult struct {
	verdicts []chainVerdict
	err      error
	pubEff   Cert
	pubErr   error
}

// --- laws ---------------------------------------------------------------

type chainLaw struct {
	name string
	// check returns (antecedentHit, ok).
	check func(id map[string]ActorID, s chainScenario, res, resAtt chainResult) (bool, bool)
}

func (r chainResult) accepted() bool { return len(r.verdicts) > 0 }

// links is the model's links(v): [consent] ++ chain.
func links(v chainVerdict, chain []Cert) []Cert {
	return append([]Cert{v.consent}, chain...)
}

func lastOf(l []Cert) Cert { return l[len(l)-1] }

// isHotKey is the model's HOT_KEYS.contains(k).
func isHotKey(id map[string]ActorID, k ActorID) bool {
	return k == id["HUB_A"] || k == id["HUB_B"] || k == id["CALLER_HOT"]
}

// chainAttributable ports the model's chainAttributable (loosest reading).
func chainAttributable(speakAs []Cert, k ActorID) []ActorID { return attributable(speakAs, k) }

// liveChainSpeakAs ports the model's liveChainSpeakAs (raw fields): a
// live speak-as S→k covering the chain's verb.
func liveChainSpeakAs(speakAs []Cert, s, k ActorID, verb Verb) bool {
	for _, sp := range speakAs {
		if sp.Iss == s && sp.Aud == string(k) && sp.Can == VerbSpeakAs && sigOKlaw(sp) &&
			containsStr(sp.Cav.Verbs, string(verb)) {
			return true
		}
	}
	return false
}

func sameStrSet(a, b []string) bool { return subset(a, b) && subset(b, a) }

func anyPostage(certs []Cert) bool {
	for _, c := range certs {
		if c.Cav.Postage != noPostage {
			return true
		}
	}
	return false
}

var chainLaws = []chainLaw{
	// Rule 1 — every verdict is rooted in one of R's OWN consents that
	// verifies and carries the verb R expects; with none such, nothing
	// verifies.
	{"invChainRootIsReceiverConsent", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, v := range res.verdicts {
			found := false
			for _, c := range s.consents {
				if certKey(c) == certKey(v.consent) {
					found = true
				}
			}
			if !found || v.consent.Iss != id["R"] || !sigValid(v.consent) || v.consent.Can != s.verb {
				return true, false
			}
		}
		anyRoot := false
		for _, c := range s.consents {
			if c.Iss == id["R"] && sigValid(c) {
				anyRoot = true
			}
		}
		if !anyRoot {
			return true, !res.accepted()
		}
		return res.accepted(), true
	}},
	// Rule 2 — linkage: each link's signer is attributable to the key the
	// previous link's aud names. Corollary: group / "*" is never followed.
	{"invChainLinked", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		hit := false
		for _, v := range res.verdicts {
			ls := links(v, s.chain)
			for i := 1; i < len(ls); i++ {
				hit = true
				ok := false
				for _, p := range chainAttributable(s.speakAs, ls[i].Iss) {
					if ls[i-1].Aud == string(p) {
						ok = true
					}
				}
				if !ok {
					return true, false
				}
			}
		}
		return hit, true
	}},
	// Receiver-rooted survives every link: a chain link signed by a hot
	// key with no live speak-as to it covering the chain's verb admits
	// nothing.
	{"invChainHotKeyNeedsSpeakAs", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, l := range s.chain {
			if !isHotKey(id, l.Iss) {
				continue
			}
			vouched := false
			for _, sp := range s.speakAs {
				if sp.Aud == string(l.Iss) && sigValid(sp) && sp.Exp > testNOW && sp.Can == VerbSpeakAs &&
					!sp.Cav.Unknown && containsStr(sp.Cav.Verbs, string(s.verb)) {
					vouched = true
				}
			}
			if !vouched {
				return true, !res.accepted()
			}
		}
		return false, true
	}},
	// Rule 3 — the last link's aud binds the signer: the signer's key, a
	// principal S with a live speak-as S→signer covering the chain's verb,
	// or "*" with postage somewhere on the chain. A group audience is only
	// ever the sentinel, never bound.
	{"invChainAudBindsSigner", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, v := range res.verdicts {
			last := lastOf(links(v, s.chain))
			grp, isGroup := trimGroup(last.Aud)
			if v.group != "" {
				if !isGroup || grp != v.group {
					return true, false
				}
				continue
			}
			switch {
			case isGroup:
				return true, false
			case last.Aud == AudAny:
				if !anyPostage(links(v, s.chain)) {
					return true, false
				}
			default:
				if last.Aud != string(s.signer) && !liveChainSpeakAs(s.speakAs, ActorID(last.Aud), s.signer, s.verb) {
					return true, false
				}
			}
		}
		return res.accepted(), true
	}},
	// "*" is fail-closed: without postage on any link it binds nobody.
	{"invStarFailsClosedWithoutPostage", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		if len(s.chain) > 0 && lastOf(s.chain).Aud == AudAny && !anyPostage(s.chain) && !anyPostage(s.consents) {
			return true, !res.accepted()
		}
		return false, true
	}},
	// Aud-side speak-as is verb-scoped: aud: S presented by another key
	// needs a speak-as S→signer that is live and covers the chain's verb.
	{"invAudSpeakAsCoversVerb", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		if len(s.chain) == 0 {
			return false, true
		}
		aud := lastOf(s.chain).Aud
		if _, isGroup := trimGroup(aud); isGroup || aud == AudAny {
			return false, true
		}
		if aud != string(s.signer) && !liveChainSpeakAs(s.speakAs, ActorID(aud), s.signer, s.verb) {
			return true, !res.accepted()
		}
		return false, true
	}},
	// Attenuation only (invariant 5): the effective cert is below every
	// link in every recognised caveat and in expiry.
	{"invEffectiveIsIntersection", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, v := range res.verdicts {
			for _, l := range links(v, s.chain) {
				if !subsetID(v.eff.Cav.Target, l.Cav.Target) || !subset(v.eff.Cav.Facet, l.Cav.Facet) ||
					!subset(v.eff.Cav.Groups, l.Cav.Groups) || !subset(v.eff.Cav.Endpoints, l.Cav.Endpoints) ||
					v.eff.Exp > l.Exp {
					return true, false
				}
			}
		}
		return res.accepted(), true
	}},
	// Caveat v2 — endpoints attenuate by exact intersection over the chain.
	{"invEndpointsIntersect", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, v := range res.verdicts {
			acc := modelEndpoints
			for _, l := range links(v, s.chain) {
				acc = intersectStr(acc, l.Cav.Endpoints)
			}
			if !sameStrSet(acc, v.eff.Cav.Endpoints) {
				return true, false
			}
		}
		return res.accepted(), true
	}},
	// Caveat v2 — postage is monotone: present on any link ⇔ present on
	// the effective cert, and equal to every link that set it.
	{"invPostageMonotone", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, v := range res.verdicts {
			ls := links(v, s.chain)
			if anyPostage(ls) != (v.eff.Cav.Postage != noPostage) {
				return true, false
			}
			for _, l := range ls {
				if l.Cav.Postage != noPostage && l.Cav.Postage != v.eff.Cav.Postage {
					return true, false
				}
			}
		}
		return res.accepted(), true
	}},
	// Two caller links that both set postage and disagree reject.
	{"invPostageConflictRejects", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, a := range s.chain {
			for _, b := range s.chain {
				if a.Cav.Postage != noPostage && b.Cav.Postage != noPostage && a.Cav.Postage != b.Cav.Postage {
					return true, !res.accepted()
				}
			}
		}
		return false, true
	}},
	// Fail closed per link.
	{"invChainForgedLinkRejects", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, l := range s.chain {
			if !sigValid(l) {
				return true, !res.accepted()
			}
		}
		return false, true
	}},
	{"invChainExpiredLinkRejects", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		hit := false
		for _, l := range s.chain {
			if l.Exp <= testNOW {
				hit = true
				if res.accepted() {
					return true, false
				}
			}
		}
		allExpired := true
		for _, c := range s.consents {
			if c.Exp > testNOW {
				allExpired = false
			}
		}
		if allExpired {
			return true, !res.accepted()
		}
		return hit, true
	}},
	{"invChainUnknownCaveatRejects", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		hit := false
		for _, l := range s.chain {
			if l.Cav.Unknown {
				hit = true
				if res.accepted() {
					return true, false
				}
			}
		}
		allUnknown := true
		for _, c := range s.consents {
			if !c.Cav.Unknown {
				allUnknown = false
			}
		}
		if allUnknown {
			return true, !res.accepted()
		}
		return hit, true
	}},
	// Verb = the root consent's (xwu): a caller link carrying any verb
	// other than the one R expects rejects — an invoke link under a publish
	// root as much as an unknown verb …
	{"invChainVerbIsRoot", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, l := range s.chain {
			if l.Can != s.verb {
				return true, !res.accepted()
			}
		}
		return false, true
	}},
	// … and on every verdict the whole chain, root included, carries that
	// one verb.
	{"invChainVerbUniform", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		for _, v := range res.verdicts {
			for _, l := range links(v, s.chain) {
				if l.Can != s.verb {
					return true, false
				}
			}
		}
		return res.accepted(), true
	}},
	// delegable:false admits no following link — on a caller link or on
	// the consent itself.
	{"invChainNotDelegableStops", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		hit := false
		for i, l := range s.chain {
			if i < len(s.chain)-1 && !l.Cav.Delegable {
				hit = true
				if res.accepted() {
					return true, false
				}
			}
		}
		if len(s.chain) > 0 {
			allNonDeleg := true
			for _, c := range s.consents {
				if c.Cav.Delegable {
					allNonDeleg = false
				}
			}
			if allNonDeleg {
				return true, !res.accepted()
			}
		}
		return hit, true
	}},
	// Rule 4 — target and facet: every link must name R and the facet.
	{"invChainTargetsReceiver", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		if !res.accepted() {
			return false, true
		}
		for _, l := range s.chain {
			if !containsID(l.Cav.Target, id["R"]) {
				return true, false
			}
		}
		anyConsent := false
		for _, c := range s.consents {
			if containsID(c.Cav.Target, id["R"]) {
				anyConsent = true
			}
		}
		return true, anyConsent
	}},
	{"invChainFacetMatches", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		if !res.accepted() {
			return false, true
		}
		for _, l := range s.chain {
			if !containsStr(l.Cav.Facet, s.facet) {
				return true, false
			}
		}
		anyConsent := false
		for _, c := range s.consents {
			if containsStr(c.Cav.Facet, s.facet) {
				anyConsent = true
			}
		}
		return true, anyConsent
	}},
	// Monotone under attenuation: adding a caveat (other than postage) to
	// any chain link or speak-as can only shrink acceptance.
	{"invChainMonotone", func(id map[string]ActorID, s chainScenario, res, resAtt chainResult) (bool, bool) {
		if resAtt.accepted() {
			return true, res.accepted()
		}
		return false, true
	}},
	// Public/internal agreement (not a model law; pins the Go API): the
	// exported VerifyChain accepts ⇔ the verdict set is non-empty, returns
	// ErrGroupAud exactly when every verdict is the group sentinel, and
	// its eff is one of the set's.
	{"publicAgreesWithVerdictSet", func(id map[string]ActorID, s chainScenario, res, _ chainResult) (bool, bool) {
		if !res.accepted() {
			return false, res.pubErr != nil && !errors.Is(res.pubErr, ErrGroupAud) && errors.Is(res.err, res.pubErr)
		}
		allGroup := true
		for _, v := range res.verdicts {
			if v.group == "" {
				allGroup = false
			}
		}
		if allGroup != errors.Is(res.pubErr, ErrGroupAud) {
			return true, false
		}
		if !allGroup && res.pubErr != nil {
			return true, false
		}
		for _, v := range res.verdicts {
			if certKey(v.eff) == certKey(res.pubEff) {
				return true, true
			}
		}
		return true, false
	}},
}

func subsetID(need, have []ActorID) bool {
	for _, x := range need {
		if !containsID(have, x) {
			return false
		}
	}
	return true
}

func evalChain(id map[string]ActorID, s chainScenario, chain, speakAs []Cert) chainResult {
	ctx := newAuthCtx()
	vs, err := ctx.verifyChain(id["R"], s.verb, s.consents, chain, speakAs, s.signer, s.facet, testNOW)
	eff, _, pubErr := VerifyChain(id["R"], s.verb, s.consents, chain, speakAs, s.signer, s.facet, testNOW)
	return chainResult{verdicts: vs, err: err, pubEff: eff, pubErr: pubErr}
}

func checkChainLaws(t failer, id map[string]ActorID, s chainScenario, hits map[string]int) {
	res := evalChain(id, s, s.chain, s.speakAs)
	resAtt := evalChain(id, s, s.chainAtt, s.speakAsAtt)
	for _, l := range chainLaws {
		hit, ok := l.check(id, s, res, resAtt)
		if !ok {
			t.Fatalf("%s violated (%+v; err=%v)", l.name, s.p, res.err)
		}
		if hit && hits != nil {
			hits[l.name]++
		}
	}
}

// TestChainLaws is the rapid suite: one property per law, same names as
// the model.
func TestChainLaws(t *testing.T) {
	for _, l := range chainLaws {
		l := l
		t.Run(l.name, func(t *testing.T) {
			rapid.Check(t, func(t *rapid.T) {
				f := newFixture(t)
				s := genChain(t, f)
				res := evalChain(f.id, s, s.chain, s.speakAs)
				resAtt := evalChain(f.id, s, s.chainAtt, s.speakAsAtt)
				if _, ok := l.check(f.id, s, res, resAtt); !ok {
					t.Fatalf("%s violated (%+v; err=%v)", l.name, s.p, res.err)
				}
			})
		})
	}
}

// TestChainFaultPairSweep enumerates nearChain's fault space EXHAUSTIVELY
// — every unordered chain-fault pair × kind × hubSigned (consents
// fault-free, the consent faults being the connection-level sweep's
// job) — the deterministic backbone behind the random suite.
func TestChainFaultPairSweep(t *testing.T) {
	f := detFixture(60)
	n := 0
	for c1 := cfault(0); c1 < numCFaults; c1++ {
		for c2 := c1; c2 < numCFaults; c2++ {
			for kind := chainKind(0); kind < numKinds; kind++ {
				for _, hub := range []bool{false, true} {
					for _, verb := range chainVerbs {
						p := chainParams{cf1: c1, cf2: c2, kind: kind, verb: verb, hubSigned: hub,
							attI: n % 2, attKind: n % numAtts, attT: "R", attF: "apid",
							attG: modelGroups[n%len(modelGroups)], attE: modelEndpoints[n%len(modelEndpoints)]}
						checkChainLaws(sweepCT{t, p}, f.id, buildChainScenario(f, p), nil)
						n++
					}
				}
			}
		}
	}
	t.Logf("swept %d chain scenarios", n)
}

type sweepCT struct {
	t *testing.T
	p chainParams
}

func (s sweepCT) Fatal(args ...any) { s.t.Fatalf("%+v: %s", s.p, fmt.Sprint(args...)) }
func (s sweepCT) Fatalf(format string, args ...any) {
	s.t.Fatalf("%+v: %s", s.p, fmt.Sprintf(format, args...))
}

// TestGenChainReachesAccept proves the chain generator is not vacuous:
// every kind reaches Accept (group kind: the sentinel), and every law's
// antecedent is hit a meaningful number of times.
func TestGenChainReachesAccept(t *testing.T) {
	const samples = 3000
	const minHits = 5
	f := detFixture(70)
	gen := rapid.Custom(func(t *rapid.T) chainScenario { return genChain(t, f) })
	accepts := map[chainKind]int{}
	hits := map[string]int{}
	for i := 0; i < samples; i++ {
		s := gen.Example(i)
		res := evalChain(f.id, s, s.chain, s.speakAs)
		if res.accepted() {
			accepts[s.p.kind]++
		}
		checkChainLaws(t, f.id, s, hits)
	}
	for kind := chainKind(0); kind < numKinds; kind++ {
		t.Logf("kind %d accepted %d times", kind, accepts[kind])
		if accepts[kind] < minHits {
			t.Errorf("kind %d accepted only %d times (< %d) — laws would hold vacuously", kind, accepts[kind], minHits)
		}
	}
	for _, l := range chainLaws {
		t.Logf("%-34s antecedent hit %d times", l.name, hits[l.name])
		if hits[l.name] < minHits {
			t.Errorf("%s: antecedent hit only %d times (< %d)", l.name, hits[l.name], minHits)
		}
	}
}

// --- witnesses (the model's run tests) -----------------------------------

// chainHappyPath is the model's init chain: consent(R→OWNER1) ·
// LINK1 (HUB_A→OWNER2, delegable, both endpoints) · LINK2 (HUB_B→CALLER,
// apid only, quic:a only), presented by CALLER_HOT under speak-as
// CALLER→CALLER_HOT; speak-as OWNER1→HUB_A and OWNER2→HUB_B.
func chainHappyPath(f fixture) (consent Cert, chain, speakAs []Cert) {
	id := f.id
	consent = f.build(certSpec{iss: "R", aud: string(id["OWNER1"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid", "kube-api"}, Delegable: true, Endpoints: modelEndpoints}, exp: 10})
	link1 := f.build(certSpec{iss: "HUB_A", aud: string(id["OWNER2"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid", "kube-api"}, Delegable: true, Endpoints: modelEndpoints}, exp: 10})
	link2 := f.build(certSpec{iss: "HUB_B", aud: string(id["CALLER"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Endpoints: []string{"quic:a"}}, exp: 10})
	sa := func(iss, aud string, verbs []string) Cert {
		return f.build(certSpec{iss: iss, aud: string(id[aud]), can: VerbSpeakAs, cav: Caveats{Verbs: verbs}, exp: 10})
	}
	return consent, []Cert{link1, link2}, []Cert{sa("OWNER1", "HUB_A", []string{"invoke"}), sa("OWNER2", "HUB_B", []string{"invoke"}), sa("CALLER", "CALLER_HOT", []string{"invoke"})}
}

// TestVerifyChainHappyPath is the model's chainHappyPathTest.
func TestVerifyChainHappyPath(t *testing.T) {
	f := detFixture(80)
	id := f.id
	consent, chain, speakAs := chainHappyPath(f)
	eff, verified, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, chain, speakAs, id["CALLER_HOT"], "apid", testNOW)
	if err != nil {
		t.Fatalf("3-link hub-signed chain rejected: %v", err)
	}
	if !sameStrSet(eff.Cav.Endpoints, []string{"quic:a"}) || !sameStrSet(eff.Cav.Facet, []string{"apid"}) ||
		len(eff.Cav.Target) != 1 || eff.Cav.Target[0] != id["R"] || eff.Exp != 10 || eff.Aud != string(id["CALLER"]) {
		t.Fatalf("effective cert is not the intersection: %+v", eff.Cav)
	}
	// verified: consent, both hub speak-as, both links (rooted along the
	// chain); the aud-side speak-as CALLER→CALLER_HOT is NOT rooted at R.
	if len(verified) != 5 {
		t.Fatalf("verified = %d certs, want 5 (consent, 2 speak-as, 2 links): %v", len(verified), verifiedIss(verified))
	}
	for _, v := range verified {
		if v.Iss == id["CALLER"] {
			t.Fatal("aud-side speak-as from an unrooted principal counted as rooted")
		}
	}
	// tainting the last link rejects.
	tainted := append([]Cert(nil), chain...)
	tainted[1].Cav.Unknown = true
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, tainted, speakAs, id["CALLER_HOT"], "apid", testNOW); !errors.Is(err, ErrUnknownCaveat) {
		t.Fatalf("tainted last link: err = %v, want ErrUnknownCaveat", err)
	} else if errors.Is(err, ErrPostageConflict) {
		t.Fatalf("plain unknown-caveat taint: err = %v, want NOT ErrPostageConflict", err)
	}
	// the same chain presented by a key nobody vouches for is unbound.
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, chain, speakAs, id["ROGUE"], "apid", testNOW); !errors.Is(err, ErrAudUnbound) {
		t.Fatalf("ROGUE presenting: err = %v, want ErrAudUnbound", err)
	}
	// and with the aud-side speak-as lacking invoke, too.
	noInvoke := append([]Cert(nil), speakAs[:2]...)
	noInvoke = append(noInvoke, f.build(certSpec{iss: "CALLER", aud: string(id["CALLER_HOT"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: []string{"member"}}, exp: 10}))
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, chain, noInvoke, id["CALLER_HOT"], "apid", testNOW); !errors.Is(err, ErrAudUnbound) {
		t.Fatalf("speak-as without invoke: err = %v, want ErrAudUnbound", err)
	}
	// a fourth link after the non-delegable LINK2 cannot follow.
	link3 := f.build(certSpec{iss: "CALLER", aud: string(id["ROGUE"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Endpoints: modelEndpoints}, exp: 10})
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, append(append([]Cert(nil), chain...), link3), speakAs, id["ROGUE"], "apid", testNOW); !errors.Is(err, ErrNotDelegable) {
		t.Fatalf("link after delegable:false: err = %v, want ErrNotDelegable", err)
	}
}

func verifiedIss(cs []Cert) []ActorID {
	out := make([]ActorID, len(cs))
	for i, c := range cs {
		out[i] = c.Iss
	}
	return out
}

// TestVerifyChainStarPostage is the model's starPostageTest: aud "*"
// binds anyone when postage is present and nobody when it is not; a
// conflicting postage on the consent taints; the connection-level check
// binds "*" the same way.
func TestVerifyChainStarPostage(t *testing.T) {
	f := detFixture(90)
	id := f.id
	consent := func(postage string) Cert {
		return f.build(certSpec{iss: "R", aud: string(id["OWNER1"]), can: VerbInvoke,
			cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid", "kube-api"}, Delegable: true,
				Endpoints: modelEndpoints, Postage: postage}, exp: 10})
	}
	star := func(postage string) Cert {
		return f.build(certSpec{iss: "OWNER1", aud: AudAny, can: VerbInvoke,
			cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Endpoints: modelEndpoints, Postage: postage}, exp: 10})
	}
	eff, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent("")}, []Cert{star(postagePoW)}, nil, id["ROGUE"], "apid", testNOW)
	if err != nil || eff.Cav.Postage != postagePoW {
		t.Fatalf("* with postage presented by ROGUE: err=%v eff.postage=%q", err, eff.Cav.Postage)
	}
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent("")}, []Cert{star("")}, nil, id["ROGUE"], "apid", testNOW); !errors.Is(err, ErrAudUnbound) {
		t.Fatalf("* without postage: err = %v, want ErrAudUnbound", err)
	}
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent(postagePay)}, []Cert{star(postagePoW)}, nil, id["ROGUE"], "apid", testNOW); !errors.Is(err, ErrPostageConflict) {
		t.Fatalf("conflicting postage: err = %v, want ErrPostageConflict", err)
	} else if !errors.Is(err, ErrUnknownCaveat) {
		t.Fatalf("conflicting postage: err = %v, ErrPostageConflict must remain an ErrUnknownCaveat (taint)", err)
	}
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent(postagePoW)}, []Cert{star(postagePoW)}, nil, id["ROGUE"], "apid", testNOW); err != nil {
		t.Fatalf("agreeing postage: %v", err)
	}
	// postage on the consent alone unlocks a "*" link without its own.
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent(postagePoW)}, []Cert{star("")}, nil, id["ROGUE"], "apid", testNOW); err != nil {
		t.Fatalf("postage carried from the consent: %v", err)
	}
	// connection level: the same rule on a "*" grant.
	member := f.build(certSpec{iss: "OWNER1", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 10})
	in := Input{Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: []Cert{consent("")}, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"], Bundle: Bundle{Member: member, Grants: []Cert{star(postagePoW)}}}
	if !Authorize(in).OK {
		t.Fatal("Authorize rejected a * grant with postage")
	}
	in.Bundle.Grants = []Cert{star("")}
	if Authorize(in).OK {
		t.Fatal("Authorize accepted a * grant without postage")
	}
}

// TestVerifyChainConsentOnly is the model's consentOnlyTest: an empty
// caller chain is the consented sovereign presenting R's consent itself
// — it binds as the consent's aud (or via its hot key), never to a
// stranger; the effective cert is the consent.
func TestVerifyChainConsentOnly(t *testing.T) {
	f := detFixture(100)
	id := f.id
	consent := f.build(certSpec{iss: "R", aud: string(id["OWNER1"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid", "kube-api"}, Delegable: true, Endpoints: modelEndpoints}, exp: 10})
	eff, verified, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, nil, nil, id["OWNER1"], "kube-api", testNOW)
	if err != nil {
		t.Fatalf("consent-only chain rejected: %v", err)
	}
	if certKey(eff) != certKey(consent) {
		t.Fatal("effective cert of an empty chain is not the consent")
	}
	if len(verified) != 1 {
		t.Fatalf("verified = %d, want 1 (the consent)", len(verified))
	}
	hub := f.build(certSpec{iss: "OWNER1", aud: string(id["HUB_A"]), can: VerbSpeakAs, cav: Caveats{Verbs: []string{"invoke"}}, exp: 10})
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, nil, []Cert{hub}, id["HUB_A"], "kube-api", testNOW); err != nil {
		t.Fatalf("consent presented by OWNER1's hot key: %v", err)
	}
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, nil, nil, id["OWNER2"], "kube-api", testNOW); !errors.Is(err, ErrAudUnbound) {
		t.Fatalf("consent presented by a stranger: err = %v, want ErrAudUnbound", err)
	}
	if _, _, err := VerifyChain(id["R"], VerbInvoke, nil, nil, nil, id["OWNER1"], "kube-api", testNOW); !errors.Is(err, ErrChainUnrooted) {
		t.Fatalf("no consents: err = %v, want ErrChainUnrooted", err)
	}
}

// TestVerifyChainPublish is the model's publishChainTest (xwu): the
// happy-path shape with every link and speak-as carrying `publish`
// under a `publish` consent verifies for a receiver expecting `publish`,
// rooted in that consent only; the same links do not verify as `invoke`
// (no root); an `invoke` link under the `publish` root is ErrChainVerb;
// speak-as covering only `invoke` vouch for nothing; and Authorize stays
// invoke-only — a `publish` grant admits no connection even beside a
// `publish` consent.
func TestVerifyChainPublish(t *testing.T) {
	f := detFixture(120)
	id := f.id
	consentInv, chainInv, speakAsInv := chainHappyPath(f)
	// asVerb re-signs c (issued by the named principal) under another verb.
	asVerb := func(iss string, c Cert, v Verb) Cert {
		return f.build(certSpec{iss: iss, aud: c.Aud, can: v, cav: c.Cav, exp: c.Exp})
	}
	consentPub := asVerb("R", consentInv, VerbPublish)
	chainPub := []Cert{asVerb("HUB_A", chainInv[0], VerbPublish), asVerb("HUB_B", chainInv[1], VerbPublish)}
	sa := func(iss, aud string, verbs ...string) Cert {
		return f.build(certSpec{iss: iss, aud: string(id[aud]), can: VerbSpeakAs, cav: Caveats{Verbs: verbs}, exp: 10})
	}
	speakAsPub := []Cert{sa("OWNER1", "HUB_A", "publish"), sa("OWNER2", "HUB_B", "publish"), sa("CALLER", "CALLER_HOT", "publish")}
	consents := []Cert{consentInv, consentPub}

	eff, _, err := VerifyChain(id["R"], VerbPublish, consents, chainPub, speakAsPub, id["CALLER_HOT"], "apid", testNOW)
	if err != nil || eff.Can != VerbPublish {
		t.Fatalf("publish chain as publish: err=%v eff.can=%q", err, eff.Can)
	}
	ctx := newAuthCtx()
	vs, _ := ctx.verifyChain(id["R"], VerbPublish, consents, chainPub, speakAsPub, id["CALLER_HOT"], "apid", testNOW)
	for _, v := range vs {
		if certKey(v.consent) != certKey(consentPub) {
			t.Fatalf("publish chain rooted in a %s consent", v.consent.Can)
		}
	}
	// the same links as an invoke chain: no root has the chain's verb.
	if _, _, err := VerifyChain(id["R"], VerbInvoke, consents, chainPub, speakAsPub, id["CALLER_HOT"], "apid", testNOW); !errors.Is(err, ErrChainVerb) {
		t.Fatalf("publish links under the invoke root: err = %v, want ErrChainVerb", err)
	}
	// an invoke link under the publish root: verb differs from the root's.
	mixed := []Cert{chainPub[0], chainInv[1]}
	if _, _, err := VerifyChain(id["R"], VerbPublish, consents, mixed, speakAsPub, id["CALLER_HOT"], "apid", testNOW); !errors.Is(err, ErrChainVerb) {
		t.Fatalf("invoke link under the publish root: err = %v, want ErrChainVerb", err)
	}
	// speak-as covering invoke only: the hub links do not resolve (rule 2).
	if _, _, err := VerifyChain(id["R"], VerbPublish, consents, chainPub, speakAsInv, id["CALLER_HOT"], "apid", testNOW); !errors.Is(err, ErrChainLinkage) {
		t.Fatalf("invoke-only speak-as on a publish chain: err = %v, want ErrChainLinkage", err)
	}
	// …and, with only the aud-side speak-as lacking publish, unbound (rule 3).
	audInv := []Cert{speakAsPub[0], speakAsPub[1], speakAsInv[2]}
	if _, _, err := VerifyChain(id["R"], VerbPublish, consents, chainPub, audInv, id["CALLER_HOT"], "apid", testNOW); !errors.Is(err, ErrAudUnbound) {
		t.Fatalf("aud-side speak-as without publish: err = %v, want ErrAudUnbound", err)
	}
	// connection level is invoke-only: a publish grant never admits.
	member := f.build(certSpec{iss: "OWNER1", aud: string(id["CALLER"]), can: VerbMember,
		cav: Caveats{Groups: []string{"admins"}, Name: "laptop"}, exp: 10})
	grantPub := f.build(certSpec{iss: "OWNER1", aud: string(id["CALLER"]), can: VerbPublish,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 10})
	in := Input{Receiver: id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: consents, Blocklist: map[ActorID]bool{}, Now: testNOW,
		ALPN: "mesh/apid/v1", Peer: id["CALLER"], Bundle: Bundle{Member: member, Grants: []Cert{grantPub}}}
	if Authorize(in).OK {
		t.Fatal("Authorize accepted a publish grant beside a publish consent")
	}
}

// TestVerifyChainGroupSentinel: a group audience returns ErrGroupAud
// BESIDE the effective cert (the chain verified; the talos layer resolves).
func TestVerifyChainGroupSentinel(t *testing.T) {
	f := detFixture(110)
	id := f.id
	consent, _, _ := chainHappyPath(f)
	grant := f.build(certSpec{iss: "OWNER1", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 10})
	eff, verified, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, []Cert{grant}, nil, id["CALLER"], "apid", testNOW)
	if !errors.Is(err, ErrGroupAud) {
		t.Fatalf("err = %v, want ErrGroupAud", err)
	}
	if eff.Aud != "group:admins" || len(verified) != 2 {
		t.Fatalf("sentinel without eff/verified: aud=%q verified=%d", eff.Aud, len(verified))
	}
	// a group audience can never be followed (rule 2).
	next := f.build(certSpec{iss: "CALLER", aud: string(id["ROGUE"]), can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}}, exp: 10})
	grantD := f.build(certSpec{iss: "OWNER1", aud: "group:admins", can: VerbInvoke,
		cav: Caveats{Target: []ActorID{id["R"]}, Facet: []string{"apid"}, Delegable: true}, exp: 10})
	if _, _, err := VerifyChain(id["R"], VerbInvoke, []Cert{consent}, []Cert{grantD, next}, nil, id["ROGUE"], "apid", testNOW); !errors.Is(err, ErrChainLinkage) {
		t.Fatalf("link after a group audience: err = %v, want ErrChainLinkage", err)
	}
}
