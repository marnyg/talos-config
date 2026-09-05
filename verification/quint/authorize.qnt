// authorize.qnt — the per-connect check of ADR-0017 + ADR-0018 (talos-config-czi).
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
// FINDING (2026-09-06, model-first, NOT yet ruled — see the report for
// czi): ADR-0018 axiom 1 and the glossary say the group rule "compares
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
// member cert. `authorize` below does that (`grantAdmits` quantifies one
// `w`); invGroupMatchRootedInChain is the law; mutant m14 (overlap
// instead of the same `w`) is the regression test. Proposed glossary
// wording: "the group rule is satisfied when one sovereign W that R
// holds a live consent for both (i) vouches for the grant's signer and
// (ii) vouches for the member cert's signer, and the member's groups
// contain g" — never "resolved issuers are equal".
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

  pure val WALLETS = Set(OWNER1, OWNER2, ROGUE)  // sovereign roots — what receivers consent to
  pure val HUBS = Set(HUB_A, HUB_B)              // hot keys — hold authority only via speak-as
  pure val SIGNERS = WALLETS.union(HUBS)         // may sign member / invoke certs
  pure val PEERS = Set(CALLER, ROGUE)
  pure val RECEIVERS = Set(R, OTHER_R)
  pure val FACETS = Set("apid", "kube-api")
  pure val NO_FACET = ""          // total-lookup sentinel: no grant's cav.facet ever contains it
  pure val GROUPS = Set("admins", "media")
  pure val NAMES = Set("tv", "laptop")
  pure val NOW = 5
  pure val EXPS = Set(0, 5, 10)  // expired / at the boundary / live relative to NOW

  // The receiver's accept table: ALPN class → facet. Producer-owned,
  // closed. The third ALPN is one this receiver never declared.
  pure val ACCEPT_TABLE = Map("mesh/apid/v1" -> "apid", "mesh/kube/v1" -> "kube-api")
  pure val ALPNS = ACCEPT_TABLE.keys().union(Set("mesh/bogus/v1"))

  // ---- the one primitive -------------------------------------------
  type Verb = Member | Invoke | SpeakAs | OtherVerb   // OtherVerb: any verb outside this verifier's closed set
  pure val VERBS = Set(Member, Invoke)                // what a speak-as may caveat over (ADR-0018: literal)
  type Aud = AKey(int) | AGroup(str)
  // `unknown` stands for "carries a caveat this verifier does not
  // recognise" (ADR-0017: an unknown caveat rejects). `verbs` is the
  // speak-as caveat: which verbs the hot key may sign as the wallet.
  type Cav = { target: Set[int], facet: Set[str], groups: Set[str], verbs: Set[Verb],
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
             delegable: delegable, unknown: unknown } }

  // {iss: wallet, aud: hubkey, can: speak-as, cav: {verbs, groups, delegable: false}, exp}
  pure def mkSpeakAs(wallet: int, hubkey: int, verbs: Set[Verb], groups: Set[str],
                     unknown: bool, exp: int, forged: bool): Cert =
    { iss: wallet, aud: AKey(hubkey), can: SpeakAs, exp: exp, forged: forged,
      cav: { target: Set(), facet: Set(), groups: groups, verbs: verbs, name: "",
             delegable: false, unknown: unknown } }

  pure def isKey(a: Aud, k: int): bool = match a {
    | AKey(x) => x == k
    | AGroup(_) => false
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
    | AKey(_) => Set()
    | AGroup(grp) => Set(grp)
  }

  // (2b) on resolved issuers: the consented sovereigns that vouch for
  // the member cert. Empty ⇒ name/groups are stranger-chosen (3cx).
  pure def memberSovereigns(r: int, consents: Set[Cert], speakAs: Set[Cert],
                            member: Cert, now: int): Set[int] =
    resolve(speakAs, member.iss, Member, member.cav.groups, now)
      .filter(w => consentsFor(consents, r, w, now) != Set())

  // One grant admits if, for ONE sovereign w that R consented to, the
  // chain [consent(R→w), speak-as(w→g.iss)?, g] verifies, the field-wise
  // intersection still names R and the facet, and the audience resolves:
  // a key audience is the QUIC peer; a group audience is satisfied by a
  // member cert that the SAME w vouches for and whose groups contain it.
  // Groups are sovereign-scoped names — never hot-key-scoped, never global.
  pure def grantAdmits(r: int, facet: str, consents: Set[Cert], speakAs: Set[Cert],
                       g: Cert, member: Cert, peer: int, now: int): bool =
    g.can == Invoke and sigOk(g, now)
    and resolve(speakAs, g.iss, Invoke, grantGroups(g), now).exists(w =>
      consentsFor(consents, r, w, now).exists(c =>
        c.cav.target.intersect(g.cav.target).contains(r)
        and c.cav.facet.intersect(g.cav.facet).contains(facet))
      and match g.aud {
        | AKey(k) => k == peer
        | AGroup(grp) =>
          resolve(speakAs, member.iss, Member, member.cav.groups, now).contains(w)
          and member.cav.groups.contains(grp)
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
      // (3) any grant whose chain verifies admits; identity from the member cert ONLY
      else if (grants.exists(g => grantAdmits(r, facet, consents, speakAs, g, member, peer, now)))
        Accept({ key: peer, name: member.cav.name, groups: member.cav.groups })
      // (4) no grant matched
      else Reject
    }

  pure def accepted(res: Result): bool = match res {
    | Reject => false
    | Accept(_) => true
  }

  // ---- attenuation: a link adds caveats, never removes ---------------
  // The first four attenuate a grant link; the last three a speak-as link.
  type Att = ShrinkTarget | ShrinkFacet | AddUnknownCaveat | ShortenExpiry
           | ShrinkSpeakAsGroups | ShrinkSpeakAsVerbs | ShortenSpeakAsExpiry
  pure val ATTS = Set(ShrinkTarget, ShrinkFacet, AddUnknownCaveat, ShortenExpiry,
                      ShrinkSpeakAsGroups, ShrinkSpeakAsVerbs, ShortenSpeakAsExpiry)

  pure def attenuate(g: Cert, a: Att, tgt: int, fct: str): Cert = match a {
    | ShrinkTarget => g.with("cav", g.cav.with("target", g.cav.target.exclude(Set(tgt))))
    | ShrinkFacet => g.with("cav", g.cav.with("facet", g.cav.facet.exclude(Set(fct))))
    | AddUnknownCaveat => g.with("cav", g.cav.with("unknown", true))
    | ShortenExpiry => g.with("exp", 0)
    | _ => g
  }

  pure def attenuateSpeakAs(s: Cert, a: Att, grp: str): Cert = match a {
    | ShrinkSpeakAsGroups => s.with("cav", s.cav.with("groups", s.cav.groups.exclude(Set(grp))))
    | ShrinkSpeakAsVerbs => s.with("cav", s.cav.with("verbs", s.cav.verbs.exclude(Set(Member))))
    | ShortenSpeakAsExpiry => s.with("exp", 0)
    | _ => s
  }

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

  val res = authorize(R, ACCEPT_TABLE, consents, blocklist, NOW, alpn, peer, member, speakAs, grants)
  val resAtt = authorize(R, ACCEPT_TABLE, consents, blocklist, NOW, alpn, peer, member, speakAsAtt, grantsAtt)

  pure val CONSENT1 = mkCert(R, AKey(OWNER1), Invoke, Set(R), FACETS, Set(), "", true, false, 10, false)
  pure val CONSENT2 = mkCert(R, AKey(OWNER2), Invoke, Set(R), FACETS, Set(), "", true, false, 10, false)

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
    nondet g1Aud = Set(AKey(CALLER), AKey(ROGUE), AGroup("admins"), AGroup("media")).oneOf()
    nondet g1Can = Set(Invoke, Member, OtherVerb).oneOf()
    nondet g1Target = RECEIVERS.powerset().oneOf()
    nondet g1Facet = FACETS.powerset().oneOf()
    nondet g1Unknown = Bool.oneOf()
    nondet g1Exp = EXPS.oneOf()
    nondet g1Forged = Bool.oneOf()
    nondet g2Iss = SIGNERS.oneOf()
    nondet g2Aud = Set(AKey(CALLER), AKey(ROGUE), AGroup("admins"), AGroup("media")).oneOf()
    nondet g2Can = Set(Invoke, OtherVerb).oneOf()
    nondet g2Target = RECEIVERS.powerset().oneOf()
    nondet g2Facet = FACETS.powerset().oneOf()
    nondet g2Exp = EXPS.oneOf()
    nondet g2Forged = Bool.oneOf()
    // one attenuation applied to g1 (or to every speak-as)
    nondet att = ATTS.oneOf()
    nondet attT = RECEIVERS.oneOf()
    nondet attF = FACETS.oneOf()
    nondet attG = GROUPS.oneOf()
    val g1 = mkCert(g1Iss, g1Aud, g1Can, g1Target, g1Facet, Set(), "", false, g1Unknown, g1Exp, g1Forged)
    val g2 = mkCert(g2Iss, g2Aud, g2Can, g2Target, g2Facet, Set(), "", false, false, g2Exp, g2Forged)
    val s1 = mkSpeakAs(s1Iss, s1Aud, s1Verbs, s1Groups, s1Unknown, s1Exp, s1Forged).with("can", s1Can)
    val s2 = mkSpeakAs(s2Iss, s2Aud, s2Verbs, s2Groups, false, s2Exp, s2Forged)
    val sa = if (sCount == 0) Set() else if (sCount == 1) Set(s1) else Set(s1, s2)
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
      grantsAtt' = Set(attenuate(g1, att, attT, attF), g2),
      speakAsAtt' = sa.map(s => attenuateSpeakAs(s, att, attG)),
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
    nondet att = ATTS.oneOf()
    nondet attT = RECEIVERS.oneOf()
    nondet attF = FACETS.oneOf()
    nondet attG = GROUPS.oneOf()
    nondet audKind = Set(0, 1).oneOf()   // 0: grant to the key, 1: grant to group:admins
    nondet hubSigned = Bool.oneOf()      // false: wallets sign directly (pre-ADR-0018 shape)
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
      grantsAtt' = Set(attenuate(g, att, attT, attF)),
      speakAsAtt' = sa.map(s => attenuateSpeakAs(s, att, attG)),
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
      | AKey(_) => false
      | AGroup(grp) => speakAs.forall(s => not(isKey(s.aud, g.iss)) or not(s.cav.groups.contains(grp)))
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
      | AKey(_) => false
      | AGroup(_) => attributable(g.iss).intersect(attributable(member.iss)) == Set()
    }) implies not(accepted(res))

  // FINDING (2026-09-06): a group match must be rooted in ONE consented
  // sovereign that vouches for BOTH the grant's signer and the member's
  // signer — overlap of resolved sets is not enough (a stranger wallet
  // can vouch for any hub key it likes).
  val invGroupMatchRootedInChain =
    accepted(res) implies grants.exists(g => match g.aud {
      | AKey(k) => k == peer
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
}
