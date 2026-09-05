// clock.qnt — time as a trust input (talos-config-439, ADR-0019).
//
// Every guarantee in ADR-0017/0018 is `exp > now`, and `now` at a
// verifier (node agent, gateway) is an unauthenticated NTP clock the
// LAN can spoof. Roll-forward only denies (availability). ROLLBACK
// resurrects expired certs — with revocation-is-expiry that is the
// cheapest attack on the whole design: LAN + old leaked cert + NTP.
//
// Ruling (439): time has two halves. The UPPER bound (denial) is the
// local clock — ops, not protocol. The LOWER bound (resurrection) is
// protocol-enforced: every cert carries `iat`; a verifier keeps a
// volatile monotone LOW-WATER MARK `lw` over the `iat` of every cert it
// verifies (all wallet-rooted issuers — member/invoke/speak-as/consent,
// never a member's self-issued reach-me-at), and checks
// `exp > max(local, lw)`. No time authority exists: the time facts come
// from the chain the receiver already trusts, in the one direction that
// cannot grant access. What rollback can resurrect is bounded by the
// verifier's own STARVATION — the same clock runway.qnt measures.
//
// The model: real time `t` (ground truth), a verifier whose local clock
// an adversary sets freely, honest issuers (iat = t) and — because the
// hub hot key is a trusted issuer that may be compromised or have a bad
// clock — a LYING issuer that signs certs with a future `iat`. Any
// issued cert may be presented at any time (stolen certs are just
// certs). Volatile: `restart` clears the mark, as hub.qnt clears hub
// memory.
//
// FINDING (2026-09-06, model-first): the first draft of the ruling
// capped the mark's advance at `local + SKEW` "so a lying issuer cannot
// push lw into the future". CAP=true makes invLWCoversHonestSeen and
// invResurrectionBounded FAIL: after a rollback every HONEST fresh cert
// looks like it is from the future, the cap ignores exactly the
// evidence that would have caught the rollback, and the stolen expired
// cert is accepted. The cap protects against the weaker adversary (a
// trusted issuer lying, who could already mint any cert) by disabling
// the defence against the stronger one (LAN NTP). Ruled: NO cap
// (CAP=false). Residual: a lying trusted issuer can push lw forward and
// deny until `restart` — strictly less than what that issuer can do
// already; `invHonestCorrectClockAccepted` is therefore stated for
// liar-free histories only.
//
// Laws:
//   invLWCoversHonestSeen        lw ≥ iat of every honest cert verified since restart
//   invResurrectionBounded       an accepted cert that is truly expired (exp ≤ t)
//                                expired AFTER every honest iat the verifier had
//                                seen — resurrection ⊆ starvation window; liars
//                                cannot widen it
//   invHonestCorrectClockAccepted  correct clock + honest issuers ⇒ the mark never
//                                causes a false reject (no runway cost)
//   invNoAcceptBeyondLocal       roll-forward only denies (documentation)
//
// Check:
//   quint run    --invariant=invAll --max-steps=30 clock.qnt
//   quint verify --invariant=invAll --max-steps=10 clock.qnt
//   (CAP=true ⇒ invAll violated — the FINDING above)
module clock {
  pure val HORIZON = 8        // real-time ticks the model explores
  pure val LIFE = 3           // cert lifetime, ticks
  pure val SKEW = 1
  pure val CAP = false        // FINDING: true disables the defence
  pure val LYING_ISSUER = true

  pure def max(a: int, b: int): int = if (a > b) a else b

  // honest: iat ≤ real time at issuance (the issuer's clock was right).
  type Cert = { iat: int, exp: int, honest: bool }
  // One presentation, with the verifier's view at that moment: `hs` is
  // the max honest iat it had verified so far, `liar` whether any
  // dishonest cert had fed its mark.
  type Outcome = { cert: Cert, t: int, local: int, lwBefore: int, hs: int, liar: bool, accepted: bool }

  var t: int
  var local: int
  var lw: int
  var hs: int
  var liar: bool
  var certs: Set[Cert]
  var seen: Set[Cert]
  var outcomes: Set[Outcome]

  // The verifier's rule. A verified cert first advances the mark, then
  // is judged against it (its own iat < exp, so it never rejects itself).
  pure def advance(lw0: int, c: Cert, loc: int): int =
    if (CAP and c.iat > loc + SKEW) lw0 else max(lw0, c.iat)
  pure def admits(c: Cert, loc: int, lw0: int): bool = c.exp > max(loc, lw0)

  action init = all {
    t' = 0, local' = 0, lw' = 0, hs' = 0, liar' = false,
    certs' = Set(), seen' = Set(), outcomes' = Set(),
  }

  action unchangedClock = all { t' = t, local' = local }
  action unchangedMark = all { lw' = lw, hs' = hs, liar' = liar, seen' = seen }

  // Real time advances; a well-behaved local clock advances with it.
  action tick = all {
    t < HORIZON,
    t' = t + 1, local' = local + 1,
    certs' = certs, outcomes' = outcomes, unchangedMark,
  }

  // The adversary (LAN NTP, physical RTC) sets the local clock anywhere.
  action rollback = all {
    t >= 1,
    nondet v = 0.to(t - 1).oneOf()
    local' = v,
    t' = t, certs' = certs, outcomes' = outcomes, unchangedMark,
  }
  action rollforward = all {
    nondet v = (t + 1).to(HORIZON + LIFE + 1).oneOf()
    local' = v,
    t' = t, certs' = certs, outcomes' = outcomes, unchangedMark,
  }

  action issueHonest = all {
    certs' = certs.union(Set({ iat: t, exp: t + LIFE, honest: true })),
    outcomes' = outcomes, unchangedClock, unchangedMark,
  }
  // A trusted issuer with a wrong or hostile clock: future iat.
  action issueLying = all {
    LYING_ISSUER,
    nondet f = (t + 1).to(HORIZON + 1).oneOf()
    certs' = certs.union(Set({ iat: f, exp: f + LIFE, honest: false })),
    outcomes' = outcomes, unchangedClock, unchangedMark,
  }

  // Any issued cert is presented (stolen or not — the verifier cannot
  // tell); signature verification is authorize.qnt's job and assumed.
  action present = all {
    certs != Set(),
    nondet c = certs.oneOf()
    val lw1 = advance(lw, c, local)
    all {
      lw' = lw1,
      seen' = seen.union(Set(c)),
      hs' = if (c.honest) max(hs, c.iat) else hs,
      liar' = (liar or not(c.honest)),
      outcomes' = outcomes.union(Set({ cert: c, t: t, local: local, lwBefore: lw, hs: hs, liar: liar,
                                       accepted: admits(c, local, lw1) })),
      certs' = certs, unchangedClock,
    }
  }

  // Volatile mark: a verifier restart forgets everything but its clock.
  action restart = all {
    lw' = 0, hs' = 0, liar' = false, seen' = Set(),
    outcomes' = Set(),          // laws are about what this mark has seen
    certs' = certs, unchangedClock,
  }

  action step = any { tick, rollback, rollforward, issueHonest, issueLying, present, restart }

  // ---- laws ----------------------------------------------------------

  val invLWCoversHonestSeen =
    seen.forall(c => c.honest implies c.iat <= lw)

  // A truly expired cert that is accepted expired after every honest iat
  // this verifier had seen: rollback resurrects only what died inside
  // the starvation window, and liars cannot widen it.
  val invResurrectionBounded =
    outcomes.forall(o => (o.accepted and o.cert.exp <= o.t) implies o.cert.exp > o.hs)

  // With a correct clock and no liar in the history, the mark never
  // rejects a live honest cert — the defence costs no runway.
  val invHonestCorrectClockAccepted =
    outcomes.forall(o =>
      (o.local == o.t and o.cert.honest and o.cert.exp > o.t and not(o.liar)) implies o.accepted)

  // Roll-forward can only deny.
  val invNoAcceptBeyondLocal =
    outcomes.forall(o => o.accepted implies o.cert.exp > o.local)

  val invAll = and {
    invLWCoversHonestSeen,
    invResurrectionBounded,
    invHonestCorrectClockAccepted,
    invNoAcceptBeyondLocal,
  }

  // Sanity: the attack the mark exists for is actually expressible —
  // an honest cert, time passes past its expiry, a fresh honest cert is
  // seen, the clock is rolled back, the stale cert is presented and
  // REJECTED although local time says it is live.
  run rollbackCaughtTest =
    init.then(issueHonest)               // cert A: iat 0, exp 3
      .then(tick).then(tick).then(tick).then(tick)   // t = 4, A expired
      .then(issueHonest)                 // cert B: iat 4
      .then(all { certs' = certs, outcomes' = outcomes, unchangedMark,
                  t' = t, local' = 0 })  // rollback to 0
      .then(all {                        // verify B: mark → 4
        nondet c = certs.filter(c => c.iat == 4).oneOf()
        val lw1 = advance(lw, c, local)
        all { lw' = lw1, seen' = seen.union(Set(c)), hs' = max(hs, c.iat), liar' = liar,
              outcomes' = outcomes.union(Set({ cert: c, t: t, local: local, lwBefore: lw, hs: hs, liar: liar,
                                               accepted: admits(c, local, lw1) })),
              certs' = certs, unchangedClock }
      })
      .then(all {                        // present A: local says live, mark says dead
        nondet c = certs.filter(c => c.iat == 0).oneOf()
        val lw1 = advance(lw, c, local)
        all { lw' = lw1, seen' = seen.union(Set(c)), hs' = max(hs, c.iat), liar' = liar,
              outcomes' = outcomes.union(Set({ cert: c, t: t, local: local, lwBefore: lw, hs: hs, liar: liar,
                                               accepted: admits(c, local, lw1) })),
              certs' = certs, unchangedClock }
      })
      .then(all {
        assert(outcomes.exists(o => o.cert.iat == 0 and o.local == 0 and not(o.accepted))),
        assert(lw == 4),
        step,
      })
}
