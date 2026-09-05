// authorize.qnt — the per-connect check of ADR-0017 (talos-config-czi).
//
// Models domain-model.md glossary "Authorize", "Attenuation", "Grant",
// "Consent grant", "Group", "Facet" as ONE pure function, then throws
// random worlds at it: forged/expired/unknown certs, consents signed
// by the wrong receiver, group grants from a different sovereign than
// the member cert, blocklisted peers, ALPNs the receiver never
// declared. This is the spec the Go `authorize()` (0bc.1 / 359.8.1)
// must match; the rapid suite there checks the same laws over real
// signatures.
//
// Shape of the model: every step regenerates a complete scenario
// (receiver consents, caller bundle, ALPN, blocklist, one attenuation
// of one grant) and the invariants are laws over that scenario. Because
// no state carries across steps, `verify --max-steps=2` already covers
// the full space Apalache can reach; `run` adds random breadth.
//
// Laws checked (glossary "Authorize" → Properties):
//   deterministic & offline    by construction: `authorize` is a pure def
//                              whose inputs are exactly the glossary's
//                              (receiver key, accept table, consents,
//                              blocklist, ALPN, bundle) — the name map
//                              is NOT among them.
//   receiver-rooted            invReceiverRooted, invConsentMustDelegate
//   monotone under attenuation invMonotone
//   fail-closed on unknown     invUnknownAlpnRejects, invGrantsFailClosed,
//                              invMemberRequired
//   identity from member only  invIdentityFromMember
//   issuer-scoped groups       invCrossIssuerGroupRejects
//   target/facet in chain      invTargetIsReceiver, invFacetMatchesAlpn
//   blocklist                  invBlocklistRejects
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
// Time (ADR-0019, talos-config-439): `NOW` here is the verifier's
// EFFECTIVE clock, max(local clock, low-water mark over verified `iat`s).
// The primitive carries `iat`; how the mark is maintained and what it
// bounds is clock.qnt's subject — authorize() only consumes the result,
// so `iat` is not a field of this model's Cert.
//
// Check:
//   quint run    --invariant=invAll --max-steps=30 authorize.qnt
//   quint verify --invariant=invAll --max-steps=2  authorize.qnt
module authorize {
  // ---- closed world -------------------------------------------------
  pure val OWNER1 = 1        // a sovereign R consents to (usually)
  pure val OWNER2 = 2        // a second sovereign — N>1 is a design goal
  pure val R = 3             // the receiver under test
  pure val OTHER_R = 4       // another receiver; its consents must not count for R
  pure val CALLER = 5        // the QUIC peer
  pure val ROGUE = 6         // signs things it has no authority over

  pure val ISSUERS = Set(OWNER1, OWNER2, ROGUE)
  pure val PEERS = Set(CALLER, ROGUE)
  pure val RECEIVERS = Set(R, OTHER_R)
  pure val FACETS = Set("apid", "kube-api")
  pure val GROUPS = Set("admins", "media")
  pure val NAMES = Set("tv", "laptop")
  pure val NOW = 5
  pure val EXPS = Set(0, 5, 10)  // expired / at the boundary / live relative to NOW

  // The receiver's accept table: ALPN class → facet. Producer-owned,
  // closed. The third ALPN is one this receiver never declared.
  pure val ACCEPT_TABLE = Map("mesh/apid/v1" -> "apid", "mesh/kube/v1" -> "kube-api")
  pure val ALPNS = ACCEPT_TABLE.keys().union(Set("mesh/bogus/v1"))

  // ---- the one primitive -------------------------------------------
  type Verb = Member | Invoke | OtherVerb   // OtherVerb: any verb outside this verifier's closed set
  type Aud = AKey(int) | AGroup(str)
  // `unknown` stands for "carries a caveat this verifier does not
  // recognise" (ADR-0017: an unknown caveat rejects).
  type Cav = { target: Set[int], facet: Set[str], groups: Set[str],
               name: str, delegable: bool, unknown: bool }
  // `forged`: the signature does not verify under `iss`.
  type Cert = { iss: int, aud: Aud, can: Verb, cav: Cav, exp: int, forged: bool }
  type Identity = { key: int, name: str, groups: Set[str] }
  type Result = Reject | Accept(Identity)

  pure def mkCert(iss: int, aud: Aud, can: Verb, target: Set[int], facet: Set[str],
                  groups: Set[str], name: str, delegable: bool, unknown: bool,
                  exp: int, forged: bool): Cert =
    { iss: iss, aud: aud, can: can, exp: exp, forged: forged,
      cav: { target: target, facet: facet, groups: groups, name: name,
             delegable: delegable, unknown: unknown } }

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

  // Group resolution rule: `aud: group:<g>` is satisfied by a member
  // cert whose `iss` is the SAME key as the grant's `iss` and whose
  // groups contain g. Groups are issuer-scoped names, never global.
  pure def audSatisfied(g: Cert, member: Cert, peer: int): bool = match g.aud {
    | AKey(k) => k == peer
    | AGroup(grp) => g.iss == member.iss and member.cav.groups.contains(grp)
  }

  // One grant admits if the chain [consent(R→g.iss), g] verifies and
  // the field-wise intersection still names R and the facet.
  pure def grantAdmits(r: int, facet: str, consents: Set[Cert], g: Cert,
                       member: Cert, peer: int, now: int): bool =
    g.can == Invoke and sigOk(g, now)
    and consentsFor(consents, r, g.iss, now).exists(c =>
      c.cav.target.intersect(g.cav.target).contains(r)
      and c.cav.facet.intersect(g.cav.facet).contains(facet))
    and audSatisfied(g, member, peer)

  // authorize(): glossary steps (1)–(4), including (2b).
  pure def authorize(r: int, table: str -> str, consents: Set[Cert], blocklist: Set[int],
                     now: int, alpn: str, peer: int, member: Cert, grants: Set[Cert]): Result =
    // (1) ALPN → facet; unknown ⇒ reject
    if (not(table.keys().contains(alpn))) Reject
    else {
      val facet = table.get(alpn)
      // (2) verify member: sig, exp, aud = the QUIC peer key, verb = member
      if (not(member.can == Member and sigOk(member, now) and isKey(member.aud, peer))) Reject
      // (2b) the member cert must come from an issuer R consented to,
      //      otherwise name/groups are stranger-chosen (3cx)
      else if (consentsFor(consents, r, member.iss, now) == Set()) Reject
      // (3) blocklist — plain git list keyed by member key in v0
      else if (blocklist.contains(peer)) Reject
      // (3) any grant whose chain verifies admits; identity from the member cert ONLY
      else if (grants.exists(g => grantAdmits(r, facet, consents, g, member, peer, now)))
        Accept({ key: peer, name: member.cav.name, groups: member.cav.groups })
      // (4) no grant matched
      else Reject
    }

  pure def accepted(res: Result): bool = match res {
    | Reject => false
    | Accept(_) => true
  }

  // ---- attenuation: a link adds caveats, never removes ---------------
  type Att = ShrinkTarget | ShrinkFacet | AddUnknownCaveat | ShortenExpiry

  pure def attenuate(g: Cert, a: Att, tgt: int, fct: str): Cert = match a {
    | ShrinkTarget => g.with("cav", g.cav.with("target", g.cav.target.exclude(Set(tgt))))
    | ShrinkFacet => g.with("cav", g.cav.with("facet", g.cav.facet.exclude(Set(fct))))
    | AddUnknownCaveat => g.with("cav", g.cav.with("unknown", true))
    | ShortenExpiry => g.with("exp", 0)
  }

  // ---- scenario state (regenerated every step) -----------------------
  var alpn: str
  var peer: int
  var blocklist: Set[int]
  var member: Cert
  var consents: Set[Cert]
  var grants: Set[Cert]
  var grantsAtt: Set[Cert]   // grants with one link attenuated

  val res = authorize(R, ACCEPT_TABLE, consents, blocklist, NOW, alpn, peer, member, grants)
  val resAtt = authorize(R, ACCEPT_TABLE, consents, blocklist, NOW, alpn, peer, member, grantsAtt)

  // A well-formed happy path, so init itself is a meaningful check.
  action init = all {
    alpn' = "mesh/apid/v1",
    peer' = CALLER,
    blocklist' = Set(),
    member' = mkCert(OWNER1, AKey(CALLER), Member, Set(), Set(), Set("admins"), "laptop", false, false, 10, false),
    consents' = Set(mkCert(R, AKey(OWNER1), Invoke, Set(R), FACETS, Set(), "", true, false, 10, false)),
    grants' = Set(mkCert(OWNER1, AGroup("admins"), Invoke, Set(R), Set("apid"), Set(), "", false, false, 10, false)),
    grantsAtt' = Set(mkCert(OWNER1, AGroup("admins"), Invoke, Set(R), Set("apid"), Set(), "", false, true, 10, false)),
  }

  action gen = {
    nondet alpn_ = ALPNS.oneOf()
    nondet peer_ = PEERS.oneOf()
    nondet blk = PEERS.powerset().oneOf()
    // member cert
    nondet mIss = ISSUERS.oneOf()
    nondet mAud = PEERS.oneOf()
    nondet mCan = Set(Member, Invoke).oneOf()
    nondet mName = NAMES.oneOf()
    nondet mGroups = GROUPS.powerset().oneOf()
    nondet mExp = EXPS.oneOf()
    nondet mForged = Bool.oneOf()
    nondet mUnknown = Bool.oneOf()
    // two consents held by the receiver
    nondet c1Iss = RECEIVERS.oneOf()
    nondet c1Aud = ISSUERS.oneOf()
    nondet c1Can = Set(Invoke, Member).oneOf()
    nondet c1Target = RECEIVERS.powerset().oneOf()
    nondet c1Facet = FACETS.powerset().oneOf()
    nondet c1Deleg = Bool.oneOf()
    nondet c1Unknown = Bool.oneOf()
    nondet c1Exp = EXPS.oneOf()
    nondet c1Forged = Bool.oneOf()
    nondet c2Iss = RECEIVERS.oneOf()
    nondet c2Aud = ISSUERS.oneOf()
    nondet c2Deleg = Bool.oneOf()
    nondet c2Exp = EXPS.oneOf()
    nondet c2Forged = Bool.oneOf()
    // two grants presented by the caller
    nondet g1Iss = ISSUERS.oneOf()
    nondet g1Aud = Set(AKey(CALLER), AKey(ROGUE), AGroup("admins"), AGroup("media")).oneOf()
    nondet g1Can = Set(Invoke, Member, OtherVerb).oneOf()
    nondet g1Target = RECEIVERS.powerset().oneOf()
    nondet g1Facet = FACETS.powerset().oneOf()
    nondet g1Unknown = Bool.oneOf()
    nondet g1Exp = EXPS.oneOf()
    nondet g1Forged = Bool.oneOf()
    nondet g2Iss = ISSUERS.oneOf()
    nondet g2Aud = Set(AKey(CALLER), AKey(ROGUE), AGroup("admins"), AGroup("media")).oneOf()
    nondet g2Can = Set(Invoke, OtherVerb).oneOf()
    nondet g2Target = RECEIVERS.powerset().oneOf()
    nondet g2Facet = FACETS.powerset().oneOf()
    nondet g2Exp = EXPS.oneOf()
    nondet g2Forged = Bool.oneOf()
    // one attenuation applied to g1
    nondet att = Set(ShrinkTarget, ShrinkFacet, AddUnknownCaveat, ShortenExpiry).oneOf()
    nondet attT = RECEIVERS.oneOf()
    nondet attF = FACETS.oneOf()
    val g1 = mkCert(g1Iss, g1Aud, g1Can, g1Target, g1Facet, Set(), "", false, g1Unknown, g1Exp, g1Forged)
    val g2 = mkCert(g2Iss, g2Aud, g2Can, g2Target, g2Facet, Set(), "", false, false, g2Exp, g2Forged)
    all {
      alpn' = alpn_,
      peer' = peer_,
      blocklist' = blk,
      member' = mkCert(mIss, AKey(mAud), mCan, Set(), Set(), mGroups, mName, false, mUnknown, mExp, mForged),
      consents' = Set(
        mkCert(c1Iss, AKey(c1Aud), c1Can, c1Target, c1Facet, Set(), "", c1Deleg, c1Unknown, c1Exp, c1Forged),
        mkCert(c2Iss, AKey(c2Aud), Invoke, Set(R), FACETS, Set(), "", c2Deleg, false, c2Exp, c2Forged)),
      grants' = Set(g1, g2),
      grantsAtt' = Set(attenuate(g1, att, attT, attF), g2),
    }
  }

  // Near-valid scenarios: a correct bundle with at most two faults
  // injected. Pure random generation (`gen`) reaches Accept with
  // probability ~2^-15, so on its own every law holds vacuously — a
  // mutation-test of the model itself showed all seeded bugs surviving.
  // Boundary-biased generation is what makes the laws bite.
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
    FGrantTargetOtherR, FGrantFacetKubeOnly)

  action genNear = {
    nondet f1 = FAULTS.oneOf()
    nondet f2 = FAULTS.oneOf()
    val f = Set(f1, f2)
    nondet att = Set(ShrinkTarget, ShrinkFacet, AddUnknownCaveat, ShortenExpiry).oneOf()
    nondet attT = RECEIVERS.oneOf()
    nondet attF = FACETS.oneOf()
    nondet audKind = Set(0, 1).oneOf()   // 0: grant to the key, 1: grant to group:admins
    val consent1 = mkCert(
      if (f.contains(FConsentSignedByOtherR)) OTHER_R else R,
      AKey(OWNER1), Invoke,
      if (f.contains(FConsentTargetOtherR)) Set(OTHER_R) else Set(R),
      if (f.contains(FConsentFacetKubeOnly)) Set("kube-api") else FACETS,
      Set(), "",
      not(f.contains(FConsentNotDelegable)), false,
      if (f.contains(FConsentExpired)) NOW else 10,
      f.contains(FConsentForged))
    val consent2 = mkCert(R, AKey(OWNER2), Invoke, Set(R), FACETS, Set(), "", true, false, 10, false)
    val g = mkCert(
      if (f.contains(FGrantIssuerOwner2)) OWNER2 else OWNER1,
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
    all {
      alpn' = if (f.contains(FAlpnBogus)) "mesh/bogus/v1" else "mesh/apid/v1",
      peer' = CALLER,
      blocklist' = if (f.contains(FBlocklisted)) Set(CALLER) else Set(),
      member' = mkCert(
        if (f.contains(FMemberIssuerRogue)) ROGUE else OWNER1,
        AKey(if (f.contains(FMemberAudRogue)) ROGUE else CALLER),
        if (f.contains(FMemberVerb)) Invoke else Member,
        Set(), Set(),
        if (f.contains(FMemberNoGroups)) Set() else Set("admins"),
        "laptop", false,
        f.contains(FMemberUnknown),
        if (f.contains(FMemberExpired)) NOW else 10,
        f.contains(FMemberForged)),
      consents' = if (f.contains(FNoConsentOwner2)) Set(consent1) else Set(consent1, consent2),
      grants' = Set(g),
      grantsAtt' = Set(attenuate(g, att, attT, attF)),
    }
  }

  action step = any { gen, genNear }

  // ---- laws ----------------------------------------------------------

  // Fail-closed (1): an ALPN the receiver never declared admits nothing.
  val invUnknownAlpnRejects =
    not(ACCEPT_TABLE.keys().contains(alpn)) implies not(accepted(res))

  // Receiver-rooted: with no live R-signed cert anywhere, nothing is
  // verifiable — regardless of what Owners signed.
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

  // Step (2b): the member cert's issuer must be one R consented to (3cx).
  val invMemberIssuerConsented =
    accepted(res) implies consents.exists(c =>
      c.iss == R and isKey(c.aud, member.iss) and sigOk(c, NOW))

  // Blocklist is checked on the member key.
  val invBlocklistRejects = blocklist.contains(peer) implies not(accepted(res))

  // Fail-closed (3): if every grant is forged/expired/unknown-verb/unknown-caveat, reject.
  val invGrantsFailClosed =
    grants.forall(g => g.forged or g.exp <= NOW or g.can != Invoke or g.cav.unknown)
    implies not(accepted(res))

  // Issuer-scoped groups: if every grant is a group grant from an issuer
  // other than the member cert's, no group can resolve — reject. (A
  // grant from OWNER2 to group:admins must not be satisfied by OWNER1's
  // admins cert, even if R consented to both.)
  val invCrossIssuerGroupRejects =
    grants.forall(g => match g.aud {
      | AKey(_) => false
      | AGroup(_) => g.iss != member.iss
    }) implies not(accepted(res))

  // Target: a grant whose target set omits R never admits at R.
  val invTargetIsReceiver =
    grants.forall(g => not(g.cav.target.contains(R))) implies not(accepted(res))

  // Facet: ALPN routes, grants authorize — the granted facet must
  // contain the one the ALPN selects.
  val invFacetMatchesAlpn =
    (ACCEPT_TABLE.keys().contains(alpn)
      and grants.forall(g => not(g.cav.facet.contains(ACCEPT_TABLE.get(alpn)))))
    implies not(accepted(res))

  // Identity out comes from the member cert only — never from a grant.
  val invIdentityFromMember = match res {
    | Reject => true
    | Accept(id) => id == { key: peer, name: member.cav.name, groups: member.cav.groups }
  }

  // Monotone under attenuation: adding a caveat to a link can only
  // shrink what is accepted.
  val invMonotone = accepted(resAtt) implies accepted(res)

  val invAll = and {
    invUnknownAlpnRejects,
    invReceiverRooted,
    invConsentMustDelegate,
    invConsentTargetsSelf,
    invMemberRequired,
    invMemberIssuerConsented,
    invBlocklistRejects,
    invGrantsFailClosed,
    invCrossIssuerGroupRejects,
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
}
