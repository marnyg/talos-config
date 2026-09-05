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
// consents, speak-as certs whose iss is a principal R holds a live
// consent for, and member/grant certs whose RESOLVED issuer is such a
// principal. A stranger's validly-self-signed cert is NOT included even
// though its signature verifies, because it is not on a chain rooted at
// R; otherwise any peer that can connect could push a caller's
// clock.Mark into the future (denial by strangers, which ADR-0019
// excludes). Expired-but-rooted certs ARE included: an expired cert from
// a consented issuer still proves its iat passed.
//
// The caller folds these into a clock.Mark via mark.ObserveAll — safe to
// do on both accept and reject, as Verified is populated on both paths.
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

// resolve maps a cert's signing key to its principal through a speak-as
// in the bundle (ADR-0018 "resolve before compare").
//
//   - It considers ALL speak-as certs whose aud equals c.Iss (not just
//     the first): re-unseal without a redeploy leaves an expired
//     speak-as beside a fresh valid one for the same hot key, and an
//     honest caller must not be rejected because the stale one was
//     listed first. A link is ok iff SOME matching speak-as is good
//     (verifies, unexpired, no unknown caveat, cav.verbs contains c.can,
//     and — for member certs — cav.groups ⊇ requireGroups); the
//     effective expiry uses the BEST (max exp) good speak-as, min'd with
//     c.exp ("verification-time validity").
//   - With no matching speak-as the cert is directly issued: resolved
//     issuer is c.Iss, effExp is c.Exp, link ok.
//
// The speak-as's own cav.delegable is NOT consulted here: it governs
// whether the HOT KEY may re-delegate the speak-as itself, not the certs
// the hot key issues (those are gated by cav.verbs / cav.groups).
// Resolution is single-level in v0: this returns the speak-as's iss as
// the resolved issuer without recursively resolving THAT key, so a
// speak-as whose own iss is not a directly consented principal resolves
// to nothing usable (step 2b / the principal set reject it).
//
// used is the good speak-as selected (nil for direct issuance), so the
// caller can also mark it rooted.
func (a *authCtx) resolve(c Cert, speakAs []Cert, now int64, requireGroups []string) (issuer ActorID, effExp int64, ok bool, used *Cert) {
	matched := false
	found := false
	var bestExp int64
	var best Cert
	for i := range speakAs {
		sa := speakAs[i]
		if sa.Can != VerbSpeakAs || sa.Aud != string(c.Iss) {
			continue
		}
		matched = true
		good := a.sigOK(sa, sa.Exp, now) &&
			containsStr(sa.Cav.Verbs, string(c.Can)) &&
			subset(requireGroups, sa.Cav.Groups)
		if good && (!found || sa.Exp > bestExp) {
			found = true
			bestExp = sa.Exp
			best = sa
		}
	}
	if matched {
		if !found {
			return c.Iss, c.Exp, false, nil
		}
		bc := best
		return best.Iss, min(c.Exp, bestExp), true, &bc
	}
	return c.Iss, c.Exp, true, nil
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

// consentedPrincipals is the set of actor ids R holds a live, delegable
// consent grant for. These are the only issuers whose certs may feed the
// mark (Result.Verified) or root a chain.
func (a *authCtx) consentedPrincipals(consents []Cert, r ActorID, now int64) map[ActorID]bool {
	set := map[ActorID]bool{}
	for _, c := range consents {
		if c.Iss == r && c.Can == VerbInvoke && c.Cav.Delegable && a.sigOK(c, c.Exp, now) {
			set[ActorID(c.Aud)] = true
		}
	}
	return set
}

// buildRooted populates ctx.rooted with every validly-signed cert that is
// rooted at R (see Result). Independent of the accept/reject decision, so
// the mark advances even when authorization fails.
func (a *authCtx) buildRooted(in Input) {
	principals := a.consentedPrincipals(in.Consents, in.Receiver, in.Now)
	// (a) R-signed consents that verified (expiry irrelevant — R signed
	// them, so they are rooted at R and their iat proves time passed).
	for _, c := range in.Consents {
		if c.Iss == in.Receiver && a.verify(c) {
			a.root(c)
		}
	}
	// (b) speak-as certs whose iss is a consented principal.
	for _, sa := range in.Bundle.SpeakAs {
		if sa.Can == VerbSpeakAs && a.verify(sa) && principals[sa.Iss] {
			a.root(sa)
		}
	}
	// (c) member/grant certs whose RESOLVED issuer is a consented
	// principal (resolution single-level; a stranger hot key resolves to
	// a non-principal and is dropped).
	certs := append([]Cert{in.Bundle.Member}, in.Bundle.Grants...)
	for _, c := range certs {
		resIss, _, ok, _ := a.resolve(c, in.Bundle.SpeakAs, in.Now, nil)
		if ok && a.verify(c) && principals[resIss] {
			a.root(c)
		}
	}
}

// Authorize is the per-connect check: glossary steps (1)–(4), including
// speak-as resolution (2a) and the consented-issuer rule (2b). It is
// deterministic, offline, receiver-rooted, monotone under attenuation
// and fail-closed on every unknown. Identity out comes from the member
// cert only.
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

	m := in.Bundle.Member
	// (2a) resolve the member issuer through a speak-as (if any).
	mIssuer, mEffExp, mLinkOK, _ := ctx.resolve(m, in.Bundle.SpeakAs, in.Now, m.Cav.Groups)

	// (2) member cert: verb, signature, effective expiry, aud = peer.
	if m.Can != VerbMember {
		return reject()
	}
	if !mLinkOK {
		return reject()
	}
	if !ctx.sigOK(m, mEffExp, in.Now) {
		return reject()
	}
	if m.Aud != string(in.Peer) {
		return reject()
	}

	// (2b) the RESOLVED member issuer must be one R holds a live,
	// delegable consent grant for, targeting R — otherwise its name and
	// groups are stranger-chosen (authorize.qnt, 3cx).
	if !consentTargets(ctx.consentsFor(in.Consents, in.Receiver, mIssuer, in.Now), in.Receiver) {
		return reject()
	}

	// (3) blocklist is keyed on the peer (member) key.
	if in.Blocklist[in.Peer] {
		return reject()
	}

	// (3) a grant admits if its chain [consent(R→resolved iss), grant]
	// verifies, intersects to still name R and the facet, and its aud
	// resolves to the peer or the member's group.
	for _, g := range in.Bundle.Grants {
		if ctx.grantAdmits(in, facet, g, mIssuer, in.Now) {
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

// grantAdmits ports the Quint grantAdmits with speak-as resolution on
// the grant's issuer and the group rule comparing RESOLVED issuers.
func (a *authCtx) grantAdmits(in Input, facet string, g Cert, resolvedMemberIssuer ActorID, now int64) bool {
	if g.Can != VerbInvoke {
		return false
	}
	gIssuer, gEffExp, gLinkOK, _ := a.resolve(g, in.Bundle.SpeakAs, now, nil)
	if !gLinkOK || !a.sigOK(g, gEffExp, now) {
		return false
	}
	// consent chain: R → resolved grant issuer, target ∋ R, facet ∋ facet.
	chained := false
	for _, c := range a.consentsFor(in.Consents, in.Receiver, gIssuer, now) {
		if containsID(intersectID(c.Cav.Target, g.Cav.Target), in.Receiver) &&
			containsStr(intersectStr(c.Cav.Facet, g.Cav.Facet), facet) {
			chained = true
			break
		}
	}
	if !chained {
		return false
	}
	return audSatisfied(g, gIssuer, in.Bundle.Member, resolvedMemberIssuer, in.Peer)
}

// audSatisfied resolves a grant's aud: an actor id must equal the peer;
// a group is satisfied only by a member cert whose RESOLVED issuer is the
// grant's RESOLVED issuer and whose groups contain the name (issuer-
// scoped groups; ADR-0018 "resolve before compare").
func audSatisfied(g Cert, gIssuer ActorID, member Cert, memberIssuer, peer ActorID) bool {
	if grp, isGroup := strings.CutPrefix(g.Aud, groupPrefix); isGroup {
		return gIssuer == memberIssuer && containsStr(member.Cav.Groups, grp)
	}
	return g.Aud == string(peer)
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
