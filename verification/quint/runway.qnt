// runway.qnt — cert lifetimes vs. the sealed hub (talos-config-jp2).
//
// Models the ADR-0017 "Cert classes and lifetimes" rule — "propagation
// is by poll, expiry is runway: no class expires faster than the
// sealed-hub window it must survive" — together with hub.qnt's fact
// that every deploy re-seals the hub until the wallet signs, and the
// ADR-0018 bound: every hub-signed cert is only as valid as the
// `speak-as` the wallet signed to the hub PROCESS that issued it. A
// member keeps `member` (90 d, renewed at ⅔ life by background dial)
// and `invoke` grants (7 d, polled daily); while the hub is sealed
// neither can be refreshed, so the question is: for how long without
// a hub does access provably survive?
//
// Time is in HOURS. Instead of stepping through 2160 h, `init` places
// the system nondeterministically anywhere in its renewal cycle
// (possibly already sealed), so every boundary state is a few steps
// from init and Apalache can reach it.
//
// The speak-as bound (ADR-0018, 2026-09-06). `speakAsIssued` is the
// hour the wallet last unsealed the CURRENT process; its speak-as
// expires SPEAKAS_LIFE later. Unseal is the only refresh (a wallet
// act); a member beat never moves it; a deploy is a new process with
// no speak-as (= sealed) until the next unseal. A cert's EFFECTIVE
// expiry is min(own exp, exp of the speak-as its signer held) — the
// member learns that value on the beat that served the cert
// (`memberSAExp`, `invokeSAExp`). The hub NAGS: when its speak-as has
// < NAG left it reports sealed (`/sealed` 503) and — NAG_SEALS — stops
// serving beats, so no cert leaves with less than NAG of speak-as life.
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
// FINDINGS (2026-09-06, model-first, on the ADR-0018 speak-as bound;
// NOT yet ruled — see the czi/jp2 report):
//
// 5. The glossary's renewal rule for `member` ("renewed at ⅔ life") is
//    insufficient under ADR-0018. A member cert signed by a dead
//    process keeps that process's speak-as expiry forever (nothing can
//    refresh it); if the hub redeploys when its speak-as has < 30 d
//    left, members whose certs are < 60 d old will not renew before
//    their EFFECTIVE expiry — access dies with the hub healthy and
//    starvation 0. ADR-0018 §Confirmation says members "converge within
//    one beat" after a redeploy; that must be a RENEWAL TRIGGER: renew
//    on the first served beat whose hub key differs from the cert's
//    signer (RENEW_ON_ROTATION=true; false ⇒ invRunway fails from
//    init). Equivalent alternative: schedule renewal off the effective
//    expiry (renew when min(exp, speakas.exp) − now ≤ ⅓ life).
//
// 6. Quantified: with NAG = 30 d the 30 d member runway holds exactly
//    (NAG = MEMBER_RUNWAY, zero margin) — but ONLY if the nag means
//    "stop serving beats" (NAG_SEALS=true), so that the process's
//    unattended serving life is SPEAKAS_LIFE − NAG = 90 d. If the nag
//    is advisory (NAG_SEALS=false; the hub keeps serving to day 120),
//    a beat served at process age a > 90 d leaves runway 120 d − a:
//    invServedBeatKeepsRunway and invReenrollOnlyAfterLongStarvation
//    fail, and past day 114 even 6 d of starvation loses access. No
//    SPEAKAS_LIFE fixes the advisory reading — the cliff is at the
//    speak-as expiry whatever its length; only a serving cutoff ≥ the
//    promised runway does. Note the self-seal buys nothing at the
//    cliff itself: with or without it every member's effective expiry
//    is the speak-as expiry, so the wallet act is due by day 120 either
//    way; what NAG_SEALS buys is that the promise "N days of starvation
//    lose nothing" stays true in STARVATION terms. Fencepost: a beat
//    served with exactly NAG left expires at starvation == NAG, hence
//    `>=` in invReenrollOnlyAfterLongStarvation (was `>`).
//
// 7. Past the cliff, WHAT the wallet does decides the cost. Re-unsealing
//    the SAME process (same hub key) covers every cert that key signed:
//    the next beat renews them, zero human acts per device
//    (nagReunsealRecoversTest). Redeploying first (new key) leaves the
//    old certs with a speak-as nobody can refresh; strict renewal (sqm)
//    ⇒ every member re-enrolls although every member cert is unexpired
//    (nagRedeployReenrollsTest). `fbb` (every deploy re-seals) therefore
//    has a cheap and an expensive order after a missed nag: unseal,
//    let one beat run, THEN deploy. The link expiry is not the cert's expiry: the
//    glossary's strict rule should say "expired or unresolvable".
//
// Check:
//   quint test   runway.qnt
//   quint run    --invariant=invAll --max-steps=60 runway.qnt
//   quint verify --invariant=invAll --max-steps=10 runway.qnt
//   (RENEW_ON_ROTATION=false or NAG_SEALS=false or NAG < 30*24 ⇒ invAll violated)
module runway {
  pure val MEMBER_LIFE = 90 * 24
  pure val MEMBER_RENEW_AGE = 60 * 24      // ⅔ life elapsed ⇒ renew on the next beat
  pure val INVOKE_LIFE = 7 * 24
  pure val POLL_EVERY = 24
  pure val RMA_LIFE = 1
  pure val RMA_HUB_ISSUED = false           // ruled xwz: actors self-issue reach-me-at

  // ADR-0018: the speak-as belongs to the hub PROCESS.
  pure val SPEAKAS_LIFE = 120 * 24
  pure val NAG = 30 * 24                    // hub reports sealed when < NAG of speak-as is left
  pure val NAG_SEALS = true                 // finding 6: the nag must stop serving, not just warn
  pure val RENEW_ON_ROTATION = true         // finding 5: renew on the first beat under a new hub key

  pure val INIT_LOOKBACK = 7 * 24           // how far back init may place the last served beat
  // Runway per class: lifetime minus the refresh cadence.
  pure val INVOKE_RUNWAY = INVOKE_LIFE - POLL_EVERY
  pure val MEMBER_RUNWAY = MEMBER_LIFE - MEMBER_RENEW_AGE
  pure val SEALED_WINDOW = 6 * 24           // ADR-0017 §Confirmation (as amended, z1z)
  // Finding 6: the longest a process may serve beats before a beat it
  // serves carries less than the member runway of speak-as life.
  pure val MAX_UNATTENDED_LIFE = SPEAKAS_LIFE - MEMBER_RUNWAY

  pure def min(a: int, b: int): int = if (a < b) a else b

  var t: int
  var proc: int          // hub process generation; a deploy starts a new one
  var speakAsIssued: int // hour the wallet unsealed the current process (its speak-as iat)
  var unsealedSince: int
  var lastServed: int    // last hour the hub served a beat
  var maxStarved: int    // longest (t − lastServed) observed (history witness)
  var memberIssued: int
  var memberProc: int    // the process whose key signed the member cert
  var memberSAExp: int   // speak-as expiry the member's bundle carries for its member cert
  var invokeIssued: int
  var invokeSAExp: int   // speak-as expiry the member's bundle carries for its invoke grants
  var lastPoll: int
  var rmaIssued: int
  var gitEpoch: int      // policy version in git
  var hubEpoch: int      // policy version the hub compiled (baked at deploy)
  var callerEpoch: int   // policy version the caller's grants encode
  var reenrolled: int    // wallet-signed re-enrollments forced by an expired member cert
  var commitSinceDeploy: bool // a policy commit no deploy has compiled yet (witness for invDeployBakesGit)

  def speakAsExp = speakAsIssued + SPEAKAS_LIFE
  def sealedAt(h: int): bool = h >= speakAsExp                        // no live speak-as: nothing can be signed
  def naggingAt(h: int): bool = not(sealedAt(h)) and speakAsExp - h < NAG  // `/sealed` 503
  def servingAt(h: int): bool = not(sealedAt(h)) and (not(NAG_SEALS) or not(naggingAt(h)))
  def sealed = sealedAt(t)
  def nagging = naggingAt(t)
  def serving = servingAt(t)

  def memberEffExp = min(memberIssued + MEMBER_LIFE, memberSAExp)
  def invokeEffExp = min(invokeIssued + INVOKE_LIFE, invokeSAExp)
  // What the member's bundle proves at a verifier right now.
  def memberValid = t < memberEffExp
  // What the hub can still re-verify: a cert signed by ITS OWN key is
  // covered by its current speak-as (a re-unseal of the same process
  // rescues link-expired certs); one from a dead process only by the
  // speak-as that process held.
  def memberRenewable =
    t < min(memberIssued + MEMBER_LIFE, if (memberProc == proc) speakAsExp else memberSAExp)
  def invokeValid = t < invokeEffExp
  def rmaValid = t < rmaIssued + RMA_LIFE
  def starved = t - lastServed
  def unsealedFor = if (sealed) 0 else t - unsealedSince

  // Start anywhere in the cycle. Consistency constraints encode what
  // the beats did before t=0: at the last served hour the poll was
  // ≤ POLL_EVERY-1 old, the member ≤ MEMBER_RENEW_AGE-1 old (had
  // either been older it would have refreshed on that beat), and the
  // serving process had ≥ NAG of speak-as left (else it was not
  // serving). If sealed now, the current process is a fresh one with
  // no speak-as; the member's certs are from the previous process.
  action init = {
    nondet s = Bool.oneOf()
    nondet since = (0 - INIT_LOOKBACK).to(0).oneOf()
    nondet pollAge = 0.to(POLL_EVERY - 1).oneOf()
    nondet memberAge = 0.to(MEMBER_RENEW_AGE - 1).oneOf()
    nondet saLeft = (if (NAG_SEALS) NAG else 1).to(SPEAKAS_LIFE).oneOf()   // speak-as left at the last served beat
    nondet epochs = 0.to(2).oneOf()
    val anchor = if (s) since else 0
    all {
      t' = 0,
      proc' = if (s) 1 else 0,
      speakAsIssued' = if (s) since - SPEAKAS_LIFE else saLeft - SPEAKAS_LIFE,
      unsealedSince' = if (s) anchor else saLeft - SPEAKAS_LIFE,
      lastServed' = anchor,
      maxStarved' = 0 - anchor,
      lastPoll' = anchor - pollAge,
      invokeIssued' = anchor - pollAge,
      invokeSAExp' = anchor + saLeft,
      memberIssued' = anchor - memberAge,
      memberProc' = 0,
      memberSAExp' = anchor + saLeft,
      rmaIssued' = if (RMA_HUB_ISSUED) anchor else 0,
      gitEpoch' = epochs,
      hubEpoch' = epochs,
      callerEpoch' = epochs,
      reenrolled' = 0,
      commitSinceDeploy' = false,
    }
  }

  // One hour passes. The member's background beats run iff the hub is
  // serving: daily poll refreshes invoke grants (and picks up the
  // hub's policy epoch and the current speak-as); member renews once
  // past ⅔ life or — RENEW_ON_ROTATION — when its cert was signed by
  // an earlier process; either only while still effectively valid
  // (finding 3).
  action tick = {
    val t1 = t + 1
    val serving1 = servingAt(t1)   // the beat runs at hour t1 iff the hub serves at t1
    val pollDue = serving1 and (t1 - lastPoll >= POLL_EVERY)
    val renewDue = serving1
                   and ((t1 - memberIssued >= MEMBER_RENEW_AGE)
                        or (RENEW_ON_ROTATION and memberProc != proc))
                   and (t1 < min(memberIssued + MEMBER_LIFE, if (memberProc == proc) speakAsExp else memberSAExp))
    val rmaRefresh = not(RMA_HUB_ISSUED) or serving1
    val served1 = if (serving1) t1 else lastServed
    all {
      t' = t1,
      lastServed' = served1,
      // starvation is measured up to and including the hour the beat runs
      maxStarved' = if (t1 - lastServed > maxStarved) t1 - lastServed else maxStarved,
      lastPoll' = if (pollDue) t1 else lastPoll,
      invokeIssued' = if (pollDue) t1 else invokeIssued,
      invokeSAExp' = if (pollDue) speakAsExp else invokeSAExp,
      callerEpoch' = if (pollDue) hubEpoch else callerEpoch,
      memberIssued' = if (renewDue) t1 else memberIssued,
      memberProc' = if (renewDue) proc else memberProc,
      // the bundle carries the speak-as of the key that signed the cert:
      // a re-unseal of the SAME process extends it on any served beat; a
      // cert from a dead process keeps the dead process's expiry
      memberSAExp' = if (renewDue or (serving1 and memberProc == proc)) speakAsExp else memberSAExp,
      rmaIssued' = if (rmaRefresh) t1 else rmaIssued,
      proc' = proc,
      speakAsIssued' = speakAsIssued,
      unsealedSince' = unsealedSince,
      gitEpoch' = gitEpoch,
      hubEpoch' = hubEpoch,
      reenrolled' = reenrolled,
      commitSinceDeploy' = commitSinceDeploy,
    }
  }

  // Deploy: possible at any moment (hub.qnt). Bakes git into the hub,
  // starts a new process with a fresh key and no speak-as: sealed.
  action deploy = all {
    proc' = proc + 1,
    speakAsIssued' = t - SPEAKAS_LIFE,
    hubEpoch' = gitEpoch,
    t' = t, unsealedSince' = unsealedSince, lastServed' = lastServed, maxStarved' = maxStarved,
    memberIssued' = memberIssued, memberProc' = memberProc, memberSAExp' = memberSAExp,
    invokeIssued' = invokeIssued, invokeSAExp' = invokeSAExp, lastPoll' = lastPoll,
    rmaIssued' = rmaIssued, gitEpoch' = gitEpoch, callerEpoch' = callerEpoch,
    reenrolled' = reenrolled, commitSinceDeploy' = false,
  }

  // The wallet signs a speak-as to the current process's key: the hub
  // is back (if sealed) or its speak-as is refreshed (if nagging, or
  // whenever the owner likes). The only thing that moves speakAsIssued.
  action unseal = all {
    speakAsIssued' = t,
    unsealedSince' = if (sealed) t else unsealedSince,
    t' = t, proc' = proc, lastServed' = lastServed, maxStarved' = maxStarved,
    memberIssued' = memberIssued, memberProc' = memberProc, memberSAExp' = memberSAExp,
    invokeIssued' = invokeIssued, invokeSAExp' = invokeSAExp, lastPoll' = lastPoll,
    rmaIssued' = rmaIssued, gitEpoch' = gitEpoch, hubEpoch' = hubEpoch, callerEpoch' = callerEpoch,
    reenrolled' = reenrolled, commitSinceDeploy' = commitSinceDeploy,
  }

  // An effectively expired member cert cannot be renewed (its own exp
  // passed, or the speak-as of the key that signed it did); the Owner
  // signs a fresh enrollment (device flow). Counted, because every one
  // is a human act.
  action reenroll = all {
    serving,
    not(memberRenewable),
    memberIssued' = t,
    memberProc' = proc,
    memberSAExp' = speakAsExp,
    reenrolled' = reenrolled + 1, commitSinceDeploy' = commitSinceDeploy,
    t' = t, proc' = proc, speakAsIssued' = speakAsIssued, unsealedSince' = unsealedSince,
    lastServed' = lastServed, maxStarved' = maxStarved,
    invokeIssued' = invokeIssued, invokeSAExp' = invokeSAExp, lastPoll' = lastPoll,
    rmaIssued' = rmaIssued, gitEpoch' = gitEpoch, hubEpoch' = hubEpoch, callerEpoch' = callerEpoch,
  }

  // The Owner edits mesh-policy.yaml. Nothing else moves: git is
  // compiler input, and the compiler runs at deploy.
  action commitPolicy = all {
    gitEpoch' = gitEpoch + 1,
    t' = t, proc' = proc, speakAsIssued' = speakAsIssued, unsealedSince' = unsealedSince,
    lastServed' = lastServed, maxStarved' = maxStarved,
    memberIssued' = memberIssued, memberProc' = memberProc, memberSAExp' = memberSAExp,
    invokeIssued' = invokeIssued, invokeSAExp' = invokeSAExp, lastPoll' = lastPoll,
    rmaIssued' = rmaIssued, hubEpoch' = hubEpoch, callerEpoch' = callerEpoch,
    reenrolled' = reenrolled, commitSinceDeploy' = true,
  }

  action step = any { tick, deploy, unseal, reenroll, commitPolicy }

  // ---- laws ----------------------------------------------------------

  // The promise this model makes: up to SEALED_WINDOW hours without a
  // usable hub lose no access — every hub-issued class is still
  // (effectively) valid. ADR-0017/0018 §Confirmation.
  val invRunway =
    (starved <= SEALED_WINDOW) implies (memberValid and invokeValid and rmaValid)

  // The rule as pinned in ADR-0017, made static: each class's runway
  // (lifetime − refresh cadence) covers the promised window; and the
  // speak-as bound (ADR-0018) does not cut below it — the nag must fire,
  // and stop serving, no later than MEMBER_RUNWAY before the speak-as
  // expires (finding 6).
  val invLifetimesCoverWindow = and {
    INVOKE_RUNWAY >= SEALED_WINDOW,
    MEMBER_RUNWAY >= SEALED_WINDOW,
    RMA_HUB_ISSUED implies (RMA_LIFE >= SEALED_WINDOW),
    NAG_SEALS and NAG >= MEMBER_RUNWAY,
    SPEAKAS_LIFE - NAG <= MAX_UNATTENDED_LIFE,
    MAX_UNATTENDED_LIFE > 0,
  }

  // Every served beat leaves the member at least its runway: the
  // dynamic form of finding 6. A beat that leaves less has degraded the
  // 30 d promise (advisory nag, or NAG < MEMBER_RUNWAY).
  val invServedBeatKeepsRunway =
    (starved == 0) implies (memberEffExp - t >= MEMBER_RUNWAY and invokeEffExp - t >= INVOKE_RUNWAY)

  // Recovery: one served beat restores everything — as long as no
  // starvation ever reached the member runway (else see reenroll).
  val invServedBeatRecovers =
    (starved == 0 and maxStarved < MEMBER_RUNWAY) implies (memberValid and invokeValid)

  // A wallet-signed re-enrollment is only ever forced by starvation of
  // at least the member runway (30 d). Shorter outages never cost a
  // human act. (`>=`, not `>`: a beat served with exactly NAG of speak-as
  // left expires at starvation == NAG == MEMBER_RUNWAY — finding 6.)
  val invReenrollOnlyAfterLongStarvation =
    reenrolled > 0 implies maxStarved >= MEMBER_RUNWAY

  // Policy epochs flow one way: git → hub (at deploy) → caller (at poll).
  val invEpochChain = callerEpoch <= hubEpoch and hubEpoch <= gitEpoch

  // ADR-0017 §Confirmation: a policy change reaches callers within one
  // poll interval — once the hub that compiled it is serving. (If it is
  // serving now it has served since it was unsealed: remaining speak-as
  // life only shrinks with time and only grows by unseal.)
  val invPolicyPropagates =
    (serving and unsealedFor >= POLL_EVERY) implies callerEpoch == hubEpoch

  // A deploy compiles git into the hub: unless a commit has landed since
  // the last deploy, the hub's epoch IS git's. Closes mutant o5 (deploy
  // forgets to bake gitEpoch), which invPolicyPropagates alone let live
  // because it only compares caller with hub.
  val invDeployBakesGit =
    not(commitSinceDeploy) implies hubEpoch == gitEpoch

  // ADR-0018: a member never holds a cert whose speak-as outlives the
  // process that signed it, and its bundle's speak-as expiry is never
  // beyond the current process's when it is the signer.
  val invBundleSpeakAsConsistent =
    (memberProc == proc) implies (memberSAExp <= speakAsExp)

  val invAll = and {
    invRunway,
    invLifetimesCoverWindow,
    invServedBeatKeepsRunway,
    invServedBeatRecovers,
    invReenrollOnlyAfterLongStarvation,
    invEpochChain,
    invPolicyPropagates,
    invDeployBakesGit,
    invBundleSpeakAsConsistent,
  }

  // Witness: the worst case is reachable — one served beat, then seal,
  // then survive exactly INVOKE_RUNWAY hours, and not one more.
  action initServed = all {
    t' = 0, proc' = 0, speakAsIssued' = 0, unsealedSince' = 0, lastServed' = 0, maxStarved' = 0,
    lastPoll' = 0, invokeIssued' = 0, invokeSAExp' = SPEAKAS_LIFE,
    memberIssued' = 0, memberProc' = 0, memberSAExp' = SPEAKAS_LIFE, rmaIssued' = 0,
    gitEpoch' = 0, hubEpoch' = 0, callerEpoch' = 0, reenrolled' = 0, commitSinceDeploy' = false,
  }

  run worstCaseTest =
    initServed
      .then((POLL_EVERY - 1).reps(_ => tick))   // seal just before the next poll
      .then(deploy)
      .then((INVOKE_RUNWAY - 1).reps(_ => tick))
      .then(all { assert(starved == INVOKE_RUNWAY - 1 and invokeValid), tick })
      .then(all { assert(starved == INVOKE_RUNWAY and invokeValid), tick })
      .then(all { assert(starved == INVOKE_RUNWAY + 1 and not(invokeValid)), tick })

  // Witness (finding 6): a process at the nag boundary — the last beat it
  // serves has exactly NAG of speak-as left. From there the member holds
  // exactly MEMBER_RUNWAY hours and the process serves nothing more
  // until the wallet acts. The member cert itself (90 d) outlives the
  // link: the cliff is the speak-as, not the cert.
  action initAtNag = all {
    t' = 0, proc' = 0, speakAsIssued' = NAG - SPEAKAS_LIFE, unsealedSince' = NAG - SPEAKAS_LIFE,
    lastServed' = 0, maxStarved' = 0,
    lastPoll' = 0, invokeIssued' = 0, invokeSAExp' = NAG,
    memberIssued' = 0, memberProc' = 0, memberSAExp' = NAG, rmaIssued' = 0,
    gitEpoch' = 0, hubEpoch' = 0, callerEpoch' = 0, reenrolled' = 0, commitSinceDeploy' = false,
  }

  run toNagCliff =
    initAtNag
      .then(all { assert(serving and not(nagging) and memberEffExp == NAG), tick })
      .then(all { assert(nagging and not(serving) and starved == 1 and memberValid), tick })
      .then((MEMBER_RUNWAY - 3).reps(_ => tick))
      .then(all { assert(starved == MEMBER_RUNWAY - 1 and memberValid), tick })
      .then(all { assert(starved == MEMBER_RUNWAY and not(memberValid) and sealed
                         and t < memberIssued + MEMBER_LIFE), unseal })

  // Same process re-unsealed (same hub key): its new speak-as covers the
  // old cert; the next beat renews it — no human act per device.
  run nagReunsealRecoversTest =
    toNagCliff
      .then(all { assert(serving and not(memberValid) and memberRenewable), tick })
      .then(all { assert(memberValid and reenrolled == 0 and maxStarved == MEMBER_RUNWAY + 1), tick })

  // Redeployed instead (new hub key): the old speak-as is dead and no
  // new one can name the dead key — strict renewal ⇒ re-enroll every
  // member, even though every member cert is unexpired.
  run nagRedeployReenrollsTest =
    toNagCliff
      .then(deploy)
      .then(unseal)
      .then(all { assert(serving and not(memberValid) and not(memberRenewable)), tick })
      .then(all { assert(not(memberValid) and not(memberRenewable)), reenroll })
      .then(all { assert(memberValid and reenrolled == 1 and maxStarved == MEMBER_RUNWAY + 1), tick })

  // Witness (finding 5): the hub redeploys at its nag boundary while the
  // member's cert is fresh (0 d old, 90 d to run — but its speak-as link
  // has 30 d). Under RENEW_ON_ROTATION the first served beat re-signs
  // it under the new key and it survives the old link's expiry with the
  // hub healthy. Without it (RENEW_ON_ROTATION=false) the age-based
  // renewal is 60 d away, the link dies at 30 d, and access is lost at
  // starvation 0.
  run rotationConvergesTest =
    initAtNag
      .then(deploy)
      .then(all { assert(sealed and memberProc != proc and memberSAExp == NAG), unseal })
      .then(all { assert(serving and memberProc != proc), tick })
      .then(all { assert(memberProc == proc and memberSAExp == speakAsExp and memberIssued == t), tick })
      .then(NAG.reps(_ => tick))
      .then(all { assert(t > NAG and memberValid and starved == 0 and reenrolled == 0), tick })
}
