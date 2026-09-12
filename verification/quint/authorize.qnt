// authorize.qnt — the per-connect check of ADR-0017 + ADR-0018 (talos-config-czi)
// and, since talos-config-0bc.2.1, the N-link chain verifier of protocol
// ADR-0001 that the per-connect check is a special case of.
//
// Models domain-model.md glossary "Authorize", "Attenuation", "Grant",
// "Consent grant", "Group", "Facet", "Speak-as" as ONE pure function,
// then throws random worlds at it: forged/expired/unknown certs,
// consents signed by the wrong receiver, group grants from a different
// sovereign than the member cert, blocklisted peers, ALPNs the receiver
// never declared, hub-signed certs whose `speak-as` is missing, expired,
// forged, too narrow, or from a wallet the receiver never consented to.
// This is the spec the Go `authorize()` (0bc.1 / 359.8.1) must match;
// the rapid suite there checks the same laws over real signatures.
//
// Shape of the model: every step regenerates a complete scenario
// (receiver consents, caller bundle {member, speak-as[], invoke[]},
// ALPN, blocklist, one attenuation of one link) and the invariants are
// laws over that scenario. Because no state carries across steps,
// `verify --max-steps=2` already covers the full space Apalache can
// reach; `run` adds random breadth.
//
// The speak-as link (ADR-0018): member and invoke certs are signed by a
// per-process hub key (HUB_A, HUB_B); the bundle carries wallet-signed
// `{iss: wallet, aud: hubkey, can: speak-as, cav: {verbs, groups,
// delegable: false}, exp}` certs. Step (2a) RESOLVES a signer to the set
// of sovereigns that vouch for it — the signer itself (a wallet signing
// directly) plus every wallet with a live speak-as to it whose
// `cav.verbs` contains the cert's verb and whose `cav.groups` covers the
// groups the cert names. Steps (2b) and the group rule operate on
// resolved issuers. Resolution is one hop (speak-as is non-delegable).
//
// Laws checked (glossary "Authorize" → Properties):
//   deterministic & offline    by construction: `authorize` is a pure def
//                              whose inputs are exactly the glossary's
//                              (receiver key, accept table, consents,
//                              blocklist, ALPN, bundle) — the name map
//                              is NOT among them.
//   receiver-rooted            invReceiverRooted, invConsentMustDelegate,
//                              invConsentTargetsSelf, invSpeakAsIssuerConsented
//   monotone under attenuation invMonotone (grant links AND speak-as links)
//   fail-closed on unknown     invUnknownAlpnRejects, invGrantsFailClosed,
//                              invMemberRequired, invSpeakAsSigRequired
//   identity from member only  invIdentityFromMember
//   issuer-scoped groups       invCrossIssuerGroupRejects (resolved issuers),
//                              invGroupMatchRootedInChain
//   target/facet in chain      invTargetIsReceiver, invFacetMatchesAlpn
//   blocklist                  invBlocklistRejects
//   member issuer consented    invMemberIssuerConsented (2b, resolved)
//   speak-as bounds (ADR-0018) invSpeakAsExpiredRejects (verification-time
//                              validity), invSpeakAsVerbScoped,
//                              invSpeakAsGroupsCoverMember,
//                              invGrantSpeakAsRequired,
//                              invGrantSpeakAsGroupsCoverAud
//   mixed epochs accept        mixedEpochTest (member by HUB_A, grant by
//                              HUB_B, both under the same wallet ⇒ Accept)
//
// FINDING (2026-09-05, model-first; ruled same day, talos-config-3cx):
// the glossary's step (2) as first written verified the member cert's
// signature, expiry and `aud`, but never asked WHO issued it. A grant
// `aud: <key>` from a consented Owner plus a member cert self-signed by
// the caller (or any stranger sovereign) yielded Accept with identity
// {name, groups} chosen by the stranger — straight into the gateway's
// per-request identity header. invMemberIssuerConsented catches it;
// step (2b) — the member cert's `iss` must be an issuer R holds a live
// consent grant for — is now in ADR-0017 and the glossary. Mutant m1
// (drop 2b) is the regression test.
//
// FINDING (2026-09-06, model-first; ruled the same day, talos-config-9l3:
// glossary *Attenuation* + ADR-0018 axiom 1 [amended] now carry the
// one-consented-wallet wording; grant-side groups ruled `zpf`): ADR-0018 axiom 1 and the glossary say the group rule "compares
// RESOLVED issuers" and step (2a) says "take ITS iss as the effective
// issuer" — singular. But the caller assembles the bundle, and any
// wallet (the caller's own) can sign a speak-as naming any hub key, so
// a signer resolves to a SET of wallets, only some of them consented.
// Comparing the sets for overlap is unsound: R consents to OWNER1 and
// OWNER2 (N>1 is a goal); CALLER holds OWNER2's `admins` member cert
// (signed by HUB_A); OWNER1's hub HUB_B granted `group:admins` at R;
// CALLER adds two self-signed speak-as certs ROGUE→HUB_A, ROGUE→HUB_B.
// Resolved sets {HUB_A, OWNER2, ROGUE} and {HUB_B, OWNER1, ROGUE}
// overlap on ROGUE ⇒ Accept: OWNER2's member reaches OWNER1's admins
// facet. The sound rule binds both ends to ONE consented sovereign: the
// wallet whose consent roots the grant's chain must itself vouch for the
// member cert. `authorize` below does that (`grantAdmits` binds the
// group rule to the `w` named by the verdict's rooting consent);
// invGroupMatchRootedInChain is the law; mutant m14 (overlap
// instead of the same `w`) is the regression test. Proposed glossary
// wording: "the group rule is satisfied when one sovereign W that R
// holds a live consent for both (i) vouches for the grant's signer and
// (ii) vouches for the member cert's signer, and the member's groups
// contain g" — never "resolved issuers are equal".
//
// N-LINK CHAIN VERIFIER (protocol ADR-0001, talos-config-0bc.2.1). One
// pure def `verifyChain(receiver, consents, chain, speakAs, signer,
// facet, now)` is the spec both the Go `cert.VerifyChain` and its rapid
// laws follow 1:1. The caller presents `chain` — only the links it
// holds; the verifier PREPENDS a receiver-signed consent itself and
// folds `attenuate` (the Go `cert.Attenuate`, ported field for field)
// over `[consent, chain...]`. Rules:
//   1. first link signed by the receiver — every admitting root is one
//      of R's own consents (`iss == R`, verifies, `can: invoke`);
//   2. linkage — link i's signer resolves (through speak-as, one hop,
//      as in step 2a) to the principal link i-1 names in `aud`; a
//      group or `*` audience can therefore never be followed;
//   3. last link's `aud` binds the presenting signer: `aud == signer`,
//      or `aud == S` with a live `speak-as S→signer` whose `cav.verbs ∋
//      invoke` in the proof, or `aud == "*"` — fail closed unless the
//      effective cert carries `postage`. A `group:` audience is the
//      `AudGroup` sentinel (Go: `ErrGroupAud` beside `eff`): the chain
//      verified, the talos layer resolves the group (same-w rule below);
//   4. on the effective cert: `target ∋ receiver`, `facet ∋ facet`, no
//      unknown caveat (taint is OR over the chain), `exp > now` (min
//      over the chain), and every speak-as used is live at `now`;
//      `delegable: false` admits no following link.
// The result is the SET of verdicts `{eff, consent, aud}` — one per
// consent that roots the chain — because R may hold consents for N>1
// sovereigns; Accept ⇔ non-empty. `verified` (the rooted certs that
// feed the clock mark) is clock.qnt's subject and not modelled here.
//
// Caveat vocabulary v2: `endpoints` (opaque strings; plain set
// intersection, exactly like target/facet — an absent set is EMPTY, not
// "unconstrained") and `postage` (opaque requirement string; presence
// is MONOTONE — a link may add it, none may remove it; two links that
// both set it must agree, else the chain is tainted and rejected).
// Postage never gates `invoke` on its own; it only unlocks `aud: "*"`.
//
// The connection-level `authorize` below IS `verifyChain` on the
// one-link caller chain `[grant]` with `signer = peer`, `facet =
// AcceptTable[ALPN]`, plus the talos-only layer: member identity,
// `group:` audiences (the same-w rule), blocklist. Grants are
// ALTERNATIVES — one chain each — never a chain among themselves.
// The 13+4 pre-existing laws are kept unchanged and are the proof that
// the refactor preserved the connection-level behaviour.
//
// Laws checked for the chain (named after the rule they pin):
//   first link R-signed       invChainRootIsReceiverConsent
//   linkage                   invChainLinked, invChainHotKeyNeedsSpeakAs
//   aud binding               invChainAudBindsSigner, invStarFailsClosedWithoutPostage,
//                             invAudSpeakAsNeedsInvoke
//   attenuation only          invEffectiveIsIntersection, invEndpointsIntersect,
//                             invChainMonotone
//   postage                   invPostageMonotone, invPostageConflictRejects
//   fail closed per link      invChainForgedLinkRejects, invChainExpiredLinkRejects,
//                             invChainUnknownCaveatRejects, invChainVerbIsInvoke,
//                             invChainNotDelegableStops
//   target/facet              invChainTargetsReceiver, invChainFacetMatches
//   witness                   chainHappyPathTest (3 links, hub-signed, aud-side
//                             speak-as), starPostageTest, consentOnlyTest
//
// Time (ADR-0019, talos-config-439): `NOW` here is the verifier's
// EFFECTIVE clock, max(local clock, low-water mark over verified `iat`s).
// The primitive carries `iat`; how the mark is maintained and what it
// bounds is clock.qnt's subject — authorize() only consumes the result,
// so `iat` is not a field of this model's Cert.
//
// Check:
//   quint test   authorize.qnt                          (happy path, mixed epochs)
//   quint run    --invariant=invAll --max-steps=20 --max-samples=1000 authorize.qnt
//   quint verify --invariant=invAll --max-steps=2  authorize.qnt
module authorize {
  // ---- closed world -------------------------------------------------
  pure val OWNER1 = 1        // a sovereign R consents to (usually)
  pure val OWNER2 = 2        // a second sovereign — N>1 is a design goal
  pure val R = 3             // the receiver under test
  pure val OTHER_R = 4       // another receiver; its consents must not count for R
  pure val CALLER = 5        // the QUIC peer
  pure val ROGUE = 6         // a wallet nobody consented to; signs whatever it likes
  pure val HUB_A = 7         // a hub process key (ADR-0018), one epoch
  pure val HUB_B = 8         // the next hub process key, another epoch
  pure val CALLER_HOT = 9    // the caller's own hot key: presents chains under a speak-as CALLER→CALLER_HOT

  pure val WALLETS = Set(OWNER1, OWNER2, ROGUE)  // sovereign roots — what receivers consent to
  pure val HUBS = Set(HUB_A, HUB_B)              // hot keys — hold authority only via speak-as
  pure val SIGNERS = WALLETS.union(HUBS)         // may sign member / invoke certs
  pure val HOT_KEYS = HUBS.union(Set(CALLER_HOT)) // every key that holds authority only via speak-as
  pure val PEERS = Set(CALLER, ROGUE)
  pure val RECEIVERS = Set(R, OTHER_R)
  pure val FACETS = Set("apid", "kube-api")
  pure val NO_FACET = ""          // total-lookup sentinel: no grant's cav.facet ever contains it
  pure val GROUPS = Set("admins", "media")
  pure val NAMES = Set("tv", "laptop")
  pure val NOW = 5
  pure val EXPS = Set(0, 5, 10)  // expired / at the boundary / live relative to NOW
  // Caveat vocabulary v2 (ADR-0001). Opaque strings both; the model only
  // needs two distinct values to state "agree" and "conflict".
  pure val ENDPOINTS = Set("quic:a", "quic:b")
  pure val NO_POSTAGE = ""
  pure val POSTAGES = Set(NO_POSTAGE, "pow:20", "pay:1")

  // The receiver's accept table: ALPN class → facet. Producer-owned,
  // closed. The third ALPN is one this receiver never declared.
  pure val ACCEPT_TABLE = Map("mesh/apid/v1" -> "apid", "mesh/kube/v1" -> "kube-api")
  pure val ALPNS = ACCEPT_TABLE.keys().union(Set("mesh/bogus/v1"))

  // ---- the one primitive -------------------------------------------
  type Verb = Member | Invoke | SpeakAs | OtherVerb   // OtherVerb: any verb outside this verifier's closed set
  pure val VERBS = Set(Member, Invoke)                // what a speak-as may caveat over (ADR-0018: literal)
  type Aud = AKey(int) | AGroup(str) | AAny   // AAny is the wire `"*"`
  // `unknown` stands for "carries a caveat this verifier does not
  // recognise" (ADR-0017: an unknown caveat rejects). `verbs` is the
  // speak-as caveat: which verbs the hot key may sign as the wallet.
  // `endpoints` / `postage` are caveat vocabulary v2 (ADR-0001);
  // `postage == NO_POSTAGE` means absent.
  type Cav = { target: Set[int], facet: Set[str], groups: Set[str], verbs: Set[Verb],
               endpoints: Set[str], postage: str,
               name: str, delegable: bool, unknown: bool }
  // `forged`: the signature does not verify under `iss`.
  type Cert = { iss: int, aud: Aud, can: Verb, cav: Cav, exp: int, forged: bool }
  type Identity = { key: int, name: str, groups: Set[str] }
  type Result = Reject | Accept(Identity)

  pure def mkCert(iss: int, aud: Aud, can: Verb, target: Set[int], facet: Set[str],
                  groups: Set[str], name: str, delegable: bool, unknown: bool,
                  exp: int, forged: bool): Cert =
    { iss: iss, aud: aud, can: can, exp: exp, forged: forged,
      cav: { target: target, facet: facet, groups: groups, verbs: Set(), name: name,
             endpoints: Set(), postage: NO_POSTAGE,
             delegable: delegable, unknown: unknown } }

  pure def withEndpoints(c: Cert, e: Set[str]): Cert = c.with("cav", c.cav.with("endpoints", e))
  pure def withPostage(c: Cert, p: str): Cert = c.with("cav", c.cav.with("postage", p))

  // {iss: wallet, aud: hubkey, can: speak-as, cav: {verbs, groups, delegable: false}, exp}
  pure def mkSpeakAs(wallet: int, hubkey: int, verbs: Set[Verb], groups: Set[str],
                     unknown: bool, exp: int, forged: bool): Cert =
    { iss: wallet, aud: AKey(hubkey), can: SpeakAs, exp: exp, forged: forged,
      cav: { target: Set(), facet: Set(), groups: groups, verbs: verbs, name: "",
             endpoints: Set(), postage: NO_POSTAGE,
             delegable: false, unknown: unknown } }

  pure def isKey(a: Aud, k: int): bool = match a {
    | AKey(x) => x == k
    | _ => false
  }
  pure def isAny(a: Aud): bool = match a {
    | AAny => true
    | _ => false
  }

  // Signature, expiry, and "every caveat is one I understand".
  pure def sigOk(c: Cert, now: int): bool =
    not(c.forged) and c.exp > now and not(c.cav.unknown)

  // The consent grants receiver `r` has issued to `issuer`: R-signed,
  // `invoke`, delegable (a non-delegable consent could not be followed
  // by a grant — ADR-0017 "delegable: false means no link may follow").
  pure def consentsFor(consents: Set[Cert], r: int, issuer: int, now: int): Set[Cert] =
    consents.filter(c =>
      c.iss == r and isKey(c.aud, issuer) and c.can == Invoke
      and c.cav.delegable and sigOk(c, now))

  // (2a) Resolve the issuer (ADR-0018): the sovereigns a cert signed by
  // `signer` may be attributed to — the signer itself (a wallet signing
  // directly) plus every wallet with a live speak-as whose `aud` is the
  // signer, whose `cav.verbs` contains the cert's verb and whose
  // `cav.groups` covers every group the cert names. Verification-time
  // validity: an expired speak-as vouches for nothing, whatever the
  // cert's own `exp`. One hop — a speak-as is non-delegable.
  pure def resolve(speakAs: Set[Cert], signer: int, verb: Verb, groups: Set[str], now: int): Set[int] =
    Set(signer).union(
      speakAs.filter(s =>
        s.can == SpeakAs and sigOk(s, now) and isKey(s.aud, signer)
        and s.cav.verbs.contains(verb) and groups.subseteq(s.cav.groups))
      .map(s => s.iss))

  // The groups a grant names: its group audience, if any. A hot key may
  // only address groups its speak-as lets it speak for (literal caveats).
  pure def grantGroups(g: Cert): Set[str] = match g.aud {
    | AGroup(grp) => Set(grp)
    | _ => Set()
  }

  // (2b) on resolved issuers: the consented sovereigns that vouch for
  // the member cert — a live delegable consent from R whose `target`
  // still names R (glossary step 2b; ruled 2026-09-06 with 44r: the Go
  // port checked `target ∋ R` here and the model did not — the model
  // now matches). Empty ⇒ name/groups are stranger-chosen (3cx).
  pure def memberSovereigns(r: int, consents: Set[Cert], speakAs: Set[Cert],
                            member: Cert, now: int): Set[int] =
    resolve(speakAs, member.iss, Member, member.cav.groups, now)
      .filter(w => consentsFor(consents, r, w, now).exists(c => c.cav.target.contains(r)))

  // ---- the N-link chain verifier (protocol ADR-0001) ------------------

  // A fold step's outcome: Go's `(Cert, error)`.
  type Fold = { ok: bool, eff: Cert }

  // attenuate — the Go `cert.Attenuate(parent, child)`, 1:1: the child
  // under its parent is the field-wise intersection over target, facet,
  // groups, verbs and endpoints; `exp` is the min; an unknown caveat on
  // either taints the result; a parent with `delegable: false` admits no
  // following link (ErrNotDelegable). `postage` is monotone: whichever
  // link set it carries forward; both set and different ⇒ taint.
  pure def attenuate(parent: Cert, child: Cert): Fold =
    if (not(parent.cav.delegable)) { ok: false, eff: child }
    else {
      val conflict = parent.cav.postage != NO_POSTAGE and child.cav.postage != NO_POSTAGE
                     and parent.cav.postage != child.cav.postage
      { ok: true,
        eff: child
          .with("exp", if (parent.exp < child.exp) parent.exp else child.exp)
          .with("cav", child.cav
            .with("target", parent.cav.target.intersect(child.cav.target))
            .with("facet", parent.cav.facet.intersect(child.cav.facet))
            .with("groups", parent.cav.groups.intersect(child.cav.groups))
            .with("verbs", parent.cav.verbs.intersect(child.cav.verbs))
            .with("endpoints", parent.cav.endpoints.intersect(child.cav.endpoints))
            .with("postage", if (parent.cav.postage != NO_POSTAGE) parent.cav.postage else child.cav.postage)
            .with("unknown", parent.cav.unknown or child.cav.unknown or conflict)) }
    }

  // One link of the fold. Rule 2 (linkage): the link's signer must
  // resolve — itself, or a principal with a live speak-as to it covering
  // `invoke` and the groups the link names (step 2a) — to the principal
  // the previous link's `aud` names. A forged link, a link with another
  // verb, or a previous link addressed to a group or to `*` ends the
  // chain. Expiry and taint travel in `eff` (min / OR) and are judged on
  // the effective cert.
  pure def linkStep(acc: Fold, c: Cert, speakAs: Set[Cert], now: int): Fold =
    if (not(acc.ok)) acc
    else if (c.forged or c.can != Invoke
             or not(resolve(speakAs, c.iss, Invoke, grantGroups(c), now).exists(p => isKey(acc.eff.aud, p))))
      { ok: false, eff: c }
    else attenuate(acc.eff, c)

  // Rule 4 on the effective cert.
  pure def effAdmits(eff: Cert, r: int, facet: str, now: int): bool =
    eff.cav.target.contains(r) and eff.cav.facet.contains(facet)
    and not(eff.cav.unknown) and eff.exp > now

  // Rule 3 — the last link's `aud` (which `attenuate` keeps, so it is
  // `eff.aud`) against the presenting signer. AudGroup is the sentinel
  // the Go port returns as ErrGroupAud beside `eff`.
  type AudVerdict = AudBound | AudGroup(str) | AudUnbound
  pure def audBinding(eff: Cert, signer: int, speakAs: Set[Cert], now: int): AudVerdict = match eff.aud {
    | AKey(s) =>
      if (s == signer
          or speakAs.exists(sp => sp.iss == s and isKey(sp.aud, signer) and sp.can == SpeakAs
                                  and sigOk(sp, now) and sp.cav.verbs.contains(Invoke)))
        AudBound
      else AudUnbound
    | AAny => if (eff.cav.postage != NO_POSTAGE) AudBound else AudUnbound
    | AGroup(grp) => AudGroup(grp)
  }

  // One verdict per receiver-signed consent that roots the chain: the
  // effective cert, the consent (its `aud` is the sovereign w the talos
  // group rule binds to — Go reads it off `verified`), and the audience
  // verdict.
  type Verdict = { eff: Cert, consent: Cert, aud: AudVerdict }
  pure def chainUnder(c: Cert, chain: List[Cert], speakAs: Set[Cert], r: int,
                      signer: int, facet: str, now: int): { ok: bool, v: Verdict } = {
    val f = chain.foldl({ ok: true, eff: c }, (acc, l) => linkStep(acc, l, speakAs, now))
    { ok: f.ok and effAdmits(f.eff, r, facet, now),
      v: { consent: c, eff: f.eff, aud: audBinding(f.eff, signer, speakAs, now) } }
  }

  // verifyChain — the Go `cert.VerifyChain(receiver, consents, chain,
  // speakAs, signer, facet, now)`. Rule 1: the roots are R's own consents
  // that verify and carry `invoke`; everything else about the consent
  // (target ∋ R, delegable, expiry, taint) is judged by the fold like
  // any other link. An empty caller chain is legal: the consented
  // sovereign presents R's consent alone and binds as its `aud`.
  pure def verifyChain(r: int, consents: Set[Cert], chain: List[Cert], speakAs: Set[Cert],
                       signer: int, facet: str, now: int): Set[Verdict] =
    consents
      .filter(c => c.iss == r and c.can == Invoke and not(c.forged))
      .map(c => chainUnder(c, chain, speakAs, r, signer, facet, now))
      .filter(o => o.ok and match o.v.aud { | AudUnbound => false | _ => true })
      .map(o => o.v)

  // ---- the connection-level check (ADR-0017/0018) on top of it -------

  // One grant admits if `verifyChain` admits the one-link caller chain
  // `[g]` with the QUIC peer as signer and, for a group audience (the
  // AudGroup sentinel), the SAME sovereign w whose consent roots the
  // chain vouches for the member cert and the member's groups contain
  // it. Groups are sovereign-scoped names — never hot-key-scoped, never
  // global (FINDING 2026-09-06).
  pure def grantAdmits(r: int, facet: str, consents: Set[Cert], speakAs: Set[Cert],
                       g: Cert, member: Cert, peer: int, now: int): bool =
    verifyChain(r, consents, [g], speakAs, peer, facet, now).exists(v => match v.aud {
      | AudBound => true
      | AudUnbound => false
      | AudGroup(grp) =>
        member.cav.groups.contains(grp)
        and resolve(speakAs, member.iss, Member, member.cav.groups, now).exists(w => isKey(v.consent.aud, w))
    })

  // authorize(): glossary steps (1)–(4), including (2a) and (2b).
  pure def authorize(r: int, table: str -> str, consents: Set[Cert], blocklist: Set[int],
                     now: int, alpn: str, peer: int, member: Cert, speakAs: Set[Cert],
                     grants: Set[Cert]): Result =
    // (1) ALPN → facet; unknown ⇒ reject. The lookup is TOTAL (NO_FACET
    // for an unknown ALPN), so the guard is defence in depth: dropping it
    // (mutant e1) still rejects at step (4) because no grant names NO_FACET,
    // and mapping an unknown ALPN to a real facet (mutant e1b) is caught by
    // invUnknownAlpnRejects — by the law, never by a Map.get crash.
    val facet = if (table.keys().contains(alpn)) table.get(alpn) else NO_FACET
    if (facet == NO_FACET) Reject
    else {
      // (2) verify member: sig, exp, aud = the QUIC peer key, verb = member
      if (not(member.can == Member and sigOk(member, now) and isKey(member.aud, peer))) Reject
      // (2a)+(2b) resolve the member's issuer through the bundle's speak-as;
      //      some resolved issuer must be one R consented to (3cx)
      else if (memberSovereigns(r, consents, speakAs, member, now) == Set()) Reject
      // (3) blocklist — plain git list keyed by member key in v0
      else if (blocklist.contains(peer)) Reject
      // (3) any grant whose chain verifies admits — grants are alternatives,
      //     one chain each; identity from the member cert ONLY
      else if (grants.exists(g => grantAdmits(r, facet, consents, speakAs, g, member, peer, now)))
        Accept({ key: peer, name: member.cav.name, groups: member.cav.groups })
      // (4) no grant matched
      else Reject
    }

  pure def accepted(res: Result): bool = match res {
    | Reject => false
    | Accept(_) => true
  }

  // ---- adding a caveat to a link (for the monotonicity laws) ---------
  // The first five perturb a grant / chain link; the last three a
  // speak-as link. NOT among them: adding `postage`. Postage is the one
  // caveat that is not an attenuation in the monotone sense — it UNLOCKS
  // `aud: "*"` (fail-closed without it), so adding it can turn a
  // rejected chain into an accepted one. See the report / open questions.
  type Att = ShrinkTarget | ShrinkFacet | ShrinkEndpoints | AddUnknownCaveat | ShortenExpiry
           | ShrinkSpeakAsGroups | ShrinkSpeakAsVerbs | ShortenSpeakAsExpiry
  pure val ATTS = Set(ShrinkTarget, ShrinkFacet, ShrinkEndpoints, AddUnknownCaveat, ShortenExpiry,
                      ShrinkSpeakAsGroups, ShrinkSpeakAsVerbs, ShortenSpeakAsExpiry)

  pure def addCaveat(g: Cert, a: Att, tgt: int, fct: str, ep: str): Cert = match a {
    | ShrinkTarget => g.with("cav", g.cav.with("target", g.cav.target.exclude(Set(tgt))))
    | ShrinkFacet => g.with("cav", g.cav.with("facet", g.cav.facet.exclude(Set(fct))))
    | ShrinkEndpoints => g.with("cav", g.cav.with("endpoints", g.cav.endpoints.exclude(Set(ep))))
    | AddUnknownCaveat => g.with("cav", g.cav.with("unknown", true))
    | ShortenExpiry => g.with("exp", 0)
    | _ => g
  }

  pure def addSpeakAsCaveat(s: Cert, a: Att, grp: str): Cert = match a {
    | ShrinkSpeakAsGroups => s.with("cav", s.cav.with("groups", s.cav.groups.exclude(Set(grp))))
    | ShrinkSpeakAsVerbs => s.with("cav", s.cav.with("verbs", s.cav.verbs.exclude(Set(Member))))
    | ShortenSpeakAsExpiry => s.with("exp", 0)
    | _ => s
  }

  // Perturb chain link `i` (no-op when the chain is shorter).
  pure def addChainCaveat(ch: List[Cert], i: int, a: Att, tgt: int, fct: str, ep: str): List[Cert] =
    if (i < ch.length()) ch.replaceAt(i, addCaveat(ch.nth(i), a, tgt, fct, ep)) else ch

  // Audiences a chain link may name: the next principal, a peer, a group, `*`.
  pure val CHAIN_AUDS = Set(AKey(OWNER2), AKey(CALLER), AKey(ROGUE), AGroup("admins"), AAny)

  // ---- scenario state (regenerated every step) -----------------------
  var alpn: str
  var peer: int
  var blocklist: Set[int]
  var member: Cert
  var speakAs: Set[Cert]     // the bundle's speak-as links (ADR-0018)
  var consents: Set[Cert]
  var grants: Set[Cert]
  var grantsAtt: Set[Cert]   // grants with one link attenuated
  var speakAsAtt: Set[Cert]  // speak-as with one link attenuated (or unchanged)
  // The N-link scenario (ADR-0001), sharing `consents` with the above:
  var chain: List[Cert]           // the links the caller presents (0..3)
  var chainAtt: List[Cert]        // the same with one link attenuated
  var chainSpeakAs: Set[Cert]     // the proof's speak-as links
  var chainSpeakAsAtt: Set[Cert]  // the same with one link attenuated (or unchanged)
  var signer: int                 // the presenting key (envelope `from` / QUIC peer)
  var cfacet: str                 // the facet the caller derived (`to.facet` / AcceptTable[ALPN])

  val res = authorize(R, ACCEPT_TABLE, consents, blocklist, NOW, alpn, peer, member, speakAs, grants)
  val resAtt = authorize(R, ACCEPT_TABLE, consents, blocklist, NOW, alpn, peer, member, speakAsAtt, grantsAtt)
  val chainRes = verifyChain(R, consents, chain, chainSpeakAs, signer, cfacet, NOW)
  val chainResAtt = verifyChain(R, consents, chainAtt, chainSpeakAsAtt, signer, cfacet, NOW)

  pure val CONSENT1 = mkCert(R, AKey(OWNER1), Invoke, Set(R), FACETS, Set(), "", true, false, 10, false)
                        .withEndpoints(ENDPOINTS)
  pure val CONSENT2 = mkCert(R, AKey(OWNER2), Invoke, Set(R), FACETS, Set(), "", true, false, 10, false)
                        .withEndpoints(ENDPOINTS)

  // A 3-link happy path: consent(R→OWNER1) · OWNER1→OWNER2 (via HUB_A) ·
  // OWNER2→CALLER (via HUB_B), presented by CALLER_HOT under a speak-as
  // CALLER→CALLER_HOT (aud-side speak-as). Endpoints narrow along the way.
  pure val LINK1 = mkCert(HUB_A, AKey(OWNER2), Invoke, Set(R), FACETS, Set(), "", true, false, 10, false)
                     .withEndpoints(ENDPOINTS)
  pure val LINK2 = mkCert(HUB_B, AKey(CALLER), Invoke, Set(R), Set("apid"), Set(), "", false, false, 10, false)
                     .withEndpoints(Set("quic:a"))
  pure val CHAIN_SPEAKAS = Set(mkSpeakAs(OWNER1, HUB_A, Set(Invoke), Set(), false, 10, false),
                               mkSpeakAs(OWNER2, HUB_B, Set(Invoke), Set(), false, 10, false),
                               mkSpeakAs(CALLER, CALLER_HOT, Set(Invoke), Set(), false, 10, false))

  // A well-formed happy path (wallet signs directly, no speak-as), so
  // init itself is a meaningful check.
  action init = all {
    alpn' = "mesh/apid/v1",
    peer' = CALLER,
    blocklist' = Set(),
    member' = mkCert(OWNER1, AKey(CALLER), Member, Set(), Set(), Set("admins"), "laptop", false, false, 10, false),
    speakAs' = Set(),
    consents' = Set(CONSENT1),
    grants' = Set(mkCert(OWNER1, AGroup("admins"), Invoke, Set(R), Set("apid"), Set(), "", false, false, 10, false)),
    grantsAtt' = Set(mkCert(OWNER1, AGroup("admins"), Invoke, Set(R), Set("apid"), Set(), "", false, true, 10, false)),
    speakAsAtt' = Set(),
    chain' = [LINK1, LINK2],
    chainAtt' = [LINK1, LINK2.with("cav", LINK2.cav.with("unknown", true))],
    chainSpeakAs' = CHAIN_SPEAKAS,
    chainSpeakAsAtt' = CHAIN_SPEAKAS,
    signer' = CALLER_HOT,
    cfacet' = "apid",
  }

  // `aud: "*"` + postage: a location-record-shaped last link anyone may
  // present (ROGUE does). The attenuated twin has the SAME chain but the
  // aud-side speak-as expired — irrelevant for `*`, so it still accepts;
  // the test below checks fail-closed by dropping postage instead.
  pure val STAR_LINK = mkCert(OWNER1, AAny, Invoke, Set(R), Set("apid"), Set(), "", false, false, 10, false)
                         .withEndpoints(ENDPOINTS).withPostage("pow:20")
  action initStar = all {
    alpn' = "mesh/apid/v1",
    peer' = CALLER,
    blocklist' = Set(),
    member' = mkCert(OWNER1, AKey(CALLER), Member, Set(), Set(), Set("admins"), "laptop", false, false, 10, false),
    speakAs' = Set(),
    consents' = Set(CONSENT1),
    grants' = Set(mkCert(OWNER1, AKey(CALLER), Invoke, Set(R), Set("apid"), Set(), "", false, false, 10, false)),
    grantsAtt' = Set(mkCert(OWNER1, AKey(CALLER), Invoke, Set(R), Set("apid"), Set(), "", false, true, 10, false)),
    speakAsAtt' = Set(),
    chain' = [STAR_LINK],
    chainAtt' = [STAR_LINK.withPostage(NO_POSTAGE)],   // not an attenuation — see Att; used by starPostageTest only
    chainSpeakAs' = Set(),
    chainSpeakAsAtt' = Set(),
    signer' = ROGUE,
    cfacet' = "apid",
  }

  // ADR-0018 law 1 — mixed epochs: member cert by HUB_A, group grant by
  // HUB_B, two valid speak-as from the same wallet, consent to the
  // wallet ⇒ Accept. This is the shape every bundle has after a redeploy.
  action initMixedEpoch = all {
    alpn' = "mesh/apid/v1",
    peer' = CALLER,
    blocklist' = Set(),
    member' = mkCert(HUB_A, AKey(CALLER), Member, Set(), Set(), Set("admins"), "laptop", false, false, 10, false),
    speakAs' = Set(mkSpeakAs(OWNER1, HUB_A, VERBS, GROUPS, false, 10, false),
                   mkSpeakAs(OWNER1, HUB_B, VERBS, GROUPS, false, 10, false)),
    consents' = Set(CONSENT1),
    grants' = Set(mkCert(HUB_B, AGroup("admins"), Invoke, Set(R), Set("apid"), Set(), "", false, false, 10, false)),
    grantsAtt' = Set(mkCert(HUB_B, AGroup("admins"), Invoke, Set(R), Set("apid"), Set(), "", false, false, 10, false)),
    // attenuated: the member's speak-as expired — ADR-0018 axiom 2
    speakAsAtt' = Set(mkSpeakAs(OWNER1, HUB_A, VERBS, GROUPS, false, NOW, false),
                      mkSpeakAs(OWNER1, HUB_B, VERBS, GROUPS, false, 10, false)),
    // consent-only chain: OWNER1 presents R's consent itself
    chain' = [],
    chainAtt' = [],
    chainSpeakAs' = Set(),
    chainSpeakAsAtt' = Set(),
    signer' = OWNER1,
    cfacet' = "kube-api",
  }

  action gen = {
    nondet alpn_ = ALPNS.oneOf()
    nondet peer_ = PEERS.oneOf()
    nondet blk = PEERS.powerset().oneOf()
    // member cert
    nondet mIss = SIGNERS.oneOf()
    nondet mAud = PEERS.oneOf()
    nondet mCan = Set(Member, Invoke).oneOf()
    nondet mName = NAMES.oneOf()
    nondet mGroups = GROUPS.powerset().oneOf()
    nondet mExp = EXPS.oneOf()
    nondet mForged = Bool.oneOf()
    nondet mUnknown = Bool.oneOf()
    // two consents held by the receiver
    nondet c1Iss = RECEIVERS.oneOf()
    nondet c1Aud = WALLETS.oneOf()
    nondet c1Can = Set(Invoke, Member).oneOf()
    nondet c1Target = RECEIVERS.powerset().oneOf()
    nondet c1Facet = FACETS.powerset().oneOf()
    nondet c1Deleg = Bool.oneOf()
    nondet c1Unknown = Bool.oneOf()
    nondet c1Exp = EXPS.oneOf()
    nondet c1Forged = Bool.oneOf()
    nondet c2Iss = RECEIVERS.oneOf()
    nondet c2Aud = WALLETS.oneOf()
    nondet c2Deleg = Bool.oneOf()
    nondet c2Exp = EXPS.oneOf()
    nondet c2Forged = Bool.oneOf()
    // two speak-as links in the bundle (any wallet may sign one)
    nondet s1Iss = WALLETS.oneOf()
    nondet s1Aud = SIGNERS.oneOf()
    nondet s1Can = Set(SpeakAs, Invoke).oneOf()
    nondet s1Verbs = VERBS.powerset().oneOf()
    nondet s1Groups = GROUPS.powerset().oneOf()
    nondet s1Unknown = Bool.oneOf()
    nondet s1Exp = EXPS.oneOf()
    nondet s1Forged = Bool.oneOf()
    nondet s2Iss = WALLETS.oneOf()
    nondet s2Aud = HUBS.oneOf()
    nondet s2Verbs = VERBS.powerset().oneOf()
    nondet s2Groups = GROUPS.powerset().oneOf()
    nondet s2Exp = EXPS.oneOf()
    nondet s2Forged = Bool.oneOf()
    nondet sCount = Set(0, 1, 2).oneOf()
    // two grants presented by the caller
    nondet g1Iss = SIGNERS.oneOf()
    nondet g1Aud = Set(AKey(CALLER), AKey(ROGUE), AGroup("admins"), AGroup("media"), AAny).oneOf()
    nondet g1Can = Set(Invoke, Member, OtherVerb).oneOf()
    nondet g1Target = RECEIVERS.powerset().oneOf()
    nondet g1Facet = FACETS.powerset().oneOf()
    nondet g1Unknown = Bool.oneOf()
    nondet g1Exp = EXPS.oneOf()
    nondet g1Forged = Bool.oneOf()
    nondet g1Postage = POSTAGES.oneOf()
    nondet g2Iss = SIGNERS.oneOf()
    nondet g2Aud = Set(AKey(CALLER), AKey(ROGUE), AGroup("admins"), AGroup("media")).oneOf()
    nondet g2Can = Set(Invoke, OtherVerb).oneOf()
    nondet g2Target = RECEIVERS.powerset().oneOf()
    nondet g2Facet = FACETS.powerset().oneOf()
    nondet g2Exp = EXPS.oneOf()
    nondet g2Forged = Bool.oneOf()
    // the N-link scenario: up to two caller links, the proof's speak-as
    // (one wallet→hot-key, one caller→CALLER_HOT), a signer and a facet
    nondet l1Iss = SIGNERS.oneOf()
    nondet l1Aud = CHAIN_AUDS.oneOf()
    nondet l1Target = RECEIVERS.powerset().oneOf()
    nondet l1Facet = FACETS.powerset().oneOf()
    nondet l1Deleg = Bool.oneOf()
    nondet l1Unknown = Bool.oneOf()
    nondet l1Exp = EXPS.oneOf()
    nondet l1Forged = Bool.oneOf()
    nondet l1Ep = ENDPOINTS.powerset().oneOf()
    nondet l1Postage = POSTAGES.oneOf()
    nondet l2Iss = SIGNERS.union(PEERS).oneOf()
    nondet l2Aud = CHAIN_AUDS.oneOf()
    nondet l2Can = Set(Invoke, OtherVerb).oneOf()
    nondet l2Target = RECEIVERS.powerset().oneOf()
    nondet l2Facet = FACETS.powerset().oneOf()
    nondet l2Deleg = Bool.oneOf()
    nondet l2Exp = EXPS.oneOf()
    nondet l2Forged = Bool.oneOf()
    nondet l2Ep = ENDPOINTS.powerset().oneOf()
    nondet l2Postage = POSTAGES.oneOf()
    nondet cLen = Set(0, 1, 2).oneOf()
    nondet cs1Iss = WALLETS.union(PEERS).oneOf()
    nondet cs1Aud = HOT_KEYS.oneOf()
    nondet cs1Verbs = VERBS.powerset().oneOf()
    nondet cs1Exp = EXPS.oneOf()
    nondet cs1Forged = Bool.oneOf()
    nondet cs2Iss = PEERS.oneOf()
    nondet cs2Verbs = VERBS.powerset().oneOf()
    nondet cs2Exp = EXPS.oneOf()
    nondet csCount = Set(0, 1, 2).oneOf()
    nondet signer_ = SIGNERS.union(PEERS).union(Set(CALLER_HOT)).oneOf()
    nondet cfacet_ = FACETS.oneOf()
    nondet attI = Set(0, 1).oneOf()
    // one attenuation applied to g1 / chain link attI (or to every speak-as)
    nondet att = ATTS.oneOf()
    nondet attT = RECEIVERS.oneOf()
    nondet attF = FACETS.oneOf()
    nondet attG = GROUPS.oneOf()
    nondet attE = ENDPOINTS.oneOf()
    val g1 = mkCert(g1Iss, g1Aud, g1Can, g1Target, g1Facet, Set(), "", false, g1Unknown, g1Exp, g1Forged)
               .withPostage(g1Postage)
    val g2 = mkCert(g2Iss, g2Aud, g2Can, g2Target, g2Facet, Set(), "", false, false, g2Exp, g2Forged)
    val s1 = mkSpeakAs(s1Iss, s1Aud, s1Verbs, s1Groups, s1Unknown, s1Exp, s1Forged).with("can", s1Can)
    val s2 = mkSpeakAs(s2Iss, s2Aud, s2Verbs, s2Groups, false, s2Exp, s2Forged)
    val sa = if (sCount == 0) Set() else if (sCount == 1) Set(s1) else Set(s1, s2)
    val l1 = mkCert(l1Iss, l1Aud, Invoke, l1Target, l1Facet, Set(), "", l1Deleg, l1Unknown, l1Exp, l1Forged)
               .withEndpoints(l1Ep).withPostage(l1Postage)
    val l2 = mkCert(l2Iss, l2Aud, l2Can, l2Target, l2Facet, Set(), "", l2Deleg, false, l2Exp, l2Forged)
               .withEndpoints(l2Ep).withPostage(l2Postage)
    val ch = if (cLen == 0) [] else if (cLen == 1) [l1] else [l1, l2]
    val cs1 = mkSpeakAs(cs1Iss, cs1Aud, cs1Verbs, GROUPS, false, cs1Exp, cs1Forged)
    val cs2 = mkSpeakAs(cs2Iss, CALLER_HOT, cs2Verbs, Set(), false, cs2Exp, false)
    val csa = if (csCount == 0) Set() else if (csCount == 1) Set(cs1) else Set(cs1, cs2)
    all {
      alpn' = alpn_,
      peer' = peer_,
      blocklist' = blk,
      member' = mkCert(mIss, AKey(mAud), mCan, Set(), Set(), mGroups, mName, false, mUnknown, mExp, mForged),
      speakAs' = sa,
      consents' = Set(
        mkCert(c1Iss, AKey(c1Aud), c1Can, c1Target, c1Facet, Set(), "", c1Deleg, c1Unknown, c1Exp, c1Forged),
        mkCert(c2Iss, AKey(c2Aud), Invoke, Set(R), FACETS, Set(), "", c2Deleg, false, c2Exp, c2Forged)),
      grants' = Set(g1, g2),
      grantsAtt' = Set(addCaveat(g1, att, attT, attF, attE), g2),
      speakAsAtt' = sa.map(s => addSpeakAsCaveat(s, att, attG)),
      chain' = ch,
      chainAtt' = addChainCaveat(ch, attI, att, attT, attF, attE),
      chainSpeakAs' = csa,
      chainSpeakAsAtt' = csa.map(s => addSpeakAsCaveat(s, att, attG)),
      signer' = signer_,
      cfacet' = cfacet_,
    }
  }

  // Near-valid scenarios: a correct bundle with at most two faults
  // injected. Pure random generation (`gen`) reaches Accept with
  // probability ~2^-15, so on its own every law holds vacuously — a
  // mutation-test of the model itself showed all seeded bugs surviving.
  // Boundary-biased generation is what makes the laws bite. Half the
  // scenarios are hub-signed (member by HUB_A, grant by HUB_B, two
  // speak-as from OWNER1 — the post-redeploy shape); the FSpeakAs*
  // faults perturb those links.
  type Fault =
    | FNone
    | FAlpnBogus
    | FMemberForged | FMemberExpired | FMemberVerb | FMemberAudRogue
    | FMemberUnknown | FMemberIssuerRogue | FMemberNoGroups
    | FBlocklisted
    | FConsentSignedByOtherR | FConsentNotDelegable | FConsentForged
    | FConsentExpired | FConsentFacetKubeOnly | FConsentTargetOtherR
    | FNoConsentOwner2
    | FGrantForged | FGrantExpired | FGrantVerbOther | FGrantUnknown
    | FGrantIssuerOwner2 | FGrantAudRogueKey | FGrantAudMedia
    | FGrantTargetOtherR | FGrantFacetKubeOnly
    // speak-as faults (hub-signed scenarios; no-ops otherwise)
    | FSpeakAsAMissing | FSpeakAsAExpired | FSpeakAsAForged | FSpeakAsAUnknown
    | FSpeakAsAWrongVerb | FSpeakAsAVerbsInvokeOnly | FSpeakAsAGroupsMedia
    | FSpeakAsAFromOwner2 | FSpeakAsAAudHubB
    | FSpeakAsBMissing | FSpeakAsBExpired | FSpeakAsBVerbsMemberOnly
    | FSpeakAsBGroupsMedia | FSpeakAsBFromOwner2
    | FSpeakAsRogueVouchesBoth
    // N-link chain faults (genNear's chain half; drawn from CFAULTS)
    | FLink1Forged | FLink1Expired | FLink1Unknown | FLink1NotDelegable
    | FLink1IssRogue | FLink1AudRogue | FLink1TargetOtherR | FLink1FacetKubeOnly
    | FLink1EndpointsA | FLink1Postage
    | FLink2Forged | FLink2Expired | FLink2Unknown | FLink2IssRogue | FLink2VerbOther
    | FLink2AudRogue | FLink2FacetKubeOnly | FLink2EndpointsB | FLink2Postage
    | FPostageConflict | FStarNoPostage | FSignerRogue | FDropLink1
    | FAudSpeakAsMissing | FAudSpeakAsExpired | FAudSpeakAsForged
    | FAudSpeakAsNoInvoke | FAudSpeakAsFromRogue
    | FHubANoSpeakAs | FHubBSpeakAsExpired | FHubBSpeakAsInvokeless

  pure val CFAULTS = Set(
    FNone,
    FLink1Forged, FLink1Expired, FLink1Unknown, FLink1NotDelegable,
    FLink1IssRogue, FLink1AudRogue, FLink1TargetOtherR, FLink1FacetKubeOnly,
    FLink1EndpointsA, FLink1Postage,
    FLink2Forged, FLink2Expired, FLink2Unknown, FLink2IssRogue, FLink2VerbOther,
    FLink2AudRogue, FLink2FacetKubeOnly, FLink2EndpointsB, FLink2Postage,
    FPostageConflict, FStarNoPostage, FSignerRogue, FDropLink1,
    FAudSpeakAsMissing, FAudSpeakAsExpired, FAudSpeakAsForged,
    FAudSpeakAsNoInvoke, FAudSpeakAsFromRogue,
    FHubANoSpeakAs, FHubBSpeakAsExpired, FHubBSpeakAsInvokeless)

  // How the last link binds its presenter: to CALLER's key directly; to
  // CALLER with CALLER_HOT presenting under a speak-as; to `*` with
  // postage, presented by ROGUE; to `group:admins` (the AudGroup
  // sentinel — the chain verifies, the talos layer resolves the group);
  // or no caller link at all — OWNER1 presents R's consent itself.
  type ChainKind = KindKey | KindSpeakAs | KindStar | KindGroup | KindConsentOnly
  pure val CHAIN_KINDS = Set(KindKey, KindSpeakAs, KindStar, KindGroup, KindConsentOnly)

  // A correct 2-link caller chain [OWNER1→OWNER2, OWNER2→aud] under
  // consent(R→OWNER1), hub-signed or not, with the chain faults in `f`
  // injected. Endpoints narrow along the chain; postage appears where a
  // fault or the `*` kind puts it.
  pure def nearChain(f: Set[Fault], kind: ChainKind, hubSigned: bool):
      { chain: List[Cert], speakAs: Set[Cert], signer: int } = {
    val p1 = if (f.contains(FLink1Postage)) "pow:20" else NO_POSTAGE
    val p2base = if (f.contains(FLink2Postage) or (kind == KindStar and not(f.contains(FStarNoPostage))))
                 "pow:20" else NO_POSTAGE
    val p2 = if (p2base != NO_POSTAGE and f.contains(FPostageConflict)) "pay:1" else p2base
    val l1 = mkCert(
      if (f.contains(FLink1IssRogue)) ROGUE else if (hubSigned) HUB_A else OWNER1,
      AKey(if (f.contains(FLink1AudRogue)) ROGUE else OWNER2),
      Invoke,
      if (f.contains(FLink1TargetOtherR)) Set(OTHER_R) else Set(R),
      if (f.contains(FLink1FacetKubeOnly)) Set("kube-api") else FACETS,
      Set(), "",
      not(f.contains(FLink1NotDelegable)),
      f.contains(FLink1Unknown),
      if (f.contains(FLink1Expired)) NOW else 10,
      f.contains(FLink1Forged))
      .withEndpoints(if (f.contains(FLink1EndpointsA)) Set("quic:a") else ENDPOINTS)
      .withPostage(p1)
    val l2 = mkCert(
      if (f.contains(FLink2IssRogue)) ROGUE else if (hubSigned) HUB_B else OWNER2,
      match kind {
        | KindStar => AAny
        | KindGroup => AGroup("admins")
        | _ => AKey(if (f.contains(FLink2AudRogue)) ROGUE else CALLER)
      },
      if (f.contains(FLink2VerbOther)) OtherVerb else Invoke,
      Set(R),
      if (f.contains(FLink2FacetKubeOnly)) Set("kube-api") else Set("apid"),
      Set(), "", false,
      f.contains(FLink2Unknown),
      if (f.contains(FLink2Expired)) NOW else 10,
      f.contains(FLink2Forged))
      .withEndpoints(if (f.contains(FLink2EndpointsB)) Set("quic:b") else ENDPOINTS)
      .withPostage(p2)
    // the aud-side speak-as CALLER→CALLER_HOT (KindSpeakAs only)
    val audSa = mkSpeakAs(
      if (f.contains(FAudSpeakAsFromRogue)) ROGUE else CALLER,
      CALLER_HOT,
      if (f.contains(FAudSpeakAsNoInvoke)) Set(Member) else Set(Invoke),
      Set(), false,
      if (f.contains(FAudSpeakAsExpired)) NOW else 10,
      f.contains(FAudSpeakAsForged))
    val hubSa =
      (if (f.contains(FHubANoSpeakAs)) Set() else Set(mkSpeakAs(OWNER1, HUB_A, Set(Invoke), Set(), false, 10, false)))
      .union(Set(mkSpeakAs(OWNER2, HUB_B,
        if (f.contains(FHubBSpeakAsInvokeless)) Set(Member) else Set(Invoke),
        Set(), false,
        if (f.contains(FHubBSpeakAsExpired)) NOW else 10, false)))
    val ch = match kind {
      | KindConsentOnly => []
      | _ => if (f.contains(FDropLink1)) [l2] else [l1, l2]
    }
    { chain: ch,
      speakAs: (if (hubSigned) hubSa else Set())
        .union(match kind { | KindSpeakAs => if (f.contains(FAudSpeakAsMissing)) Set() else Set(audSa) | _ => Set() }),
      signer: match kind {
        | KindKey => if (f.contains(FSignerRogue)) ROGUE else CALLER
        | KindSpeakAs => if (f.contains(FSignerRogue)) ROGUE else CALLER_HOT
        | KindStar => ROGUE
        | KindGroup => CALLER
        | KindConsentOnly => if (f.contains(FSignerRogue)) ROGUE else OWNER1
      } }
  }

  pure val FAULTS = Set(
    FNone, FAlpnBogus,
    FMemberForged, FMemberExpired, FMemberVerb, FMemberAudRogue,
    FMemberUnknown, FMemberIssuerRogue, FMemberNoGroups,
    FBlocklisted,
    FConsentSignedByOtherR, FConsentNotDelegable, FConsentForged,
    FConsentExpired, FConsentFacetKubeOnly, FConsentTargetOtherR,
    FNoConsentOwner2,
    FGrantForged, FGrantExpired, FGrantVerbOther, FGrantUnknown,
    FGrantIssuerOwner2, FGrantAudRogueKey, FGrantAudMedia,
    FGrantTargetOtherR, FGrantFacetKubeOnly,
    FSpeakAsAMissing, FSpeakAsAExpired, FSpeakAsAForged, FSpeakAsAUnknown,
    FSpeakAsAWrongVerb, FSpeakAsAVerbsInvokeOnly, FSpeakAsAGroupsMedia,
    FSpeakAsAFromOwner2, FSpeakAsAAudHubB,
    FSpeakAsBMissing, FSpeakAsBExpired, FSpeakAsBVerbsMemberOnly,
    FSpeakAsBGroupsMedia, FSpeakAsBFromOwner2,
    FSpeakAsRogueVouchesBoth)

  action genNear = {
    nondet f1 = FAULTS.oneOf()
    nondet f2 = FAULTS.oneOf()
    val f = Set(f1, f2)
    // the chain half draws its own two faults; the consent faults above
    // reach it through the shared `consents`
    nondet cf1 = CFAULTS.oneOf()
    nondet cf2 = CFAULTS.oneOf()
    nondet ckind = CHAIN_KINDS.oneOf()
    nondet cHub = Bool.oneOf()
    nondet cPostage = Set(NO_POSTAGE, "pow:20").oneOf()   // postage on the consent itself
    nondet attI = Set(0, 1).oneOf()
    nondet att = ATTS.oneOf()
    nondet attT = RECEIVERS.oneOf()
    nondet attF = FACETS.oneOf()
    nondet attG = GROUPS.oneOf()
    nondet attE = ENDPOINTS.oneOf()
    nondet audKind = Set(0, 1).oneOf()   // 0: grant to the key, 1: grant to group:admins
    nondet hubSigned = Bool.oneOf()      // false: wallets sign directly (pre-ADR-0018 shape)
    val nc = nearChain(Set(cf1, cf2), ckind, cHub)
    val memberSigner = if (hubSigned) HUB_A else OWNER1
    val grantSigner = if (hubSigned) HUB_B else OWNER1
    val consent1 = mkCert(
      if (f.contains(FConsentSignedByOtherR)) OTHER_R else R,
      AKey(OWNER1), Invoke,
      if (f.contains(FConsentTargetOtherR)) Set(OTHER_R) else Set(R),
      if (f.contains(FConsentFacetKubeOnly)) Set("kube-api") else FACETS,
      Set(), "",
      not(f.contains(FConsentNotDelegable)), false,
      if (f.contains(FConsentExpired)) NOW else 10,
      f.contains(FConsentForged))
      .withEndpoints(ENDPOINTS).withPostage(cPostage)
    val g = mkCert(
      if (f.contains(FGrantIssuerOwner2)) OWNER2 else grantSigner,
      if (f.contains(FGrantAudRogueKey)) AKey(ROGUE)
      else if (f.contains(FGrantAudMedia)) AGroup("media")
      else if (audKind == 0) AKey(CALLER) else AGroup("admins"),
      if (f.contains(FGrantVerbOther)) OtherVerb else Invoke,
      if (f.contains(FGrantTargetOtherR)) Set(OTHER_R) else Set(R),
      if (f.contains(FGrantFacetKubeOnly)) Set("kube-api") else Set("apid"),
      Set(), "", false,
      f.contains(FGrantUnknown),
      if (f.contains(FGrantExpired)) NOW else 10,
      f.contains(FGrantForged))
    // speak-as for the member's signer (HUB_A) and the grant's (HUB_B)
    val sA = mkSpeakAs(
      if (f.contains(FSpeakAsAFromOwner2)) OWNER2 else OWNER1,
      if (f.contains(FSpeakAsAAudHubB)) HUB_B else HUB_A,
      if (f.contains(FSpeakAsAVerbsInvokeOnly)) Set(Invoke) else VERBS,
      if (f.contains(FSpeakAsAGroupsMedia)) Set("media") else GROUPS,
      f.contains(FSpeakAsAUnknown),
      if (f.contains(FSpeakAsAExpired)) NOW else 10,
      f.contains(FSpeakAsAForged))
      .with("can", if (f.contains(FSpeakAsAWrongVerb)) Invoke else SpeakAs)
    val sB = mkSpeakAs(
      if (f.contains(FSpeakAsBFromOwner2)) OWNER2 else OWNER1,
      HUB_B,
      if (f.contains(FSpeakAsBVerbsMemberOnly)) Set(Member) else VERBS,
      if (f.contains(FSpeakAsBGroupsMedia)) Set("media") else GROUPS,
      false,
      if (f.contains(FSpeakAsBExpired)) NOW else 10,
      false)
    val rogue = Set(mkSpeakAs(ROGUE, HUB_A, VERBS, GROUPS, false, 10, false),
                    mkSpeakAs(ROGUE, HUB_B, VERBS, GROUPS, false, 10, false))
    val sa =
      if (not(hubSigned)) Set()
      else (if (f.contains(FSpeakAsAMissing)) Set() else Set(sA))
        .union(if (f.contains(FSpeakAsBMissing)) Set() else Set(sB))
        .union(if (f.contains(FSpeakAsRogueVouchesBoth)) rogue else Set())
    all {
      alpn' = if (f.contains(FAlpnBogus)) "mesh/bogus/v1" else "mesh/apid/v1",
      peer' = CALLER,
      blocklist' = if (f.contains(FBlocklisted)) Set(CALLER) else Set(),
      member' = mkCert(
        if (f.contains(FMemberIssuerRogue)) ROGUE else memberSigner,
        AKey(if (f.contains(FMemberAudRogue)) ROGUE else CALLER),
        if (f.contains(FMemberVerb)) Invoke else Member,
        Set(), Set(),
        if (f.contains(FMemberNoGroups)) Set() else Set("admins"),
        "laptop", false,
        f.contains(FMemberUnknown),
        if (f.contains(FMemberExpired)) NOW else 10,
        f.contains(FMemberForged)),
      speakAs' = sa,
      consents' = if (f.contains(FNoConsentOwner2)) Set(consent1) else Set(consent1, CONSENT2),
      grants' = Set(g),
      grantsAtt' = Set(addCaveat(g, att, attT, attF, attE)),
      speakAsAtt' = sa.map(s => addSpeakAsCaveat(s, att, attG)),
      chain' = nc.chain,
      chainAtt' = addChainCaveat(nc.chain, attI, att, attT, attF, attE),
      chainSpeakAs' = nc.speakAs,
      chainSpeakAsAtt' = nc.speakAs.map(s => addSpeakAsCaveat(s, att, attG)),
      signer' = nc.signer,
      cfacet' = "apid",
    }
  }

  action step = any { gen, genNear }

  // ---- laws ----------------------------------------------------------
  // Laws are stated over RAW scenario fields (never via `resolve`), so a
  // bug seeded in authorize() cannot hide behind the same bug in a law.

  // A live R-signed consent naming sovereign `w`.
  def liveConsentTo(w: int): bool =
    consents.exists(c => c.iss == R and isKey(c.aud, w) and sigOk(c, NOW))

  // A speak-as `s` that would let wallet `w` vouch for signer `k` (raw fields).
  pure def vouches(s: Cert, w: int, k: int, now: int): bool =
    s.iss == w and isKey(s.aud, k) and s.can == SpeakAs and sigOk(s, now)

  // Everything a signer could POSSIBLY be attributed to, ignoring every
  // check (the loosest reading) — for necessary-condition laws.
  def attributable(k: int): Set[int] =
    Set(k).union(speakAs.filter(s => isKey(s.aud, k)).map(s => s.iss))

  // Fail-closed (1): an ALPN the receiver never declared admits nothing.
  val invUnknownAlpnRejects =
    not(ACCEPT_TABLE.keys().contains(alpn)) implies not(accepted(res))

  // Receiver-rooted: with no live R-signed cert anywhere, nothing is
  // verifiable — regardless of what Owners or hubs signed.
  val invReceiverRooted =
    not(consents.exists(c => c.iss == R and sigOk(c, NOW))) implies not(accepted(res))

  // A non-delegable consent cannot be followed by a grant.
  val invConsentMustDelegate =
    consents.forall(c => not(c.cav.delegable)) implies not(accepted(res))

  // A consent that does not name the receiver itself in `target` cannot
  // root a chain at that receiver (consent is `invoke {target: self}`).
  val invConsentTargetsSelf =
    consents.forall(c => not(c.cav.target.contains(R))) implies not(accepted(res))

  // Fail-closed (2): a member cert that is forged, expired, wrong verb,
  // not bound to the QUIC peer, or carrying an unknown caveat, admits nothing.
  val invMemberRequired =
    (member.forged or member.exp <= NOW or member.can != Member
      or not(isKey(member.aud, peer)) or member.cav.unknown)
    implies not(accepted(res))

  // Step (2b) on the RESOLVED issuer: some sovereign R consented to must
  // vouch for the member cert — the signer itself, or a wallet whose
  // live speak-as names the signer, covers `member` and the member's
  // groups (3cx + ADR-0018).
  val invMemberIssuerConsented =
    accepted(res) implies WALLETS.exists(w =>
      liveConsentTo(w) and (
        w == member.iss
        or speakAs.exists(s => vouches(s, w, member.iss, NOW)
             and s.cav.verbs.contains(Member)
             and member.cav.groups.subseteq(s.cav.groups))))

  // ADR-0018 law 2 — verification-time validity: a hub-signed member cert
  // whose every speak-as is expired is rejected, whatever member.exp says.
  val invSpeakAsExpiredRejects =
    (HUBS.contains(member.iss)
      and speakAs.forall(s => not(isKey(s.aud, member.iss)) or s.exp <= NOW))
    implies not(accepted(res))

  // ADR-0018 law 3 — a speak-as whose cav.verbs omits `member` cannot
  // resolve a member cert.
  val invSpeakAsVerbScoped =
    (HUBS.contains(member.iss)
      and speakAs.forall(s => not(isKey(s.aud, member.iss)) or not(s.cav.verbs.contains(Member))))
    implies not(accepted(res))

  // ADR-0018 law 4 — a speak-as whose cav.groups does not cover the
  // member's groups cannot resolve it (literal caveats).
  val invSpeakAsGroupsCoverMember =
    (HUBS.contains(member.iss)
      and speakAs.forall(s => not(isKey(s.aud, member.iss)) or not(member.cav.groups.subseteq(s.cav.groups))))
    implies not(accepted(res))

  // ADR-0018 law 5 — receiver-rooted survives the extra link: a speak-as
  // from a wallet R never consented to resolves to nothing.
  val invSpeakAsIssuerConsented =
    (HUBS.contains(member.iss)
      and speakAs.forall(s => not(isKey(s.aud, member.iss)) or not(liveConsentTo(s.iss))))
    implies not(accepted(res))

  // Fail-closed on the link itself: a forged speak-as, a cert with
  // another verb, or one carrying an unknown caveat resolves nothing.
  val invSpeakAsSigRequired =
    (HUBS.contains(member.iss)
      and speakAs.forall(s => not(isKey(s.aud, member.iss))
                              or s.forged or s.can != SpeakAs or s.cav.unknown))
    implies not(accepted(res))

  // The grant's issuer resolves the same way: a hub-signed grant with no
  // live, consented, `invoke`-covering speak-as admits nothing.
  val invGrantSpeakAsRequired =
    grants.forall(g => HUBS.contains(g.iss)
      and speakAs.forall(s => not(isKey(s.aud, g.iss))
                              or s.exp <= NOW or s.forged or s.can != SpeakAs or s.cav.unknown
                              or not(s.cav.verbs.contains(Invoke)) or not(liveConsentTo(s.iss))))
    implies not(accepted(res))

  // Literal caveats on the grant side: a hot key may address `group:g`
  // only under a speak-as whose cav.groups contains g. (Model reading of
  // ADR-0018 "cav.groups ⊆ the policy's group list" — see report.)
  val invGrantSpeakAsGroupsCoverAud =
    grants.forall(g => HUBS.contains(g.iss) and match g.aud {
      | AGroup(grp) => speakAs.forall(s => not(isKey(s.aud, g.iss)) or not(s.cav.groups.contains(grp)))
      | _ => false
    }) implies not(accepted(res))

  // Blocklist is checked on the member key.
  val invBlocklistRejects = blocklist.contains(peer) implies not(accepted(res))

  // Fail-closed (3): if every grant is forged/expired/unknown-verb/unknown-caveat, reject.
  val invGrantsFailClosed =
    grants.forall(g => g.forged or g.exp <= NOW or g.can != Invoke or g.cav.unknown)
    implies not(accepted(res))

  // Issuer-scoped groups, on RESOLVED issuers: if every grant is a group
  // grant whose signer shares no possible sovereign with the member
  // cert's signer, no group can resolve — reject. (A grant from OWNER2
  // to group:admins must not be satisfied by OWNER1's admins cert, even
  // if R consented to both; the same through two hub keys.)
  val invCrossIssuerGroupRejects =
    grants.forall(g => match g.aud {
      | AGroup(_) => attributable(g.iss).intersect(attributable(member.iss)) == Set()
      | _ => false
    }) implies not(accepted(res))

  // FINDING (2026-09-06): a group match must be rooted in ONE consented
  // sovereign that vouches for BOTH the grant's signer and the member's
  // signer — overlap of resolved sets is not enough (a stranger wallet
  // can vouch for any hub key it likes).
  val invGroupMatchRootedInChain =
    accepted(res) implies grants.exists(g => match g.aud {
      | AKey(k) => k == peer
      // `*` at connection level: fail closed unless postage is on the grant
      // or on a consent (ADR-0001 aud binding, uniform with the chain)
      | AAny => g.cav.postage != NO_POSTAGE or consents.exists(c => c.cav.postage != NO_POSTAGE)
      | AGroup(grp) => member.cav.groups.contains(grp) and WALLETS.exists(w =>
          liveConsentTo(w)
          and (w == g.iss or speakAs.exists(s => vouches(s, w, g.iss, NOW)))
          and (w == member.iss or speakAs.exists(s => vouches(s, w, member.iss, NOW))))
    })

  // Target: a grant whose target set omits R never admits at R.
  val invTargetIsReceiver =
    grants.forall(g => not(g.cav.target.contains(R))) implies not(accepted(res))

  // Facet: ALPN routes, grants authorize — the granted facet must
  // contain the one the ALPN selects.
  val invFacetMatchesAlpn =
    (ACCEPT_TABLE.keys().contains(alpn)
      and grants.forall(g => not(g.cav.facet.contains(ACCEPT_TABLE.get(alpn)))))
    implies not(accepted(res))

  // Identity out comes from the member cert only — never from a grant
  // or a speak-as.
  val invIdentityFromMember = match res {
    | Reject => true
    | Accept(id) => id == { key: peer, name: member.cav.name, groups: member.cav.groups }
  }

  // Monotone under attenuation: adding a caveat to a link — a grant's
  // or a speak-as's — can only shrink what is accepted.
  val invMonotone = accepted(resAtt) implies accepted(res)

  // ---- laws on the N-link chain verifier (protocol ADR-0001) ---------
  // Same discipline: raw fields only, never `verifyChain`'s own helpers.
  // "Rejects" laws are stated over the caller's `chain` (and, where the
  // root matters, over every consent); "shape" laws quantify over the
  // verdicts, whose `consent` field pins the root they were folded under.

  def links(v: Verdict): List[Cert] = [v.consent].concat(chain)
  pure def lastOf(l: List[Cert]): Cert = l.nth(l.length() - 1)
  def chainAccepted: bool = chainRes != Set()
  // What a chain link's signer could POSSIBLY be attributed to (loosest reading).
  def chainAttributable(k: int): Set[int] =
    Set(k).union(chainSpeakAs.filter(s => isKey(s.aud, k)).map(s => s.iss))
  // A live speak-as S→k covering `invoke` (raw fields).
  def liveInvokeSpeakAs(s: int, k: int): bool =
    chainSpeakAs.exists(sp => sp.iss == s and isKey(sp.aud, k) and sp.can == SpeakAs
                              and not(sp.forged) and sp.exp > NOW and not(sp.cav.unknown)
                              and sp.cav.verbs.contains(Invoke))

  // Rule 1 — every verdict is rooted in one of R's OWN consents that
  // verifies and carries `invoke`; with none such, nothing verifies.
  val invChainRootIsReceiverConsent = and {
    chainRes.forall(v => consents.contains(v.consent) and v.consent.iss == R
                         and not(v.consent.forged) and v.consent.can == Invoke),
    not(consents.exists(c => c.iss == R and not(c.forged))) implies not(chainAccepted),
  }

  // Rule 2 — linkage: each link's signer is attributable (itself or via
  // a speak-as naming it) to the key the previous link's `aud` names.
  // Corollary: a group or `*` audience is never followed.
  val invChainLinked =
    chainRes.forall(v => links(v).indices().forall(i =>
      i == 0 or chainAttributable(links(v).nth(i).iss).exists(p => isKey(links(v).nth(i - 1).aud, p))))

  // Receiver-rooted survives every link: a chain link signed by a hot
  // key with no live, `invoke`-covering speak-as to it admits nothing.
  val invChainHotKeyNeedsSpeakAs =
    chain.indices().exists(i => HOT_KEYS.contains(chain.nth(i).iss)
      and chainSpeakAs.forall(s => not(isKey(s.aud, chain.nth(i).iss))
                                   or s.forged or s.exp <= NOW or s.can != SpeakAs or s.cav.unknown
                                   or not(s.cav.verbs.contains(Invoke))))
    implies not(chainAccepted)

  // Rule 3 — the last link's `aud` binds the signer: the signer's key, a
  // principal S with a live `speak-as S→signer` covering `invoke`, or `*`
  // with postage somewhere on the chain. A group audience is only ever
  // reported as the AudGroup sentinel, never as bound.
  val invChainAudBindsSigner =
    chainRes.forall(v => match v.aud {
      | AudBound => match lastOf(links(v)).aud {
        | AKey(s) => s == signer or liveInvokeSpeakAs(s, signer)
        | AAny => links(v).indices().exists(i => links(v).nth(i).cav.postage != NO_POSTAGE)
        | AGroup(_) => false
      }
      | AudGroup(grp) => lastOf(links(v)).aud == AGroup(grp)
      | AudUnbound => false
    })

  // `*` is fail-closed: without postage on any link it binds nobody.
  val invStarFailsClosedWithoutPostage =
    (chain.length() > 0 and isAny(lastOf(chain).aud)
      and chain.indices().forall(i => chain.nth(i).cav.postage == NO_POSTAGE)
      and consents.forall(c => c.cav.postage == NO_POSTAGE))
    implies not(chainAccepted)

  // Aud-side speak-as is verb-scoped: `aud: S` presented by another key
  // needs a speak-as S→signer that is live and covers `invoke`.
  val invAudSpeakAsNeedsInvoke =
    (chain.length() > 0 and match lastOf(chain).aud {
      | AKey(s) => s != signer and not(liveInvokeSpeakAs(s, signer))
      | _ => false
    }) implies not(chainAccepted)

  // Attenuation only (invariant 5): the effective cert is below every
  // link in every recognised caveat and in expiry.
  val invEffectiveIsIntersection =
    chainRes.forall(v => links(v).indices().forall(i => {
      val l = links(v).nth(i)
      and {
        v.eff.cav.target.subseteq(l.cav.target),
        v.eff.cav.facet.subseteq(l.cav.facet),
        v.eff.cav.groups.subseteq(l.cav.groups),
        v.eff.cav.endpoints.subseteq(l.cav.endpoints),
        v.eff.exp <= l.exp,
      }
    }))

  // Caveat v2 — endpoints attenuate by exact intersection over the chain.
  val invEndpointsIntersect =
    chainRes.forall(v =>
      v.eff.cav.endpoints == links(v).foldl(ENDPOINTS, (acc, l) => acc.intersect(l.cav.endpoints)))

  // Caveat v2 — postage is monotone: present on any link ⇒ present on
  // the effective cert, and equal to every link that set it.
  val invPostageMonotone =
    chainRes.forall(v => and {
      links(v).indices().exists(i => links(v).nth(i).cav.postage != NO_POSTAGE)
        iff v.eff.cav.postage != NO_POSTAGE,
      links(v).indices().forall(i =>
        links(v).nth(i).cav.postage == NO_POSTAGE or links(v).nth(i).cav.postage == v.eff.cav.postage),
    })

  // Two caller links that both set postage and disagree reject.
  val invPostageConflictRejects =
    chain.indices().exists(i => chain.indices().exists(j =>
      chain.nth(i).cav.postage != NO_POSTAGE and chain.nth(j).cav.postage != NO_POSTAGE
      and chain.nth(i).cav.postage != chain.nth(j).cav.postage))
    implies not(chainAccepted)

  // Fail closed per link.
  val invChainForgedLinkRejects =
    chain.indices().exists(i => chain.nth(i).forged) implies not(chainAccepted)
  val invChainExpiredLinkRejects = and {
    chain.indices().exists(i => chain.nth(i).exp <= NOW) implies not(chainAccepted),
    consents.forall(c => c.exp <= NOW) implies not(chainAccepted),
  }
  val invChainUnknownCaveatRejects = and {
    chain.indices().exists(i => chain.nth(i).cav.unknown) implies not(chainAccepted),
    consents.forall(c => c.cav.unknown) implies not(chainAccepted),
  }
  val invChainVerbIsInvoke =
    chain.indices().exists(i => chain.nth(i).can != Invoke) implies not(chainAccepted)

  // `delegable: false` admits no following link — on a caller link or on
  // the consent itself.
  val invChainNotDelegableStops = and {
    chain.indices().exists(i => i < chain.length() - 1 and not(chain.nth(i).cav.delegable))
      implies not(chainAccepted),
    (chain.length() > 0 and consents.forall(c => not(c.cav.delegable))) implies not(chainAccepted),
  }

  // Rule 4 — target and facet: every link must name R and the facet
  // (the intersection does iff each does).
  val invChainTargetsReceiver =
    chainAccepted implies (chain.indices().forall(i => chain.nth(i).cav.target.contains(R))
                           and consents.exists(c => c.cav.target.contains(R)))
  val invChainFacetMatches =
    chainAccepted implies (chain.indices().forall(i => chain.nth(i).cav.facet.contains(cfacet))
                           and consents.exists(c => c.cav.facet.contains(cfacet)))

  // Monotone under attenuation: adding a caveat (other than postage —
  // see Att) to any chain link or speak-as can only shrink acceptance.
  val invChainMonotone = (chainResAtt != Set()) implies chainAccepted

  val invAll = and {
    invUnknownAlpnRejects,
    invReceiverRooted,
    invConsentMustDelegate,
    invConsentTargetsSelf,
    invMemberRequired,
    invMemberIssuerConsented,
    invSpeakAsExpiredRejects,
    invSpeakAsVerbScoped,
    invSpeakAsGroupsCoverMember,
    invSpeakAsIssuerConsented,
    invSpeakAsSigRequired,
    invGrantSpeakAsRequired,
    invGrantSpeakAsGroupsCoverAud,
    invBlocklistRejects,
    invGrantsFailClosed,
    invCrossIssuerGroupRejects,
    invGroupMatchRootedInChain,
    invTargetIsReceiver,
    invFacetMatchesAlpn,
    invIdentityFromMember,
    invMonotone,
    // N-link chain (ADR-0001)
    invChainRootIsReceiverConsent,
    invChainLinked,
    invChainHotKeyNeedsSpeakAs,
    invChainAudBindsSigner,
    invStarFailsClosedWithoutPostage,
    invAudSpeakAsNeedsInvoke,
    invEffectiveIsIntersection,
    invEndpointsIntersect,
    invPostageMonotone,
    invPostageConflictRejects,
    invChainForgedLinkRejects,
    invChainExpiredLinkRejects,
    invChainUnknownCaveatRejects,
    invChainVerbIsInvoke,
    invChainNotDelegableStops,
    invChainTargetsReceiver,
    invChainFacetMatches,
    invChainMonotone,
  }

  // Sanity: the happy path must actually accept, otherwise every law
  // above would hold vacuously.
  run happyPathTest = init.then(all {
    assert(accepted(res)),
    assert(not(accepted(resAtt))),   // the unknown-caveat attenuation rejects
    step,
  })

  // ADR-0018 law 1: mixed epochs accept; expiring the member's speak-as
  // alone (law 2) rejects, with member.exp untouched.
  run mixedEpochTest = initMixedEpoch.then(all {
    assert(accepted(res)),
    assert(not(accepted(resAtt))),
    step,
  })

  // ADR-0001 witness: the 3-link hub-signed chain presented by the
  // caller's hot key accepts; its effective cert is the intersection
  // (endpoints narrowed to quic:a, facet to apid, exp 10); tainting the
  // last link rejects.
  run chainHappyPathTest = init.then(all {
    assert(chainAccepted),
    assert(chainRes.forall(v => v.eff.cav.endpoints == Set("quic:a") and v.eff.cav.facet == Set("apid")
                                and v.eff.cav.target == Set(R) and v.eff.exp == 10
                                and v.aud == AudBound and v.consent == CONSENT1)),
    assert(chainResAtt == Set()),
    // the same chain presented by a key nobody vouches for is unbound
    assert(verifyChain(R, consents, chain, chainSpeakAs, ROGUE, cfacet, NOW) == Set()),
    // and, with the aud-side speak-as lacking `invoke`, too
    assert(verifyChain(R, consents, chain,
      Set(mkSpeakAs(OWNER1, HUB_A, Set(Invoke), Set(), false, 10, false),
          mkSpeakAs(OWNER2, HUB_B, Set(Invoke), Set(), false, 10, false),
          mkSpeakAs(CALLER, CALLER_HOT, Set(Member), Set(), false, 10, false)),
      CALLER_HOT, cfacet, NOW) == Set()),
    step,
  })

  // ADR-0001 witness: `aud: "*"` binds anyone when postage is present
  // and nobody when it is not; a conflicting postage on the consent taints.
  run starPostageTest = initStar.then(all {
    assert(chainAccepted),
    assert(chainRes.forall(v => v.eff.cav.postage == "pow:20")),
    assert(chainResAtt == Set()),
    assert(verifyChain(R, Set(CONSENT1.withPostage("pay:1")), chain, Set(), ROGUE, cfacet, NOW) == Set()),
    assert(verifyChain(R, Set(CONSENT1.withPostage("pow:20")), chain, Set(), ROGUE, cfacet, NOW) != Set()),
    // the connection-level check binds `*` the same way (postage on the grant)
    assert(accepted(authorize(R, ACCEPT_TABLE, Set(CONSENT1), Set(), NOW, "mesh/apid/v1", CALLER, member, Set(),
                              Set(STAR_LINK)))),
    assert(not(accepted(authorize(R, ACCEPT_TABLE, Set(CONSENT1), Set(), NOW, "mesh/apid/v1", CALLER, member, Set(),
                                  Set(STAR_LINK.withPostage(NO_POSTAGE)))))),
    step,
  })

  // Emergent from the fold: an empty caller chain is the consented
  // sovereign presenting R's consent itself — it binds as the consent's
  // `aud` (or via its hot key), and never to a stranger.
  run consentOnlyTest = initMixedEpoch.then(all {
    assert(chainAccepted),
    assert(chainRes.forall(v => v.eff == CONSENT1)),
    assert(verifyChain(R, consents, [], Set(mkSpeakAs(OWNER1, HUB_A, Set(Invoke), Set(), false, 10, false)),
                       HUB_A, cfacet, NOW) != Set()),
    assert(verifyChain(R, consents, [], Set(), OWNER2, cfacet, NOW) == Set()),
    step,
  })
}
