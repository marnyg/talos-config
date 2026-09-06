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
// verifies ON A CHAIN ROOTED AT ITSELF (its own consents, speak-as certs
// for consented principals, member/invoke certs whose resolved issuer is
// a consented principal — never a stranger's self-signed cert, never a
// member's self-issued reach-me-at), and checks `exp > max(local, lw)`.
// No time authority exists: the time facts come from the chain the
// receiver already trusts, in the one direction that cannot grant
// access. What rollback can resurrect is bounded by the verifier's own
// STARVATION — the same clock runway.qnt measures.
//
// The model: real time `t` (ground truth), a verifier whose local clock
// an adversary sets freely, and three issuer classes:
//   Honest    trusted (rooted) issuer, correct clock: iat = t
//   Lying     trusted (rooted) issuer with a wrong or hostile clock —
//             the hub hot key may be compromised — signs a future iat
//   Stranger  a peer with a validly self-signed cert and NO consent
//             chain to the receiver; its iat is adversary-chosen
//             anywhere (past or future)
// Any issued cert may be presented at any time (stolen certs are just
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
// Note (2026-09-06, code-first, from the 0bc.1 review; issue zev): the
// Stranger class was added after the Go port. The first draft of
// `protocol/cert` fed EVERY signature-valid cert in the bundle to the
// mark, so any peer able to connect could present a self-signed cert
// with `iat` far in the future and deny the receiver to everyone until
// restart — the lying-issuer residual above, but for an adversary who
// holds no authority at all. ADR-0019 and the glossary already carried
// the fix ("rooted at the receiver"; `Result.Verified` in
// `protocol/cert/authorize.go` lists only rooted certs); this model
// now says it too. A stranger's cert is unverifiable (protocol
// invariant 2: no receiver-signed first link), so authorize() rejects
// it AND it never touches the mark — the two laws below. Not a
// FINDING: nothing was refuted, the ruling was ahead of the model.
//
// Laws:
//   invLWCoversHonestSeen        lw ≥ iat of every honest cert verified since restart
//   invResurrectionBounded       an accepted cert that is truly expired (exp ≤ t)
//                                expired AFTER every honest iat the verifier had
//                                seen — resurrection ⊆ starvation window; liars
//                                cannot widen it
//   invHonestCorrectClockAccepted  correct clock + honest issuers ⇒ the mark never
//                                causes a false reject (no runway cost); strangers
//                                in the history do not count as liars
//   invNoAcceptBeyondLocal       roll-forward only denies (documentation)
//   invStrangerNeverAdvances     lw == max iat over the ROOTED certs seen since
//                                restart (0 if none) — a stranger never moves it
//   invStrangerNeverAccepted     a stranger's cert is never accepted, whatever
//                                exp, local or lw
//
// Check:
//   quint test clock.qnt
//   quint run    --invariant=invAll --max-steps=30 clock.qnt
//   quint verify --invariant=invAll --max-steps=10 clock.qnt
//   (CAP=true ⇒ invAll violated — the FINDING above)
//   (mutant "advance on every cert" ⇒ invStrangerNeverAdvances and
//    invHonestCorrectClockAccepted violated — the stranger denial)
module clock {
  pure val HORIZON = 8        // real-time ticks the model explores
  pure val LIFE = 3           // cert lifetime, ticks
  pure val SKEW = 1
  pure val CAP = false        // FINDING: true disables the defence
  pure val LYING_ISSUER = true
  pure val STRANGERS = true

  pure def max(a: int, b: int): int = if (a > b) a else b

  // Who signed the cert, as the verifier can tell from the chain:
  //   Honest / Lying   rooted at the receiver (a consented issuer); the
  //                    verifier cannot distinguish the two — only whether
  //                    the issuer's clock was right (iat ≤ t at issuance)
  //   Stranger         not rooted: signature valid, no chain to R
  // A sum type rather than `honest`+`rooted` bools: the three classes are
  // exhaustive and exclusive, and an unrooted cert's "honesty" is
  // meaningless — the verifier never reads its iat.
  type Kind = Honest | Lying | Stranger
  type Cert = { iat: int, exp: int, kind: Kind }
  // One presentation, with the verifier's view at that moment: `hs` is
  // the max honest iat it had verified so far, `liar` whether any
  // dishonest ROOTED cert had fed its mark.
  type Outcome = { cert: Cert, t: int, local: int, lwBefore: int, hs: int, liar: bool, accepted: bool }

  pure def isHonest(c: Cert): bool = match c.kind { Honest => true | _ => false }
  pure def isLying(c: Cert): bool = match c.kind { Lying => true | _ => false }
  pure def isStranger(c: Cert): bool = match c.kind { Stranger => true | _ => false }
  // Rooted at the receiver ⇔ on a consent chain (authorize.go buildRooted).
  pure def isRooted(c: Cert): bool = not(isStranger(c))

  var t: int
  var local: int
  var lw: int
  var hs: int
  var liar: bool
  var certs: Set[Cert]
  var seen: Set[Cert]
  var outcomes: Set[Outcome]

  // The verifier's rule. A verified ROOTED cert first advances the mark,
  // then is judged against it (its own iat < exp, so it never rejects
  // itself). An unrooted cert is unverifiable: it neither advances the
  // mark nor can be admitted.
  pure def advance(lw0: int, c: Cert, loc: int): int =
    if (not(isRooted(c))) lw0
    else if (CAP and c.iat > loc + SKEW) lw0
    else max(lw0, c.iat)
  pure def admits(c: Cert, loc: int, lw0: int): bool =
    isRooted(c) and c.exp > max(loc, lw0)

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
    certs' = certs.union(Set({ iat: t, exp: t + LIFE, kind: Honest })),
    outcomes' = outcomes, unchangedClock, unchangedMark,
  }
  // A trusted issuer with a wrong or hostile clock: future iat.
  action issueLying = all {
    LYING_ISSUER,
    nondet f = (t + 1).to(HORIZON + 1).oneOf()
    certs' = certs.union(Set({ iat: f, exp: f + LIFE, kind: Lying })),
    outcomes' = outcomes, unchangedClock, unchangedMark,
  }
  // A stranger self-signs whatever it likes: a past iat (harmless, but
  // must still not count), a plausible one (= t, indistinguishable from
  // an honest issuer's by value alone) or a far-future one (the denial
  // attack). Three representatives rather than 0..HORIZON+1: the full
  // range verifies (10 steps) in ~200 s, over the ~2 min budget; the
  // three keep it at ~110 s and every mutant below still dies.
  action issueStranger = all {
    STRANGERS,
    nondet f = Set(0, t, HORIZON + 1).oneOf()
    certs' = certs.union(Set({ iat: f, exp: f + LIFE, kind: Stranger })),
    outcomes' = outcomes, unchangedClock, unchangedMark,
  }

  // The verifier handles one presented cert: update the mark (rooted
  // only), then judge. Shared by `present` and the witness runs.
  action presentCert(c: Cert): bool =
    val lw1 = advance(lw, c, local)
    all {
      lw' = lw1,
      seen' = seen.union(Set(c)),
      hs' = if (isHonest(c)) max(hs, c.iat) else hs,
      liar' = (liar or isLying(c)),
      outcomes' = outcomes.union(Set({ cert: c, t: t, local: local, lwBefore: lw, hs: hs, liar: liar,
                                       accepted: admits(c, local, lw1) })),
      certs' = certs, unchangedClock,
    }

  // Any issued cert is presented (stolen or not — the verifier cannot
  // tell); signature verification is authorize.qnt's job and assumed.
  // Whether the chain is ROOTED is what the verifier CAN tell, and it is
  // the only thing it reads off a stranger's cert.
  action present = all {
    certs != Set(),
    nondet c = certs.oneOf()
    presentCert(c),
  }

  // Volatile mark: a verifier restart forgets everything but its clock.
  action restart = all {
    lw' = 0, hs' = 0, liar' = false, seen' = Set(),
    outcomes' = Set(),          // laws are about what this mark has seen
    certs' = certs, unchangedClock,
  }

  action step = any { tick, rollback, rollforward, issueHonest, issueLying, issueStranger, present, restart }

  // ---- laws ----------------------------------------------------------

  val invLWCoversHonestSeen =
    seen.forall(c => isHonest(c) implies c.iat <= lw)

  // A truly expired cert that is accepted expired after every honest iat
  // this verifier had seen: rollback resurrects only what died inside
  // the starvation window, and liars cannot widen it.
  val invResurrectionBounded =
    outcomes.forall(o => (o.accepted and o.cert.exp <= o.t) implies o.cert.exp > o.hs)

  // With a correct clock and no liar in the history, the mark never
  // rejects a live honest cert — the defence costs no runway. Strangers
  // in the history are NOT liars: they must not be able to cause this.
  val invHonestCorrectClockAccepted =
    outcomes.forall(o =>
      (o.local == o.t and isHonest(o.cert) and o.cert.exp > o.t and not(o.liar)) implies o.accepted)

  // Roll-forward can only deny.
  val invNoAcceptBeyondLocal =
    outcomes.forall(o => o.accepted implies o.cert.exp > o.local)

  // The mark is exactly the max iat over the ROOTED certs seen since
  // restart: strangers never move it, in either direction.
  val invStrangerNeverAdvances =
    lw == seen.filter(c => isRooted(c)).fold(0, (m, c) => max(m, c.iat))

  // A stranger's cert is unverifiable, hence never accepted.
  val invStrangerNeverAccepted =
    outcomes.forall(o => isStranger(o.cert) implies not(o.accepted))

  val invAll = and {
    invLWCoversHonestSeen,
    invResurrectionBounded,
    invHonestCorrectClockAccepted,
    invNoAcceptBeyondLocal,
    invStrangerNeverAdvances,
    invStrangerNeverAccepted,
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
        presentCert(c)
      })
      .then(all {                        // present A: local says live, mark says dead
        nondet c = certs.filter(c => c.iat == 0).oneOf()
        presentCert(c)
      })
      .then(all {
        assert(outcomes.exists(o => o.cert.iat == 0 and o.local == 0 and not(o.accepted))),
        assert(lw == 4),
        step,
      })

  // Witness: the stranger denial does NOT happen — a self-signed cert
  // from the far future is presented, is rejected, leaves the mark where
  // it was, and a live honest cert presented right after is accepted.
  run strangerDenialTest =
    init.then(all {                      // stranger S: iat = HORIZON+1 (far future)
        certs' = certs.union(Set({ iat: HORIZON + 1, exp: HORIZON + 1 + LIFE, kind: Stranger })),
        outcomes' = outcomes, unchangedClock, unchangedMark })
      .then(tick)                        // t = 1
      .then(issueHonest)                 // cert B: iat 1, exp 4
      .then(all {                        // present S
        nondet c = certs.filter(c => isStranger(c)).oneOf()
        presentCert(c)
      })
      .then(all {
        assert(lw == 0),                 // the stranger did not move the mark
        assert(outcomes.exists(o => isStranger(o.cert) and not(o.accepted))),
        nondet c = certs.filter(c => isHonest(c)).oneOf()
        presentCert(c)                   // B: accepted, mark → 1
      })
      .then(all {
        assert(outcomes.exists(o => isHonest(o.cert) and o.accepted)),
        assert(lw == 1),
        step,
      })
}
