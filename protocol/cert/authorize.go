package cert

import (
	"errors"
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

// Input is the complete, self-contained input to Authorize. The function
// is pure: no I/O, no clock reads. Now is the verifier's EFFECTIVE clock
// max(local, lw) (ADR-0019); the caller computes it from a clock.Mark.
type Input struct {
	Receiver    ActorID           // R: the receiver under test
	AcceptTable map[string]string // ALPN class → facet (producer-owned)
	Consents    []Cert            // consent grants R has issued
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

// ErrNotDelegable is returned by Attenuate when the parent forbids a
// following link (delegable:false).
var ErrNotDelegable = errors.New("cert: parent is not delegable")

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
// issuer: r-signed, invoke, delegable, and currently valid.
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
// holds a live delegable consent targeting R. Empty ⇒ the member's name
// and groups are stranger-chosen (3cx) ⇒ reject.
func (a *authCtx) memberSovereigns(in Input, m Cert) []sovereign {
	var out []sovereign
	for _, w := range a.resolve(m, in.Bundle.SpeakAs, in.Now, m.Cav.Groups) {
		if w.EffExp > in.Now &&
			consentTargets(a.consentsFor(in.Consents, in.Receiver, w.ID, in.Now), in.Receiver) {
			out = append(out, w)
		}
	}
	return out
}

// buildRooted populates ctx.rooted with every validly-signed cert that is
// rooted at R (see Result). Independent of the accept/reject decision, so
// the mark advances even when authorization fails.
//
// Rooted = signature-only provenance (decisions 7ry, jo8; the model's
// isRooted in verification/quint/clock.qnt): a cert is rooted iff it
// sits on a chain whose root consent R signed, regardless of any
// expiry at in.Now and regardless of speak-as cav.verbs / cav.groups.
// in.Now is NOT read here — the mark must not feed its own rooting.
// Authorization (memberSovereigns, grantAdmits, resolve) keeps the full
// live-at-now, verb- and group-scoped checks.
func (a *authCtx) buildRooted(in Input) {
	principals := a.rootedPrincipals(in.Consents, in.Receiver)
	hotKeys := a.rootedHotKeys(in.Bundle.SpeakAs, principals)
	// (a) R-signed consents that verified (expiry irrelevant — R signed
	// them, so they are rooted at R and their iat proves time passed).
	for _, c := range in.Consents {
		if c.Iss == in.Receiver && a.verify(c) {
			a.root(c)
		}
	}
	// (b) speak-as certs signed by a rooted principal.
	for _, sa := range in.Bundle.SpeakAs {
		if sa.Can == VerbSpeakAs && a.verify(sa) && principals[sa.Iss] {
			a.root(sa)
		}
	}
	// (c) member/grant certs signed by a rooted principal, or by a hot
	// key a rooted principal vouches for (single-level; a stranger hot
	// key is vouched for only by stranger wallets, and is dropped).
	certs := append([]Cert{in.Bundle.Member}, in.Bundle.Grants...)
	for _, c := range certs {
		if a.verify(c) && (principals[c.Iss] || hotKeys[c.Iss]) {
			a.root(c)
		}
	}
}

// Authorize is the per-connect check: glossary steps (1)–(4), including
// speak-as resolution (2a) and the consented-issuer rule (2b). It is
// deterministic, offline, receiver-rooted, monotone under attenuation
// and fail-closed on every unknown. Identity out comes from the member
// cert only.
//
// Resolution (2a) yields a SET of sovereigns per signer (any wallet can
// vouch for any key), so every rule quantifies ONE consented wallet w:
// (2b) some w ∈ resolve(member) is consented; a grant admits iff some w
// ∈ resolve(grant) roots its chain at R and — for a group audience —
// that SAME w vouches for the member cert (decisions 4oz, w5s; model
// verification/quint/authorize.qnt).
func Authorize(in Input) Result {
	ctx := &authCtx{sigCache: map[string]bool{}, rootedSeen: map[string]bool{}}
	// Populate the rooted set first so the mark advances on every path.
	ctx.buildRooted(in)
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

	// (3) a grant admits if, for ONE consented w vouching for its
	// signer, the chain [consent(R→w), speak-as(w→g.iss)?, g] verifies,
	// intersects to still name R and the facet, and its aud resolves to
	// the peer or — via the same w — to the member's group.
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

// grantAdmits ports the model's grantAdmits 1:1. One grant admits if,
// for ONE sovereign w ∈ resolve(g, grantGroups(g)) that R consented to,
// the chain [consent(R→w), speak-as(w→g.iss)?, g] verifies, the
// field-wise intersection still names R and the facet, and the audience
// resolves:
//
//   - key audience: the aud is the QUIC peer (w only roots the chain);
//   - group audience group:g: the SAME w ∈ resolve(member,
//     member.cav.groups) — one consented wallet vouches for BOTH the
//     grant's signer and the member's signer — and g ∈ member.cav.groups.
//
// Never "resolved issuers are equal", never set overlap: a stranger
// wallet can vouch for both hub keys and would bridge two sovereigns
// (authorize.qnt FINDING 2026-09-06, mutant m14, ruled 9l3). Groups are
// sovereign-scoped names — never hot-key-scoped, never global.
func (a *authCtx) grantAdmits(in Input, facet string, g Cert) bool {
	if g.Can != VerbInvoke || !a.sigOK(g, g.Exp, in.Now) {
		return false
	}
	m := in.Bundle.Member
	for _, w := range a.resolve(g, in.Bundle.SpeakAs, in.Now, grantGroups(g)) {
		if w.EffExp <= in.Now {
			continue
		}
		// consent chain: R → w, target ∋ R, facet ∋ facet (intersected
		// with the grant's own caveats).
		chained := false
		for _, c := range a.consentsFor(in.Consents, in.Receiver, w.ID, in.Now) {
			if containsID(intersectID(c.Cav.Target, g.Cav.Target), in.Receiver) &&
				containsStr(intersectStr(c.Cav.Facet, g.Cav.Facet), facet) {
				chained = true
				break
			}
		}
		if !chained {
			continue
		}
		if a.audSatisfied(in, g, m, w.ID) {
			return true
		}
	}
	return false
}

// audSatisfied resolves a grant's aud under the consented sovereign w
// that roots the grant's chain: an actor id must equal the peer; a group
// group:g is satisfied only if that SAME w vouches for the member cert
// (w ∈ resolve(member, member.cav.groups), live along that path) and the
// member's groups contain g.
func (a *authCtx) audSatisfied(in Input, g, member Cert, w ActorID) bool {
	grp, isGroup := strings.CutPrefix(g.Aud, groupPrefix)
	if !isGroup {
		return g.Aud == string(in.Peer)
	}
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

// consentTargets reports whether any of the given consents names r in
// its target set (step 2b requires target ∋ R).
func consentTargets(consents []Cert, r ActorID) bool {
	for _, c := range consents {
		if containsID(c.Cav.Target, r) {
			return true
		}
	}
	return false
}

// Attenuate computes the effective authority of a child link under its
// parent: field-wise intersection over target, facet, groups and verbs;
// effective expiry min(parent, child); an unknown caveat on either taints
// the result. A parent with delegable:false admits no following link.
func Attenuate(parent, child Cert) (Cert, error) {
	if !parent.Cav.Delegable {
		return Cert{}, ErrNotDelegable
	}
	eff := child
	eff.Cav.Target = intersectID(parent.Cav.Target, child.Cav.Target)
	eff.Cav.Facet = intersectStr(parent.Cav.Facet, child.Cav.Facet)
	eff.Cav.Groups = intersectStr(parent.Cav.Groups, child.Cav.Groups)
	eff.Cav.Verbs = intersectStr(parent.Cav.Verbs, child.Cav.Verbs)
	eff.Cav.Delegable = child.Cav.Delegable // parent is delegable by the guard
	eff.Cav.Unknown = parent.Cav.Unknown || child.Cav.Unknown
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
