package cert

import (
	"errors"
	"fmt"
	"strings"
)

// Bundle is what a caller presents on connect: its member cert, the
// invoke grants it holds, and the speak-as certs that map the hot keys
// that signed them to their principals (ADR-0018).
type Bundle struct {
	Member  Cert
	Grants  []Cert
	SpeakAs []Cert
}

// Receiver is everything the receiver itself brings to a chain check —
// its own configuration, never anything the caller presented. Keeping
// it one value makes "receiver-held" a type: the two speak-as sets a
// verifier sees (this one and the caller's bundle) have opposite trust
// roles and must never be conflated (protocol ADR-0003; the model's
// `held` vs `speakAs`).
type Receiver struct {
	// ID is R, the receiver under test.
	ID ActorID
	// Consents are the consent grants R has issued (iss == ID): the
	// roots VerifyChain prepends to every caller chain.
	Consents []Cert
	// SpeakAs are the speak-as certs R HOLDS naming ID as aud — the
	// principals R is a hot key for. Rule 4 lets R answer for each
	// principal P with a live such cert: an effective target naming P
	// admits at R (ADR-0003). Other certs in the slice are ignored, so
	// an actor may pass its whole speak-as set.
	SpeakAs []Cert
}

// Input is the complete, self-contained input to Authorize. The function
// is pure: no I/O, no clock reads. Now is the verifier's EFFECTIVE clock
// max(local, lw) (ADR-0019); the caller computes it from a clock.Mark.
type Input struct {
	Receiver    Receiver          // R's own configuration: id, consents, held speak-as
	AcceptTable map[string]string // ALPN class → facet (producer-owned)
	Blocklist   map[ActorID]bool  // blocked peer keys
	Now         int64             // effective clock
	ALPN        string            // negotiated ALPN class
	Peer        ActorID           // the QUIC peer key
	Bundle      Bundle
}

// Identity is what a successful Authorize attributes the connection to.
// It comes from the member cert ONLY, never from a grant.
type Identity struct {
	Key    ActorID
	Name   string
	Groups []string
}

// Result is the outcome. Verified lists only the certs that both (a) had
// a valid signature and (b) are ROOTED AT THE RECEIVER — R-signed
// consents, speak-as certs whose iss is a principal R has signed a
// delegable consent for, and member/grant certs signed by such a
// principal or by a hot key such a principal vouches for via a
// signature-valid speak-as. Rootedness is SIGNATURE-ONLY PROVENANCE
// (decision 7ry): whether the consent or speak-as is expired at Now is
// irrelevant, and so are the speak-as's cav.verbs / cav.groups (decision
// jo8) — those gate authorization, not provenance. Now never enters the
// rooting path, so there is no now → rooted → lw → now loop. A
// stranger's validly-self-signed cert is NOT included even though its
// signature verifies, because it is not on a chain rooted at R;
// otherwise any peer that can connect could push a caller's clock.Mark
// into the future (denial by strangers, which ADR-0019 excludes).
// Expired-but-rooted certs ARE included: an expired cert from a consented
// issuer still proves its iat passed.
//
// The caller folds these into a clock.Mark via mark.ObserveAll — safe to
// do on both accept and reject, as Verified is populated on both paths.
// ADR-0019's "update first, then judge" holds ACROSS bundles, not within
// one (decision c4c): Authorize judges with the caller's Now, and
// Verified feeds the mark afterwards; the presenter chooses the bundle,
// so omitting fresh certs only weakens their own evidence.
type Result struct {
	OK       bool
	Identity Identity
	Verified []Cert
}

// Chain errors. Every one of them is fail-closed; ErrGroupAud is the
// single non-fatal sentinel (see VerifyChain).
var (
	// ErrNotDelegable is returned by Attenuate when the parent forbids a
	// following link (delegable:false).
	ErrNotDelegable = errors.New("cert: parent is not delegable")
	// ErrVerbMismatch is returned by Attenuate when a child link carries
	// another verb than its parent: a chain is uniform in `can`.
	ErrVerbMismatch = errors.New("cert: child verb differs from parent")
	// ErrGroupAud is returned by VerifyChain BESIDE a verified effective
	// cert when the last link addresses `group:<name>`: the chain itself
	// verified; resolving the group against a member cert is the talos
	// layer's job (Authorize's same-w rule). Protocol-level callers
	// (envelope) treat it as a rejection.
	ErrGroupAud = errors.New("cert: last link addresses a group")
	// ErrChainUnrooted: no receiver-signed consent carrying the expected
	// verb verifies (rule 1).
	ErrChainUnrooted = errors.New("cert: no receiver-signed consent roots the chain")
	// ErrChainVerb: a chain link's verb differs from the root consent's
	// verb (talos-config-xwu). Checked before Attenuate so the diagnostic
	// is the chain's, not the generic ErrVerbMismatch.
	ErrChainVerb = errors.New("cert: chain link verb differs from the root consent verb")
	// ErrChainLinkage: a link's signer does not resolve (itself or via
	// speak-as) to the principal the previous link's aud names (rule 2).
	ErrChainLinkage = errors.New("cert: link signer does not resolve to the previous audience")
	// ErrChainExpired: the effective cert (min exp over the chain, and
	// every speak-as used) is not live at now.
	ErrChainExpired = errors.New("cert: effective chain is expired")
	// ErrUnknownCaveat: some link carries a caveat this verifier does not
	// recognise, or two links set conflicting postage (taint).
	ErrUnknownCaveat = errors.New("cert: effective chain carries an unknown caveat")
	// ErrPostageConflict IS an ErrUnknownCaveat (errors.Is holds) with a
	// sharper name: the taint came from two links setting disagreeing
	// postage, not from an unrecognised caveat key. Taint stays ONE
	// concept — the model (verification/quint/authorize.qnt, cav.unknown)
	// has no error identities, so this split is diagnostics only and the
	// 1:1 mapping is preserved. Never rejects anything ErrUnknownCaveat
	// would have accepted.
	ErrPostageConflict = fmt.Errorf("%w: two links set conflicting postage", ErrUnknownCaveat)
	// ErrTargetMismatch: the effective target omits the receiver and every
	// principal it speaks for (rule 4, ADR-0003: a receiver answers for P
	// only through a live speak-as P→receiver it HOLDS, never one the
	// caller presents).
	ErrTargetMismatch = errors.New("cert: effective target omits the receiver and every principal it speaks for")
	// ErrFacetMismatch: the effective facet omits the requested facet.
	ErrFacetMismatch = errors.New("cert: effective facet omits the requested facet")
	// ErrAudUnbound: the last link's aud binds neither the signer, nor a
	// principal with a live speak-as to it covering the chain's verb, nor
	// "*" with postage (rule 3).
	ErrAudUnbound = errors.New("cert: last link audience does not bind the presenting signer")
)

// authCtx threads the signature-verification cache and the
// rooted-cert accumulator through the pure check. The two are kept
// separate on purpose: sigCache answers "does this signature verify"
// (used everywhere), while rooted collects only certs proven to sit on a
// chain rooted at R (what feeds the caller's clock.Mark — see Result).
type authCtx struct {
	sigCache   map[string]bool
	rooted     []Cert
	rootedSeen map[string]bool
}

func newAuthCtx() *authCtx {
	return &authCtx{sigCache: map[string]bool{}, rootedSeen: map[string]bool{}}
}

func certKey(c Cert) string {
	canon, err := canonicalBytes(c)
	if err != nil {
		canon = []byte(string(c.Iss) + "|" + c.Aud + "|" + string(c.Can))
	}
	return string(canon) + "\x00" + string(c.Sig)
}

// verify checks a signature once and memoizes the result. It does NOT
// record the cert for the mark — rootedness is decided separately (root).
func (a *authCtx) verify(c Cert) bool {
	key := certKey(c)
	if v, ok := a.sigCache[key]; ok {
		return v
	}
	v := verifies(c)
	a.sigCache[key] = v
	return v
}

// root records a cert as rooted at R (dedup), so its iat may feed the
// caller's clock.Mark. Callers must have established both a valid
// signature and rootedness before calling.
func (a *authCtx) root(c Cert) {
	key := certKey(c)
	if a.rootedSeen[key] {
		return
	}
	a.rootedSeen[key] = true
	a.rooted = append(a.rooted, c)
}

// sigOK ports the Quint sigOk with resolution: signature verifies, the
// EFFECTIVE expiry is in the future, and no unknown caveat is present.
//
// The Unknown check is kept for 1:1 fidelity with the model's `sigOk`,
// not because the wire path can reach it: DecodeCert rejects unknown
// caveat keys outright, so a decoded cert never arrives here tainted.
// It bites only for certs built in-process (tests, the rapid fault
// generator) and for Attenuate's fold results, which never pass through
// sigOK. Do not read it as evidence that unknown caveats survive Decode.
func (a *authCtx) sigOK(c Cert, effExp, now int64) bool {
	return a.verify(c) && effExp > now && !c.Cav.Unknown
}

// sovereign is one vouching path for a signed cert: the wallet (or the
// signer itself) the cert may be attributed to, and the cert's effective
// expiry along that path — c.Exp for direct issuance, min(c.Exp, s.Exp)
// through a speak-as s (ADR-0018 axiom 2, "verification-time validity").
// Via is the speak-as that vouches (nil for the signer itself).
type sovereign struct {
	ID     ActorID
	EffExp int64
	Via    *Cert
}

// resolve ports the model's resolve(speakAs, signer, verb, groups, now):
// the SET of sovereigns a cert signed by c.Iss may be attributed to
// (ADR-0018 "resolve before compare", ruled 9l3 / decision 4oz).
//
// The set is {c.Iss} ∪ {s.Iss | s ∈ speakAs, s.can = speak-as, sigOk(s,
// now), s.aud = c.Iss, c.can ∈ s.cav.verbs, groups ⊆ s.cav.groups}:
//
//   - The signer itself is ALWAYS a member: direct issuance is just
//     another vouching path. A hub key is never a consented principal,
//     so it falls out at the consent check; a wallet signing directly
//     stays in.
//   - Every live speak-as naming c.Iss adds its wallet, each with its
//     own effective expiry min(c.Exp, s.Exp). Stale links beside fresh
//     ones for the same key (re-unseal without redeploy) simply
//     contribute nothing; the fresh one still vouches.
//   - `groups` is what the cert names: the member's cav.groups for a
//     member cert, grantGroups(g) for a grant (decision w5s: caveats are
//     literal on both sides).
//
// Resolution yields a set precisely because ANY wallet can sign a
// speak-as for ANY key — the caller assembles the bundle. Rules over the
// result therefore quantify ONE consented wallet (memberSovereigns,
// grantAdmits); they never compare two resolved sets for equality or
// overlap (authorize.qnt FINDING 2026-09-06, mutant m14).
//
// The speak-as's own cav.delegable is NOT consulted here: it governs
// whether the HOT KEY may re-delegate the speak-as itself, not the certs
// the hot key issues (those are gated by cav.verbs / cav.groups).
// Resolution is one hop: a speak-as is non-delegable, so the wallet it
// names is not itself resolved further.
func (a *authCtx) resolve(c Cert, speakAs []Cert, now int64, groups []string) []sovereign {
	out := []sovereign{{ID: c.Iss, EffExp: c.Exp}}
	for i := range speakAs {
		sa := speakAs[i]
		if sa.Can != VerbSpeakAs || sa.Aud != string(c.Iss) {
			continue
		}
		if !a.sigOK(sa, sa.Exp, now) ||
			!containsStr(sa.Cav.Verbs, string(c.Can)) ||
			!subset(groups, sa.Cav.Groups) {
			continue
		}
		via := sa
		out = append(out, sovereign{ID: sa.Iss, EffExp: min(c.Exp, sa.Exp), Via: &via})
	}
	return out
}

// grantGroups ports the model's grantGroups: the groups a grant names —
// its group audience, if any. A hot key may only address group:g under
// a speak-as whose cav.groups ∋ g (literal caveats, decision w5s); a
// key-audience grant names no group and requires nothing of cav.groups.
func grantGroups(g Cert) []string {
	if grp, isGroup := strings.CutPrefix(g.Aud, groupPrefix); isGroup {
		return []string{grp}
	}
	return nil
}

// consentsFor returns the consent grants receiver r has issued to
// issuer: r-signed, invoke, delegable, and currently valid. Used by
// step (2b) (memberSovereigns); the grant side is judged by the fold in
// VerifyChain instead.
func (a *authCtx) consentsFor(consents []Cert, r, issuer ActorID, now int64) []Cert {
	var out []Cert
	for _, c := range consents {
		if c.Iss == r && c.Aud == string(issuer) && c.Can == VerbInvoke &&
			c.Cav.Delegable && a.sigOK(c, c.Exp, now) {
			out = append(out, c)
		}
	}
	return out
}

// rootedPrincipals is the set of actor ids R has SIGNED a delegable
// invoke consent for — signature only, no expiry, no now (decision 7ry).
// These are the issuers that root a chain at R for the purpose of the
// mark (Result.Verified). It is deliberately NOT consentsFor: the
// authorization path keeps its live-at-now check; rooting is provenance.
func (a *authCtx) rootedPrincipals(consents []Cert, r ActorID) map[ActorID]bool {
	set := map[ActorID]bool{}
	for _, c := range consents {
		if c.Iss == r && c.Can == VerbInvoke && c.Cav.Delegable && a.verify(c) {
			set[ActorID(c.Aud)] = true
		}
	}
	return set
}

// rootedHotKeys is the set of hot keys some rooted principal vouches for
// via a signature-valid speak-as in the bundle — the provenance-only
// analogue of resolve (decision jo8): cav.verbs, cav.groups, expiry and
// unknown caveats are ignored here; they gate authorization, not
// whether the hot key is provably that principal's. Single-level.
func (a *authCtx) rootedHotKeys(speakAs []Cert, principals map[ActorID]bool) map[ActorID]bool {
	set := map[ActorID]bool{}
	for _, sa := range speakAs {
		if sa.Can == VerbSpeakAs && principals[sa.Iss] && a.verify(sa) {
			set[ActorID(sa.Aud)] = true
		}
	}
	return set
}

// memberSovereigns ports the model's memberSovereigns — step (2b) on the
// RESOLVED issuer set: the consented sovereigns that vouch for the member
// cert, i.e. those w ∈ resolve(member, member.cav.groups) for which R
// holds a live delegable consent targeting R (or a principal R answers
// for — rule 4's test, ADR-0003). Empty ⇒ the member's name and groups
// are stranger-chosen (3cx) ⇒ reject.
func (a *authCtx) memberSovereigns(in Input, m Cert) []sovereign {
	var out []sovereign
	for _, w := range a.resolve(m, in.Bundle.SpeakAs, in.Now, m.Cav.Groups) {
		if w.EffExp > in.Now &&
			a.consentTargets(in.Receiver, a.consentsFor(in.Receiver.Consents, in.Receiver.ID, w.ID, in.Now), in.Now) {
			out = append(out, w)
		}
	}
	return out
}

// answersFor is rule 4's target test (the model's `answersFor`, ADR-0003):
// target names R itself, or a principal P for which R HOLDS a live
// speak-as P→R (r.SpeakAs — R's own configuration; the caller's bundle
// never enters here). Liveness only: the speak-as's cav.verbs says what R
// may SIGN as P (ADR-0018), and being addressed is not signing.
func (a *authCtx) answersFor(r Receiver, target []ActorID, now int64) bool {
	if containsID(target, r.ID) {
		return true
	}
	for _, s := range r.SpeakAs {
		if s.Can == VerbSpeakAs && s.Aud == string(r.ID) && a.sigOK(s, s.Exp, now) &&
			containsID(target, s.Iss) {
			return true
		}
	}
	return false
}

// rootCerts populates ctx.rooted with every validly-signed cert that is
// rooted at r (see Result). Independent of the accept/reject decision,
// so the mark advances even when authorization fails.
//
// Rooted = signature-only provenance (decisions 7ry, jo8; the model's
// isRooted in verification/quint/clock.qnt): a cert is rooted iff it
// sits on a chain whose root consent r signed, regardless of any expiry
// at now and regardless of speak-as cav.verbs / cav.groups. now is NOT
// read here — the mark must not feed its own rooting. Authorization
// (memberSovereigns, VerifyChain, resolve) keeps the full live-at-now,
// verb- and group-scoped checks.
//
// certs are visited in order. With chained=false (Authorize: member and
// grants are alternatives) the principal set is fixed by the consents.
// With chained=true (VerifyChain) each rooted, delegable link whose aud
// is a key extends the principal set for the links after it — the
// signature-only shadow of the fold, so link i+1 signed by link i's aud
// (or by a hot key that aud vouches for) is rooted too.
func (a *authCtx) rootCerts(r ActorID, consents, speakAs, certs []Cert, chained bool) []Cert {
	principals := a.rootedPrincipals(consents, r)
	// (a) R-signed consents that verified (expiry irrelevant — R signed
	// them, so they are rooted at R and their iat proves time passed).
	for _, c := range consents {
		if c.Iss == r && a.verify(c) {
			a.root(c)
		}
	}
	// (b) speak-as certs signed by a rooted principal.
	rootSpeakAs := func() {
		for _, sa := range speakAs {
			if sa.Can == VerbSpeakAs && a.verify(sa) && principals[sa.Iss] {
				a.root(sa)
			}
		}
	}
	rootSpeakAs()
	// (c) certs signed by a rooted principal, or by a hot key a rooted
	// principal vouches for (single-level; a stranger hot key is vouched
	// for only by stranger wallets, and is dropped).
	for _, c := range certs {
		hotKeys := a.rootedHotKeys(speakAs, principals)
		if !a.verify(c) || !(principals[c.Iss] || hotKeys[c.Iss]) {
			continue
		}
		a.root(c)
		if chained && c.Cav.Delegable && isKeyAud(c.Aud) && !principals[ActorID(c.Aud)] {
			principals[ActorID(c.Aud)] = true
			rootSpeakAs()
		}
	}
	return a.rooted
}

// isKeyAud reports whether aud names a single key (not a group, not "*").
func isKeyAud(aud string) bool {
	return aud != AudAny && !strings.HasPrefix(aud, groupPrefix)
}

// --- the N-link chain verifier (protocol ADR-0001) --------------------

// chainVerdict is one accepted fold of the caller's chain under one of
// the receiver's consents — the model's Verdict {eff, consent, aud}.
// group is the model's AudGroup(grp) sentinel: non-empty means the
// chain verified but its last link addresses group:grp, which only the
// talos layer can resolve; "" means AudBound. (AudUnbound verdicts are
// never returned — they are the ErrAudUnbound rejection.)
type chainVerdict struct {
	eff     Cert
	consent Cert
	group   string
}

// VerifyChain is the one chain verifier (protocol ADR-0001; model
// verification/quint/authorize.qnt `verifyChain`). r is the receiver's
// OWN configuration (id, consents, held speak-as); chain and speakAs are
// what the CALLER presented — chain holds only the links it holds; the
// verifier prepends a receiver-signed consent itself and folds Attenuate
// over [consent, chain...]. verb is the verb the receiver expects for
// THIS operation — invoke for an envelope invocation, publish/relay for
// the M3 lighthouse and relay facets (talos-config-xwu); it is never
// inferred from the caller's links. Rules:
//
//  1. First link signed by the receiver: every admitting root is one of
//     receiver's own consents (iss == receiver, signature verifies,
//     can == verb). From here on the chain's verb IS the root consent's
//     Can: every link carries it (ErrChainVerb otherwise) and every
//     speak-as used must cover it. Everything else about the consent
//     (target, facet, delegability, expiry, taint) is judged by the fold
//     like any link.
//  2. Linkage: link i's signer resolves — itself, or a principal with a
//     live speak-as to it covering the chain's verb and the groups the
//     link names (resolve, ADR-0018 step 2a) — to the principal link
//     i-1's aud names. A group or "*" audience can therefore never be
//     followed.
//  3. Aud binding on the LAST link (eff.Aud): aud == signer; or aud == S
//     with a live speak-as S→signer in speakAs whose cav.verbs ∋ verb;
//     or aud == "*" only if eff.Postage != "" (fail closed without). A
//     group: audience returns ErrGroupAud BESIDE eff/verified — the
//     chain verified; the caller resolves the group (Authorize).
//  4. On the effective cert: Target ∋ receiver — or Target ∋ P for a
//     principal P whose live speak-as P→receiver the receiver HOLDS
//     (r.SpeakAs; ADR-0003 — a speak-as naming the receiver inside the
//     caller's bundle widens nothing) — Facet ∋ facet, no Unknown caveat
//     (taint is OR over the chain, including conflicting postage — that
//     case reports the sharper ErrPostageConflict, which is still an
//     ErrUnknownCaveat), Exp > now (min over the chain), every speak-as
//     used live at now. Delegable:false admits no following link.
//
// An empty caller chain is legal: the consented sovereign presents the
// receiver's consent alone and binds as its aud (or via its hot key).
// Grants at connection level are ALTERNATIVES, never a chain among
// themselves — Authorize calls this once per grant.
//
// verified is the rooted certs for clock.Mark.ObserveAll — the same
// signature-only provenance rule as Result.Verified — and is populated
// on every path, error or not. When several consents root the chain
// the first accepting one (in consents order) is returned; Authorize
// uses the full set internally for its group rule. err on rejection is
// the failure of the consent that got furthest through the fold.
func VerifyChain(r Receiver, verb Verb, chain, speakAs []Cert, signer ActorID, facet string, now int64) (eff Cert, verified []Cert, err error) {
	ctx := newAuthCtx()
	verified = ctx.rootCerts(r.ID, r.Consents, speakAs, chain, true)
	vs, err := ctx.verifyChain(r, verb, chain, speakAs, signer, facet, now)
	if err != nil {
		return Cert{}, verified, err
	}
	for _, v := range vs {
		if v.group == "" {
			return v.eff, verified, nil
		}
	}
	return vs[0].eff, verified, ErrGroupAud
}

// verifyChain ports the model's verifyChain: the SET of verdicts, one
// per receiver-signed consent carrying verb that roots the chain (R may
// hold consents for N>1 sovereigns). Empty ⇒ err (the furthest failure).
func (a *authCtx) verifyChain(r Receiver, verb Verb, chain, speakAs []Cert, signer ActorID, facet string, now int64) ([]chainVerdict, error) {
	var out []chainVerdict
	var best error
	bestRank := -1
	for _, c := range r.Consents {
		if c.Iss != r.ID || c.Can != verb || !a.verify(c) {
			continue
		}
		v, rank, err := a.chainUnder(c, chain, speakAs, r, signer, facet, now)
		if err != nil {
			if rank > bestRank {
				best, bestRank = err, rank
			}
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		if best == nil {
			best = ErrChainUnrooted
		}
		return nil, best
	}
	return out, nil
}

// chainUnder folds the caller's chain under one consent (the model's
// chainUnder + linkStep + effAdmits + audBinding). The chain's verb is
// read off the root consent, never a literal. rank says how far the
// fold got, so verifyChain can report the most informative error. Rule
// 4's target test consults r.SpeakAs (receiver-held) and never speakAs
// (caller-presented).
func (a *authCtx) chainUnder(consent Cert, chain, speakAs []Cert, r Receiver, signer ActorID, facet string, now int64) (chainVerdict, int, error) {
	verb := consent.Can
	eff := consent
	for i, l := range chain {
		switch {
		case l.Can != verb:
			return chainVerdict{}, i, ErrChainVerb
		case !a.verify(l):
			return chainVerdict{}, i, ErrSigMismatch
		case !a.linksTo(eff.Aud, l, speakAs, now):
			return chainVerdict{}, i, ErrChainLinkage
		}
		var err error
		if eff, err = Attenuate(eff, l); err != nil {
			return chainVerdict{}, i, err
		}
	}
	rank := len(chain)
	switch {
	case !a.answersFor(r, eff.Cav.Target, now):
		return chainVerdict{}, rank, ErrTargetMismatch
	case !containsStr(eff.Cav.Facet, facet):
		return chainVerdict{}, rank, ErrFacetMismatch
	case eff.Cav.PostageConflict:
		return chainVerdict{}, rank, ErrPostageConflict
	case eff.Cav.Unknown:
		return chainVerdict{}, rank, ErrUnknownCaveat
	case eff.Exp <= now:
		return chainVerdict{}, rank, ErrChainExpired
	}
	rank++
	// Rule 3 — the last link's aud (Attenuate keeps the child's aud, so
	// it is eff.Aud) against the presenting signer.
	if grp, isGroup := strings.CutPrefix(eff.Aud, groupPrefix); isGroup {
		return chainVerdict{eff: eff, consent: consent, group: grp}, rank, nil
	}
	bound := false
	if eff.Aud == AudAny {
		bound = eff.Cav.Postage != ""
	} else {
		bound = eff.Aud == string(signer) || a.speaksFor(ActorID(eff.Aud), signer, verb, speakAs, now)
	}
	if !bound {
		return chainVerdict{}, rank, ErrAudUnbound
	}
	return chainVerdict{eff: eff, consent: consent}, rank, nil
}

// linksTo is rule 2: link l's signer resolves (itself or via a live,
// group-covering speak-as covering l.Can — which chainUnder has already
// pinned to the root consent's verb) to the key prevAud names. A group
// or "*" prevAud matches no resolved principal, so it ends the chain.
func (a *authCtx) linksTo(prevAud string, l Cert, speakAs []Cert, now int64) bool {
	for _, p := range a.resolve(l, speakAs, now, grantGroups(l)) {
		if prevAud == string(p.ID) {
			return true
		}
	}
	return false
}

// speaksFor is the aud-side speak-as of rule 3: some live speak-as
// S→signer in the proof whose cav.verbs ∋ verb (the chain's verb). Only
// the verb is consulted (cav.groups gate issuer-side resolution, not
// audience binding — orchestrator ruling, see the report).
func (a *authCtx) speaksFor(s, signer ActorID, verb Verb, speakAs []Cert, now int64) bool {
	for _, sp := range speakAs {
		if sp.Iss == s && sp.Aud == string(signer) && sp.Can == VerbSpeakAs &&
			a.sigOK(sp, sp.Exp, now) && containsStr(sp.Cav.Verbs, string(verb)) {
			return true
		}
	}
	return false
}

// --- the connection-level check (ADR-0017/0018) on top of it ---------

// Authorize is the per-connect check: glossary steps (1)–(4), including
// speak-as resolution (2a) and the consented-issuer rule (2b). It is
// deterministic, offline, receiver-rooted, monotone under attenuation
// and fail-closed on every unknown. Identity out comes from the member
// cert only.
//
// It IS VerifyChain on the one-link caller chain [grant] with signer =
// Peer and facet = AcceptTable[ALPN], plus the talos-only layer: member
// identity, group: audiences (the same-w rule), blocklist. Grants are
// alternatives — one chain each.
//
// Resolution (2a) yields a SET of sovereigns per signer (any wallet can
// vouch for any key), so every rule quantifies ONE consented wallet w:
// (2b) some w ∈ resolve(member) is consented; a grant admits iff some w
// roots its chain at R and — for a group audience — that SAME w vouches
// for the member cert (decisions 4oz, w5s; model
// verification/quint/authorize.qnt).
func Authorize(in Input) Result {
	ctx := newAuthCtx()
	// Populate the rooted set first so the mark advances on every path.
	certs := append([]Cert{in.Bundle.Member}, in.Bundle.Grants...)
	ctx.rootCerts(in.Receiver.ID, in.Receiver.Consents, in.Bundle.SpeakAs, certs, false)
	reject := func() Result { return Result{OK: false, Verified: ctx.rooted} }

	// (1) ALPN → facet; unknown ⇒ reject.
	facet, ok := in.AcceptTable[in.ALPN]
	if !ok {
		return reject()
	}

	// (2) member cert: verb, signature, own expiry, no unknown caveat,
	// aud = the QUIC peer key.
	m := in.Bundle.Member
	if m.Can != VerbMember || !ctx.sigOK(m, m.Exp, in.Now) || m.Aud != string(in.Peer) {
		return reject()
	}

	// (2a)+(2b) resolve the member's issuer through the bundle's speak-as;
	// some resolved sovereign must be one R holds a live, delegable
	// consent grant for, targeting R — otherwise its name and groups are
	// stranger-chosen (authorize.qnt, 3cx).
	if len(ctx.memberSovereigns(in, m)) == 0 {
		return reject()
	}

	// (3) blocklist is keyed on the peer (member) key.
	if in.Blocklist[in.Peer] {
		return reject()
	}

	// (3) any grant whose one-link chain verifies admits — grants are
	// alternatives; identity from the member cert ONLY.
	for _, g := range in.Bundle.Grants {
		if ctx.grantAdmits(in, facet, g) {
			return Result{
				OK: true,
				Identity: Identity{
					Key:    in.Peer,
					Name:   m.Cav.Name,
					Groups: m.Cav.Groups,
				},
				Verified: ctx.rooted,
			}
		}
	}
	// (4) no grant matched.
	return reject()
}

// grantAdmits ports the model's grantAdmits 1:1: the grant admits if
// VerifyChain admits the one-link caller chain [g] as an INVOKE chain
// (a connection is an invocation; a publish/relay consent never roots
// one) with the QUIC peer as signer and, for a group audience (the
// ErrGroupAud sentinel), the
// SAME sovereign w whose consent roots the chain (the verdict's
// consent.aud) vouches for the member cert and the member's groups
// contain it.
//
// Never "resolved issuers are equal", never set overlap: a stranger
// wallet can vouch for both hub keys and would bridge two sovereigns
// (authorize.qnt FINDING 2026-09-06, mutant m14, ruled 9l3). Groups are
// sovereign-scoped names — never hot-key-scoped, never global.
func (a *authCtx) grantAdmits(in Input, facet string, g Cert) bool {
	vs, err := a.verifyChain(in.Receiver, VerbInvoke, []Cert{g}, in.Bundle.SpeakAs, in.Peer, facet, in.Now)
	if err != nil {
		return false
	}
	for _, v := range vs {
		if v.group == "" || a.groupSatisfied(in, v.group, in.Bundle.Member, ActorID(v.consent.Aud)) {
			return true
		}
	}
	return false
}

// groupSatisfied is the talos-layer group rule under the consented
// sovereign w that roots the grant's chain: group:grp is satisfied only
// if that SAME w vouches for the member cert (w ∈ resolve(member,
// member.cav.groups), live along that path) and the member's groups
// contain grp.
func (a *authCtx) groupSatisfied(in Input, grp string, member Cert, w ActorID) bool {
	if !containsStr(member.Cav.Groups, grp) {
		return false
	}
	for _, mw := range a.resolve(member, in.Bundle.SpeakAs, in.Now, member.Cav.Groups) {
		if mw.ID == w && mw.EffExp > in.Now {
			return true
		}
	}
	return false
}

// consentTargets reports whether any of the given consents names R, or a
// principal R answers for, in its target set (step 2b; rule 4's test).
func (a *authCtx) consentTargets(r Receiver, consents []Cert, now int64) bool {
	for _, c := range consents {
		if a.answersFor(r, c.Cav.Target, now) {
			return true
		}
	}
	return false
}

// Attenuate computes the effective authority of a child link under its
// parent (the model's `attenuate`, field for field): the child keeps
// its own iss/aud/can; Target, Facet, Groups, Verbs and Endpoints are
// the intersections; Exp is the min; an unknown caveat on either side
// taints the result. Postage is MONOTONE: whichever side set it carries
// forward; if both set it they must agree, else the result is tainted
// (Unknown) and the chain rejects. A conflict also sets the verifier-side
// PostageConflict flag — the same taint, OR-folded the same way, kept
// apart only so chainUnder can report ErrPostageConflict instead of the
// bare ErrUnknownCaveat. A parent with delegable:false admits
// no following link (ErrNotDelegable); a child with another verb than
// its parent is not a link of the same chain (ErrVerbMismatch).
func Attenuate(parent, child Cert) (Cert, error) {
	if !parent.Cav.Delegable {
		return Cert{}, ErrNotDelegable
	}
	if child.Can != parent.Can {
		return Cert{}, ErrVerbMismatch
	}
	eff := child
	eff.Cav.Target = intersectID(parent.Cav.Target, child.Cav.Target)
	eff.Cav.Facet = intersectStr(parent.Cav.Facet, child.Cav.Facet)
	eff.Cav.Groups = intersectStr(parent.Cav.Groups, child.Cav.Groups)
	eff.Cav.Verbs = intersectStr(parent.Cav.Verbs, child.Cav.Verbs)
	eff.Cav.Endpoints = intersectStr(parent.Cav.Endpoints, child.Cav.Endpoints)
	eff.Cav.Delegable = child.Cav.Delegable // parent is delegable by the guard
	conflict := parent.Cav.Postage != "" && child.Cav.Postage != "" && parent.Cav.Postage != child.Cav.Postage
	if parent.Cav.Postage != "" {
		eff.Cav.Postage = parent.Cav.Postage
	}
	eff.Cav.Unknown = parent.Cav.Unknown || child.Cav.Unknown || conflict
	eff.Cav.PostageConflict = parent.Cav.PostageConflict || child.Cav.PostageConflict || conflict
	eff.Exp = min(parent.Exp, child.Exp)
	return eff, nil
}

// --- small set helpers (nil-safe, order-insensitive membership) ---

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func containsID(xs []ActorID, x ActorID) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func intersectStr(a, b []string) []string {
	var out []string
	for _, x := range a {
		if containsStr(b, x) && !containsStr(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func intersectID(a, b []ActorID) []ActorID {
	var out []ActorID
	for _, x := range a {
		if containsID(b, x) && !containsID(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// subset reports whether every element of need is in have.
func subset(need, have []string) bool {
	for _, x := range need {
		if !containsStr(have, x) {
			return false
		}
	}
	return true
}
