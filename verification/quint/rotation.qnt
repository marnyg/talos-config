// rotation.qnt — root rotation via a guardian-signed epoch chain
// (talos-config-aja2.1; spike aja2, design pins 1–4).
//
// The protocol's structural trade-off "root capture has no in-protocol
// remedy" is replaced by: an identity is an address plus an epoch-0
// POLICY {root, guardians, k} it commits to (pin 1). The GUARDIAN SET is
// the identity's root; the root key is a hot key with a longer leash
// (pin 3). A ROTATION is k `rotate-root` certs with identical bodies
// {epoch, prev = H(policy_n), policy_{n+1}}, one per distinct guardian
// of policy_n (pin 4); it verifies offline by signatures alone. A
// verifier keeps its own PIN (epoch-0 policy) and the verifying
// rotations it has been shown, and derives its VIEW from them: the
// chain of rotations from the pin, one per epoch. Two verifying rotations from the same (epoch,
// prev) with different `policy` are EQUIVOCATION = capture: the
// identity is dead at that verifier until re-pinned out of band (pin 2).
//
// The model: keys are ints; `H(policy)` is a small id into a fixed
// policy table (one id per distinct body). Three parties:
//   Honest    holds every key the thief does not; rotates from its tip
//             only, once per epoch, with every honest guardian of the
//             tip signing (so it can rotate iff it still holds ≥ k)
//   Thief     holds the epoch-0 ROOT key plus 0..2 guardian keys
//             (chosen at init); signs any rotation from any prev —
//             including a prev policy it invented — with all it holds
//   Verifier  one verifier, pinned at policy_0, shown rotations and
//             delegations in any order; `restart` loses everything it
//             has been shown (the pin itself is durable)
// The verifier's rule is the pure `verifies` / `view` / `admits` triple
// below; a Go port mirrors it 1:1: check signatures on receipt, keep
// what verifies, recompute the view from the pin and what is held —
// order-independent by construction.
//
// FINDING (2026-10-13, model-first): pin 2 as written — "two VERIFYING
// rotations from the same prev → verifier rejects both" — is a free
// kill-switch for ANYONE if "verifying" means only "k signatures by
// guardians of the policy the body names": the thief (or a stranger
// holding no key of the identity) invents a policy P' whose guardians
// are its own keys, signs two rotations from P' with different nexts,
// and every verifier that applies the rule as written declares the
// identity dead. ROOTED_EQUIVOCATION_ONLY=false keeps that reading
// checkable (invDeadOnlyByRootedEquivocation is violated). Ruled by the
// model: equivocation evidence counts only at a prev the verifier's own
// chain has reached — "verifying" in pin 2 must read "verifying AND
// rooted at the verifier's pin", exactly the condition pin 1 already
// puts on advancing. Pin 2's conclusion stands; its premise gains one
// word. Nothing else in pins 1–4 was refuted.
//
// Modelling notes (not findings): (a) `H(policy_n)` alone is ambiguous
// under policy recurrence (P0→PA→P0 puts P0 at epochs 0 and 2), so
// chaining and equivocation key on (epoch, prev) — cav carries both
// (pin 4); (b) two rotations with the same (epoch, prev, next) but
// different signers or exp are the same transition, not equivocation;
// (c) `exp` is in the rotation record and read by nothing — pin 4's
// "exp bounds assembly, not validity"; assembly itself is not modelled
// (a rotation is born with its signers).
//
// Laws (invAll):
//   invThiefNeverAdvances       thief < k on every honest policy ⇒ the
//                               verifier's policy is one the honest set signed
//   invMonotone                 the view's epoch never decreases (since restart)
//   invRootedAdvance            the view is a chain from the pin: one verifying
//                               rotation per epoch, each prev = previous next
//   invEquivocationKills        two verifying rotations, same (epoch, prev) on
//                               the chain, different next ⇒ dead — whichever
//                               order, even after advancing on one
//   invDeadOnlyByRootedEquivocation  dead ⇒ such a pair exists AT the chain
//                               (the FINDING; false under the knob)
//   invDeadAcceptsNothing       dead ⇒ no delegation accepted
//   invOldRootDiesOnAdvance     a delegation is accepted only from the root of
//                               the view's CURRENT policy
//   invExpiredRotationStillProves  a chained verifying rotation advances the
//                               view whatever its exp vs. now
//   invHonestChainReaches       thief weak, every honest rotation shown, not
//                               dead ⇒ the view is at the honest tip
// Residuals (checked NEGATIVELY by check.sh — see there):
//   residualTruncatedHistory    pin 1: a verifier not shown the rotation still
//                               accepts the old (stolen) root
//   residualDeadStaysDead       pin 2: a restart loses the equivocation record
//                               and the verifier is alive at epoch 0 again
//
// Check:
//   quint test rotation.qnt
//   quint run    --invariant=invAll --max-steps=20 rotation.qnt
//   quint verify --invariant=invAll --max-steps=7 rotation.qnt   (115–140 s)
//   (ROOTED_EQUIVOCATION_ONLY=false ⇒ invDeadOnlyByRootedEquivocation violated)
module rotation {
  pure val MAX_EPOCH = 3
  pure val LIFE = 2                    // rotation exp = t + LIFE
  pure val HORIZON = 4
  pure val THIEF_GUARDIANS = Set(0, 1, 2)   // guardian keys the thief may also hold
  pure val ROOTED_EQUIVOCATION_ONLY = true  // FINDING: false is the kill-switch

  // A policy is referred to by its hash; here the hash is a small id into
  // POL (structural equality on the body would do, ids are cheaper to
  // check). The Go port's `prev` IS this hash.
  type Policy = { root: int, guardians: Set[int], k: int }
  type Rotation = { epoch: int, prev: int, next: int, signers: Set[int], exp: int }
  type Link = { epoch: int, prev: int, next: int }
  type View = { epoch: int, policy: int, dead: bool, stuck: bool, trail: Set[Link] }
  // Flat ints/bools only (Apalache cost): `chained` = the rotation verifies
  // and continues the view's chain (prev = current policy, epoch = next).
  type RotOutcome = { chained: bool, expired: bool, epochBefore: int, deadBefore: bool, epochAfter: int, deadAfter: bool }
  type DelOutcome = { root: int, epoch: int, curRoot: int, dead: bool, honestRoot: int, accepted: bool }

  pure val KEYS = 1.to(6)
  pure val P0 = 0   // birth policy; key 1 = the stolen root
  pure val PA = 1   // honest: new root
  pure val PB = 2   // honest: new root + guardians
  pure val PT = 3   // the thief's capture target / invented prev
  pure val PX = 4   // witness only: what a root-only thief rotates to
  pure val POL: int -> Policy = Map(
    P0 -> { root: 1, guardians: Set(2, 3, 4, 5), k: 2 },
    PA -> { root: 6, guardians: Set(2, 3, 4, 5), k: 2 },
    PB -> { root: 6, guardians: Set(4, 5, 6), k: 2 },
    PT -> { root: 1, guardians: Set(1), k: 1 },
    PX -> { root: 2, guardians: Set(2), k: 1 },
  )
  pure val HONEST_NEXTS = Set(P0, PA, PB)
  pure val POLICIES = Set(P0, PA, PB, PT)
  pure val ROOTS = Set(1, 2, 6)
  pure def pol(i: int): Policy = POL.get(i)

  // ---- the verifier's rule (pure; the Go port mirrors these) --------

  // k-of-n by signature alone: distinct guardians of prev, at least k.
  // `exp` is deliberately not read (pin 4).
  pure def verifies(r: Rotation): bool =
    r.signers.subseteq(pol(r.prev).guardians) and r.signers.size() >= pol(r.prev).k

  // Over rotations that already verified (the verifier keeps no other).
  pure def equivocal(a: Rotation, b: Rotation): bool =
    a.epoch == b.epoch and a.prev == b.prev and a.next != b.next

  // The view: fold the chain from the pin, one epoch at a time over the
  // VERIFIED rotations held. At each epoch the candidates are those from
  // the current policy at that epoch; >1 distinct next = equivocation
  // (dead here, forever); 0 = the chain ends; 1 = advance.
  pure def view(pin: int, seen: Set[Rotation]): View =
    val v0 = 1.to(MAX_EPOCH).fold(
      { epoch: 0, policy: pin, dead: false, stuck: false, trail: Set() },
      (v, e) =>
        if (v.dead or v.stuck) v
        else
          val cands = seen.filter(r => r.prev == v.policy and r.epoch == e)
          val nexts = cands.map(r => r.next)
          if (nexts.size() > 1) { ...v, dead: true }
          else if (nexts.size() == 0) { ...v, stuck: true }
          else
            val c = cands.fold({ epoch: e, prev: v.policy, next: v.policy, signers: Set(), exp: 0 }, (_, r) => r)  // nonempty, all share next
            { epoch: c.epoch, policy: c.next, dead: false, stuck: false,
              trail: v.trail.union(Set({ epoch: c.epoch, prev: v.policy, next: c.next })) })
    if (ROOTED_EQUIVOCATION_ONLY) v0
    else { ...v0, dead: v0.dead or seen.exists(a => seen.exists(b => equivocal(a, b))) }

  // A delegation signed by `root` is accepted iff the view is alive and
  // `root` is the CURRENT policy's root (pin 3: the root delegates,
  // guardians only rotate; old roots die with their epoch).
  pure def admits(v: View, root: int): bool =
    not(v.dead) and pol(v.policy).root == root

  // ---- state ---------------------------------------------------------

  var t: int
  var pin: int                // the verifier's epoch-0 pin (never changes)
  var thief: Set[int]         // keys the thief holds (never changes)
  var honestTip: int          // the honest guardian set's current policy
  var honestEpoch: int
  var rots: Set[Rotation]     // every rotation anyone has signed
  var honestRots: Set[Rotation]
  var seen: Set[Rotation]     // the VERIFYING rotations the verifier has been shown (volatile)
  var v: View                 // = view(pin, seen), cached as state (Apalache cost)
  var everDead: bool          // ghost: survives restart
  var rotOutcomes: Set[RotOutcome]
  var delOutcomes: Set[DelOutcome]

  val honest = KEYS.exclude(thief)
  val honestPolicies = Set(pin).union(honestRots.map(r => r.next))
  def thiefCanSign(p: int): bool = thief.intersect(pol(p).guardians).size() >= pol(p).k
  val thiefWeak = honestPolicies.forall(p => not(thiefCanSign(p)))

  action initWith(p: int, th: Set[int]): bool = all {
    t' = 0, pin' = p, thief' = th, honestTip' = p, honestEpoch' = 0,
    rots' = Set(), honestRots' = Set(), seen' = Set(), v' = view(p, Set()), everDead' = false,
    rotOutcomes' = Set(), delOutcomes' = Set(),
  }
  action init = all {
    nondet n = THIEF_GUARDIANS.oneOf()
    initWith(P0, Set(1).union(pol(P0).guardians.filter(g => g <= 1 + n))),
  }

  action unchangedWorld = all { rots' = rots, honestRots' = honestRots, honestTip' = honestTip, honestEpoch' = honestEpoch }
  action unchangedVerifier = all { seen' = seen, v' = v, rotOutcomes' = rotOutcomes, delOutcomes' = delOutcomes, everDead' = everDead }
  action unchangedPins = all { pin' = pin, thief' = thief }

  action tick = all {
    t < HORIZON, t' = t + 1,
    unchangedWorld, unchangedVerifier, unchangedPins,
  }

  // The honest guardians rotate from their tip: every honest guardian of
  // the tip signs the same body; possible iff they still number ≥ k.
  action signHonest = all {
    honestEpoch < MAX_EPOCH,
    val signers = honest.intersect(pol(honestTip).guardians)
    all {
      signers.size() >= pol(honestTip).k,
      nondet nx = HONEST_NEXTS.exclude(Set(honestTip)).oneOf()
      val r = { epoch: honestEpoch + 1, prev: honestTip, next: nx, signers: signers, exp: t + LIFE }
      all {
        rots' = rots.union(Set(r)), honestRots' = honestRots.union(Set(r)),
        honestTip' = nx, honestEpoch' = honestEpoch + 1,
      }
    },
    t' = t, unchangedVerifier, unchangedPins,
  }

  // The thief signs whatever it can: any prev (a real policy or its own
  // invention PT), any next, any epoch, with every key it holds or just
  // those among prev's guardians (< k of them when it holds < k).
  action signThief = all {
    nondet prev = POLICIES.oneOf()
    nondet nx = POLICIES.exclude(Set(prev)).oneOf()
    nondet e = 1.to(MAX_EPOCH).oneOf()
    nondet signers = Set(thief, thief.intersect(pol(prev).guardians)).oneOf()
    rots' = rots.union(Set({ epoch: e, prev: prev, next: nx, signers: signers, exp: t + LIFE })),
    honestRots' = honestRots, honestTip' = honestTip, honestEpoch' = honestEpoch,
    t' = t, unchangedVerifier, unchangedPins,
  }

  // The verifier is shown one rotation (any order; stale or expired ones
  // included), checks its signatures, keeps it if they verify, and
  // recomputes its view.
  action showRotation(r: Rotation): bool =
    val seen1 = if (verifies(r)) seen.union(Set(r)) else seen   // mutant: `verifies(r) and r.exp > t` — expiry is not validity
    val v1 = view(pin, seen1)
    all {
      seen' = seen1, v' = v1,
      everDead' = (everDead or v1.dead),
      rotOutcomes' = rotOutcomes.union(Set({
        chained: verifies(r) and r.prev == v.policy and r.epoch == v.epoch + 1, expired: r.exp <= t,
        epochBefore: v.epoch, deadBefore: v.dead, epochAfter: v1.epoch, deadAfter: v1.dead })),
      delOutcomes' = delOutcomes,
      t' = t, unchangedWorld, unchangedPins,
    }
  action present = all {
    rots != Set(),
    nondet r = rots.oneOf()
    showRotation(r),
  }

  // A delegation signed by some root key is presented (the thief holds
  // the epoch-0 root key; stolen delegations are just delegations).
  action presentDelegation(root: int): bool = all {
    delOutcomes' = delOutcomes.union(Set({ root: root, epoch: v.epoch, curRoot: pol(v.policy).root, dead: v.dead,
                                           honestRoot: pol(honestTip).root, accepted: admits(v, root) })),
    seen' = seen, v' = v, rotOutcomes' = rotOutcomes, everDead' = everDead,
    t' = t, unchangedWorld, unchangedPins,
  }
  action delegate = all {
    nondet root = ROOTS.oneOf()
    presentDelegation(root),
  }

  // Volatile verifier: everything shown is lost, the pin survives.
  action restart = all {
    seen' = Set(), v' = view(pin, Set()), rotOutcomes' = Set(), delOutcomes' = Set(), everDead' = everDead,
    t' = t, unchangedWorld, unchangedPins,
  }

  action step = any { tick, signHonest, signThief, present, delegate, restart }

  // ---- laws ----------------------------------------------------------

  // Case 1: a thief holding < k guardian keys of every honest policy
  // never moves the verifier onto a policy the honest set did not sign.
  val invThiefNeverAdvances = thiefWeak implies honestPolicies.contains(v.policy)

  // Case 2: epoch is monotone while alive (per verifier, since restart;
  // a dead view reports the epoch of the equivocation) ...
  val invMonotone = rotOutcomes.forall(o => not(o.deadAfter) implies o.epochAfter >= o.epochBefore)

  // ... and the view is a chain rooted at the pin: every link is backed
  // by a verified rotation it was shown, link 1 leaves the pin, each
  // link leaves the previous link's next.
  val invRootedAdvance = and {
    v.epoch == 0 implies v.policy == pin,
    v.trail.forall(l => and {
      seen.exists(r => r.epoch == l.epoch and r.prev == l.prev and r.next == l.next),
      if (l.epoch == 1) l.prev == pin else v.trail.exists(m => m.epoch == l.epoch - 1 and m.next == l.prev),
    }),
  }

  // `r.prev` is the policy the view's chain is at just before r.epoch.
  def onChain(r: Rotation): bool =
    if (r.epoch == 1) r.prev == pin else v.trail.exists(l => l.epoch == r.epoch - 1 and l.next == r.prev)

  // Case 3: equivocation at a prev the chain has reached kills, in
  // either order, even when the verifier had advanced on one of them.
  val invEquivocationKills =
    v.dead or seen.forall(a => seen.forall(b => not(equivocal(a, b) and onChain(a))))

  // The FINDING: death needs a rooted pair — an invented prev kills nothing.
  val invDeadOnlyByRootedEquivocation =
    v.dead implies seen.exists(a => seen.exists(b => equivocal(a, b) and onChain(a)))

  val invDeadAcceptsNothing = delOutcomes.forall(o => o.dead implies not(o.accepted))

  // Case 4: after the verifier advances, the old root's delegations are
  // rejected; only the current policy's root delegates.
  val invOldRootDiesOnAdvance = delOutcomes.forall(o => o.accepted implies o.root == o.curRoot)

  // Case 6: a chained verifying rotation advances the view whether or
  // not its exp has passed (unless it equivocates).
  val invExpiredRotationStillProves = rotOutcomes.forall(o =>
    (o.chained and not(o.deadBefore) and not(o.deadAfter))
    implies o.epochAfter >= o.epochBefore + 1)   // >= : a buffered later epoch may chain at once; `expired` unread

  // Liveness: shown the whole honest chain, a weak thief cannot keep the
  // verifier from the honest tip.
  val invHonestChainReaches =
    (thiefWeak and honestRots.subseteq(seen) and not(v.dead)) implies (v.policy == honestTip and v.epoch == honestEpoch)

  val invAll = and {
    invThiefNeverAdvances,
    invMonotone,
    invRootedAdvance,
    invEquivocationKills,
    invDeadOnlyByRootedEquivocation,
    invDeadAcceptsNothing,
    invOldRootDiesOnAdvance,
    invExpiredRotationStillProves,
    invHonestChainReaches,
  }

  // ---- residuals (EXPECTED TO FAIL; not in invAll) --------------------

  // Pin 1: a weak thief's stolen root is accepted by a verifier that has
  // not been shown the honest rotation (fresh, starved, or truncated).
  val residualTruncatedHistory =
    delOutcomes.forall(o => (thiefWeak and o.accepted) implies o.root == o.honestRoot)

  // Pin 2: the equivocation record is not safe-to-lose — a restart
  // revives a dead identity at epoch 0 (git holds the durable copy).
  val residualDeadStaysDead = everDead implies v.dead

  // ---- witnesses -----------------------------------------------------

  // Case 7: guardians0 = {root}, k = 1 — today's security, declared: the
  // thief alone rotates the identity wherever it likes.
  run rootOnlyCaptureTest =
    initWith(PT, Set(1, 2))                                              // pin {root 1, guardians {1}, k 1}
      .then(showRotation({ epoch: 1, prev: PT, next: PX, signers: Set(1), exp: LIFE }))
      .then(presentDelegation(2))
      .then(all {
        assert(v.epoch == 1 and v.policy == PX and not(v.dead)),
        assert(delOutcomes.exists(o => o.root == 2 and o.accepted)),
        assert(honest.intersect(pol(honestTip).guardians) == Set()),   // nobody else can ever rotate
        step,
      })

  // Case 8: thief with < k guardian keys cannot rotate; with = k it
  // captures; and if the honest set also rotates from the same prev,
  // both are dead at every verifier that sees both.
  run compromisedGuardiansTest =
    initWith(P0, Set(1, 2, 3))                                            // = k of P0's guardians
      .then(showRotation({ epoch: 1, prev: P0, next: PT, signers: Set(2), exp: LIFE }))       // < k
      .then(all { assert(v.epoch == 0 and v.policy == P0), delegate })
      .then(showRotation({ epoch: 1, prev: P0, next: PT, signers: Set(2, 3), exp: LIFE }))    // = k: capture
      .then(presentDelegation(1))
      .then(all {
        assert(v.epoch == 1 and v.policy == PT and not(v.dead)),
        assert(delOutcomes.exists(o => o.epoch == 1 and o.root == 1 and o.accepted)),
        signHonest,                                                       // {4, 5} sign P0 → PA or PB
      })
      .then(all {
        nondet r = honestRots.oneOf()
        showRotation(r)
      })
      .then(presentDelegation(1))
      .then(presentDelegation(6))
      .then(all {
        assert(v.dead and everDead),
        assert(delOutcomes.forall(o => o.dead implies not(o.accepted))),
        assert(delOutcomes.exists(o => o.dead and o.root == 1) and delOutcomes.exists(o => o.dead and o.root == 6)),
        step,
      })

  // Case 3, both orders — and the pin-2 residual: a restart in between
  // revives the verifier at epoch 0 until it is shown the pair again.
  run equivocationOrderTest =
    val hon = { epoch: 1, prev: P0, next: PA, signers: Set(4, 5), exp: LIFE }
    val bad = { epoch: 1, prev: P0, next: PT, signers: Set(2, 3), exp: LIFE }
    initWith(P0, Set(1, 2, 3))
      .then(showRotation(hon))
      .then(all { assert(v.epoch == 1 and v.policy == PA), showRotation(bad) })
      .then(all { assert(v.dead), restart })
      .then(all { assert(not(v.dead) and v.epoch == 0 and everDead), showRotation(bad) })  // residualDeadStaysDead
      .then(all { assert(v.epoch == 1 and v.policy == PT), showRotation(hon) })
      .then(all { assert(v.dead), step })

  // Cases 4 + 5 + 6: the honest set rotates; the unshown verifier still
  // accepts the stolen root (residualTruncatedHistory); the rotation,
  // shown after its exp has passed, still advances the view; the old
  // root is then rejected.
  run truncatedThenExpiredRotationTest =
    initWith(P0, Set(1, 2))
      .then(signHonest)                                     // exp = 2
      .then(presentDelegation(1))
      .then(all { assert(delOutcomes.exists(o => o.root == 1 and o.accepted and o.honestRoot == 6)), tick })
      .then(tick).then(tick)                                // t = 3 > exp
      .then(all {
        nondet r = honestRots.oneOf()
        all { assert(r.exp < t), showRotation(r) }
      })
      .then(presentDelegation(1))
      .then(presentDelegation(6))
      .then(all {
        assert(v.epoch == 1 and pol(v.policy).root == 6),
        assert(delOutcomes.exists(o => o.root == 1 and o.curRoot == 6 and not(o.accepted))),
        assert(delOutcomes.exists(o => o.root == 6 and o.accepted)),
        step,
      })
}
