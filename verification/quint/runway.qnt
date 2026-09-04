// runway.qnt — cert lifetimes vs. the sealed hub (talos-config-jp2).
//
// Models the ADR-0017 "Cert classes and lifetimes" rule — "propagation
// is by poll, expiry is runway: no class expires faster than the
// sealed-hub window it must survive" — together with hub.qnt's fact
// that every deploy re-seals the hub until the wallet signs. A member
// keeps `member` (90 d, renewed at ⅔ life by background dial) and
// `invoke` grants (7 d, polled daily); while the hub is sealed neither
// can be refreshed, so the question is: for how long without a hub
// does access provably survive?
//
// Time is in HOURS. Instead of stepping through 2160 h, `init` places
// the system nondeterministically anywhere in its renewal cycle
// (possibly already sealed), so every boundary state is a few steps
// from init and Apalache can reach it.
//
// FINDINGS (2026-09-05, model-first; all ruled the same day and folded
// into ADR-0017 + the glossary "Cert classes and lifetimes"):
//
// 1. ADR-0017 §Confirmation claimed "a sealed hub of < 7 days causes no
//    loss of access". With invoke grants of 7 d polled every 24 h the
//    worst case is: poll at p, deploy at p+24−ε, grant expires at
//    p+168 ⇒ only 144 h (6 d) without a hub when access is lost. A
//    class's runway is (lifetime − refresh cadence), not the lifetime.
//    Ruled (z1z): the claim is now 6 d; `invRunway` is that claim.
//
// 2. "Sealed for" is the wrong clock. Seal 7 d → unseal → redeploy
//    before any member beat ran (the `fbb` pattern: every deploy
//    re-seals) is "sealed for 0 h" and yet access is gone. The clock
//    that matters is STARVATION: hours since the hub was last unsealed
//    during a beat. All laws below use it; flapping seals accumulate.
//
// 3. Renewal "presents the expiring cert". Ruled strict (sqm): an
//    EXPIRED member cert cannot be renewed — the device re-enrolls, a
//    wallet act — so starvation beyond MEMBER_RUNWAY (30 d) costs one
//    signature per device. `invReenrollOnlyAfterLongStarvation` pins
//    that shorter outages never cost a human act.
//
// 4. `reach-me-at` (1 h) is self-issued by every actor, machines
//    included; the hub only relays (ruled, xwz). RMA_HUB_ISSUED=false
//    encodes that; flipping it makes `invLifetimesCoverWindow` fail,
//    which is the reason for the ruling.
//
// Check:
//   quint run    --invariant=invAll --max-steps=60 runway.qnt
//   quint verify --invariant=invAll --max-steps=10 runway.qnt
module runway {
  pure val MEMBER_LIFE = 90 * 24
  pure val MEMBER_RENEW_AGE = 60 * 24      // ⅔ life elapsed ⇒ renew on the next beat
  pure val INVOKE_LIFE = 7 * 24
  pure val POLL_EVERY = 24
  pure val RMA_LIFE = 1
  pure val RMA_HUB_ISSUED = false           // ruled xwz: actors self-issue reach-me-at

  pure val INIT_LOOKBACK = 7 * 24           // how far back init may place the last served beat
  // Runway per class: lifetime minus the refresh cadence.
  pure val INVOKE_RUNWAY = INVOKE_LIFE - POLL_EVERY
  pure val MEMBER_RUNWAY = MEMBER_LIFE - MEMBER_RENEW_AGE
  pure val SEALED_WINDOW = 6 * 24           // ADR-0017 §Confirmation (as amended, z1z)

  var t: int
  var sealed: bool
  var unsealedSince: int
  var lastServed: int    // last hour the hub was unsealed while a beat ran
  var maxStarved: int    // longest (t − lastServed) observed (history witness)
  var memberIssued: int
  var invokeIssued: int
  var lastPoll: int
  var rmaIssued: int
  var gitEpoch: int      // policy version in git
  var hubEpoch: int      // policy version the hub compiled (baked at deploy)
  var callerEpoch: int   // policy version the caller's grants encode
  var reenrolled: int    // wallet-signed re-enrollments forced by an expired member cert

  def memberValid = t < memberIssued + MEMBER_LIFE
  def invokeValid = t < invokeIssued + INVOKE_LIFE
  def rmaValid = t < rmaIssued + RMA_LIFE
  def starved = t - lastServed
  def unsealedFor = if (sealed) 0 else t - unsealedSince

  // Start anywhere in the cycle. Consistency constraints encode what
  // the beats did before t=0: at the last served hour the poll was
  // ≤ POLL_EVERY-1 old and the member ≤ MEMBER_RENEW_AGE-1 old (had
  // either been older it would have refreshed on that beat).
  action init = {
    nondet s = Bool.oneOf()
    nondet since = (0 - INIT_LOOKBACK).to(0).oneOf()
    nondet pollAge = 0.to(POLL_EVERY - 1).oneOf()
    nondet memberAge = 0.to(MEMBER_RENEW_AGE - 1).oneOf()
    nondet epochs = 0.to(2).oneOf()
    val anchor = if (s) since else 0
    all {
      t' = 0,
      sealed' = s,
      unsealedSince' = anchor,
      lastServed' = anchor,
      maxStarved' = 0 - anchor,
      lastPoll' = anchor - pollAge,
      invokeIssued' = anchor - pollAge,
      memberIssued' = anchor - memberAge,
      rmaIssued' = if (RMA_HUB_ISSUED) anchor else 0,
      gitEpoch' = epochs,
      hubEpoch' = epochs,
      callerEpoch' = epochs,
      reenrolled' = 0,
    }
  }

  // One hour passes. The member's background beats run iff the hub is
  // unsealed: daily poll refreshes invoke grants (and picks up the
  // hub's policy epoch); member renews once past ⅔ life — but only
  // while still valid (finding 3).
  action tick = {
    val t1 = t + 1
    val pollDue = not(sealed) and (t1 - lastPoll >= POLL_EVERY)
    val renewDue = not(sealed) and (t1 - memberIssued >= MEMBER_RENEW_AGE)
                   and (t1 < memberIssued + MEMBER_LIFE)
    val rmaRefresh = not(RMA_HUB_ISSUED) or not(sealed)
    val served1 = if (sealed) lastServed else t1
    all {
      t' = t1,
      lastServed' = served1,
      maxStarved' = if (t1 - served1 > maxStarved) t1 - served1 else maxStarved,
      lastPoll' = if (pollDue) t1 else lastPoll,
      invokeIssued' = if (pollDue) t1 else invokeIssued,
      callerEpoch' = if (pollDue) hubEpoch else callerEpoch,
      memberIssued' = if (renewDue) t1 else memberIssued,
      rmaIssued' = if (rmaRefresh) t1 else rmaIssued,
      sealed' = sealed,
      unsealedSince' = unsealedSince,
      gitEpoch' = gitEpoch,
      hubEpoch' = hubEpoch,
      reenrolled' = reenrolled,
    }
  }

  // Deploy: possible at any moment (hub.qnt). Bakes git into the hub
  // and seals it.
  action deploy = all {
    sealed' = true,
    hubEpoch' = gitEpoch,
    t' = t, unsealedSince' = unsealedSince, lastServed' = lastServed, maxStarved' = maxStarved,
    memberIssued' = memberIssued, invokeIssued' = invokeIssued, lastPoll' = lastPoll,
    rmaIssued' = rmaIssued, gitEpoch' = gitEpoch, callerEpoch' = callerEpoch,
    reenrolled' = reenrolled,
  }

  // The wallet signs; the hub is back.
  action unseal = all {
    sealed,
    sealed' = false,
    unsealedSince' = t,
    t' = t, lastServed' = lastServed, maxStarved' = maxStarved,
    memberIssued' = memberIssued, invokeIssued' = invokeIssued, lastPoll' = lastPoll,
    rmaIssued' = rmaIssued, gitEpoch' = gitEpoch, hubEpoch' = hubEpoch, callerEpoch' = callerEpoch,
    reenrolled' = reenrolled,
  }

  // An expired member cert cannot be renewed; the Owner signs a fresh
  // enrollment (device flow). Counted, because every one is a human act.
  action reenroll = all {
    not(sealed),
    not(memberValid),
    memberIssued' = t,
    reenrolled' = reenrolled + 1,
    t' = t, sealed' = sealed, unsealedSince' = unsealedSince, lastServed' = lastServed,
    maxStarved' = maxStarved, invokeIssued' = invokeIssued, lastPoll' = lastPoll,
    rmaIssued' = rmaIssued, gitEpoch' = gitEpoch, hubEpoch' = hubEpoch, callerEpoch' = callerEpoch,
  }

  // The Owner edits mesh-policy.yaml. Nothing else moves: git is
  // compiler input, and the compiler runs at deploy.
  action commitPolicy = all {
    gitEpoch' = gitEpoch + 1,
    t' = t, sealed' = sealed, unsealedSince' = unsealedSince, lastServed' = lastServed,
    maxStarved' = maxStarved, memberIssued' = memberIssued, invokeIssued' = invokeIssued,
    lastPoll' = lastPoll, rmaIssued' = rmaIssued, hubEpoch' = hubEpoch, callerEpoch' = callerEpoch,
    reenrolled' = reenrolled,
  }

  action step = any { tick, deploy, unseal, reenroll, commitPolicy }

  // ---- laws ----------------------------------------------------------

  // The promise this model makes: up to SEALED_WINDOW hours without a
  // usable hub lose no access — every hub-issued class is still valid.
  val invRunway =
    (starved <= SEALED_WINDOW) implies (memberValid and invokeValid and rmaValid)

  // The rule as pinned in ADR-0017, made static: each class's runway
  // (lifetime − refresh cadence) covers the promised window.
  val invLifetimesCoverWindow = and {
    INVOKE_RUNWAY >= SEALED_WINDOW,
    MEMBER_RUNWAY >= SEALED_WINDOW,
    RMA_HUB_ISSUED implies (RMA_LIFE >= SEALED_WINDOW),
  }

  // Recovery: one served beat restores everything — as long as no
  // starvation ever exceeded the member runway (else see reenroll).
  val invServedBeatRecovers =
    (starved == 0 and maxStarved <= MEMBER_RUNWAY) implies (memberValid and invokeValid)

  // A wallet-signed re-enrollment is only ever forced by starvation
  // beyond the member runway (30 d). Shorter outages never cost a human act.
  val invReenrollOnlyAfterLongStarvation =
    reenrolled > 0 implies maxStarved > MEMBER_RUNWAY

  // Policy epochs flow one way: git → hub (at deploy) → caller (at poll).
  val invEpochChain = callerEpoch <= hubEpoch and hubEpoch <= gitEpoch

  // ADR-0017 §Confirmation: a policy change reaches callers within one
  // poll interval — once the hub that compiled it is unsealed.
  val invPolicyPropagates =
    (not(sealed) and unsealedFor >= POLL_EVERY) implies callerEpoch == hubEpoch

  val invAll = and {
    invRunway,
    invLifetimesCoverWindow,
    invServedBeatRecovers,
    invReenrollOnlyAfterLongStarvation,
    invEpochChain,
    invPolicyPropagates,
  }

  // Witness: the worst case is reachable — one served beat, then seal,
  // then survive exactly INVOKE_RUNWAY hours, and not one more.
  action initServed = all {
    t' = 0, sealed' = false, unsealedSince' = 0, lastServed' = 0, maxStarved' = 0,
    lastPoll' = 0, invokeIssued' = 0, memberIssued' = 0, rmaIssued' = 0,
    gitEpoch' = 0, hubEpoch' = 0, callerEpoch' = 0, reenrolled' = 0,
  }

  run worstCaseTest =
    initServed
      .then((POLL_EVERY - 1).reps(_ => tick))   // seal just before the next poll
      .then(deploy)
      .then((INVOKE_RUNWAY - 1).reps(_ => tick))
      .then(all { assert(starved == INVOKE_RUNWAY - 1 and invokeValid), tick })
      .then(all { assert(starved == INVOKE_RUNWAY and invokeValid), tick })
      .then(all { assert(starved == INVOKE_RUNWAY + 1 and not(invokeValid)), tick })
}
