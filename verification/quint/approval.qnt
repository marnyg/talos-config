// approval.qnt — machine approval + boot-token enrollment
// (talos-config-pwm, extended for ADR-0015 by talos-config-54n).
//
// Models invariant 6 as reworded 2026-08-16: "Machine configuration is
// hardware-selected and human-ratified. The MAC selects the node's
// declared config; approval — per-machine, single-use — ratifies the
// name↔hardware binding. Identity keys are minted on the machine,
// never derived from hardware." And ADR-0015's join path: the served
// config carries a short-TTL, MAC-bound, HMAC-derived enrollment TOKEN
// (verified statelessly — no pending table); the node mints its own
// key at first boot and redeems {pubkey, token} for a cert; a wiped
// machine re-enrolls at next boot with NO human act, and its address
// (MachineIP(master, MAC)) never moves.
//
// Approval is durable (git); the token is machine-held (in the served
// config); the cert is machine-held (its disk). The hub keeps only a
// VOLATILE replay guard (`seen`) that dies on redeploy, like every
// other piece of hub memory (hub.qnt).
//
// FINDING (2026-09-05, model-first; ruled same day, talos-config-vzj):
// ADR-0015 called the token "single-use" AND "stateless — no pending
// table". Both cannot hold: single-use needs a record of use. With a
// volatile record, a redeploy inside the token's TTL reopens a leaked
// token (`residualTokenReplayAcrossRedeploy` is violated by exactly
// that trace). ADR-0015 now says "single-use per hub process,
// TTL-bounded" and accepts the residual (strictly less exposure than
// serving the key). `invSingleUsePerEpoch` is the law that holds; the
// residual is checked NEGATIVELY by check.sh so that a future design
// change closing it is noticed.
//
// Minor finding, same ruling: a token that is only HMAC(master, mac,
// issue-time) is IDENTICAL for two serves in the same time bucket, so
// the replay guard would reject a legitimate wipe-and-reboot inside
// one bucket. The token carries a fresh nonce (random, embedded,
// covered by the HMAC — still stateless). Modeled as `nonce`.
//
// Check:
//   quint run    --invariant=invAll approval.qnt
//   quint verify --invariant=invAll --max-steps=12 approval.qnt
//   quint run    --invariant=residualTokenReplayAcrossRedeploy approval.qnt   # expected: violation
module approval {
  pure val MACS = Set("aa:aa", "bb:bb")
  pure val KEYS = 1.to(3)            // machine-born keypairs (opaque ids)
  pure val TTL = 2                   // token validity, in ticks

  // addr : MAC -> Addr — pure, the keypair never feeds it (invariant 1,
  // ADR-0015 step 3: address from MachineIP(master, MAC)).
  pure val addrOf = Map("aa:aa" -> 20, "bb:bb" -> 21)

  type MPhase = Blank | Approved | Member

  // `forged`: not HMAC(master, mac, issuedAt) — an attacker's guess.
  type Token = { mac: str, issuedAt: int, nonce: int, forged: bool }
  type Cert = { mac: str, key: int, addr: int, token: Token }
  type Redemption = { token: Token, epoch: int, at: int, key: int, approvedAtRedeem: bool }

  var machines: str -> MPhase
  var approvals: Set[str]           // git: ratified MACs (durable)
  var approvalActs: int             // history: wallet signatures spent on approval
  var decommissions: int            // history: machines removed from git
  var tokens: Set[Token]            // machine-held: in the served config
  var leaked: Set[Token]            // adversary-held copies of served tokens (never consumed)
  var certs: Set[Cert]              // machine-held: on its disk (dies with a wipe)
  var issuedEver: Set[Cert]         // history witness
  var seen: Set[Token]              // hub memory: volatile replay guard
  var epoch: int                    // hub process generation
  var redemptions: Set[Redemption]  // history witness
  var now: int
  var nextNonce: int                // randomness in the served token, modeled as a counter

  action init = all {
    machines' = MACS.mapBy(_ => Blank),
    approvals' = Set(),
    approvalActs' = 0,
    decommissions' = 0,
    tokens' = Set(),
    leaked' = Set(),
    certs' = Set(),
    issuedEver' = Set(),
    seen' = Set(),
    epoch' = 0,
    redemptions' = Set(),
    now' = 0,
    nextNonce' = 0,
  }

  // The human ratifies one specific machine: name↔hardware, recorded in
  // git. The model offers no path back to Blank, so this is the ONLY
  // wallet act a machine ever costs — reinstalls included.
  action approveMac(mac: str): bool = all {
    machines.get(mac) == Blank,
    machines' = machines.set(mac, Approved),
    approvals' = approvals.union(Set(mac)),
    approvalActs' = approvalActs + 1,
    decommissions' = decommissions,
    tokens' = tokens, leaked' = leaked, certs' = certs, issuedEver' = issuedEver,
    seen' = seen, epoch' = epoch, redemptions' = redemptions, now' = now, nextNonce' = nextNonce,
  }
  action approve = {
    nondet mac = MACS.oneOf()
    approveMac(mac)
  }

  // Decommission: the Owner deletes `talos/machines/<mac>/`. Only for a
  // machine that is not currently a member (a live cert is revoked by
  // expiry, not by git — out of scope here). A token already served to
  // it may still be in flight: redeem must re-check git.
  action decommissionMac(mac: str): bool = all {
    machines.get(mac) == Approved,
    machines' = machines.set(mac, Blank),
    approvals' = approvals.exclude(Set(mac)),
    decommissions' = decommissions + 1,
    approvalActs' = approvalActs, tokens' = tokens, leaked' = leaked, certs' = certs,
    issuedEver' = issuedEver, seen' = seen, epoch' = epoch, redemptions' = redemptions,
    now' = now, nextNonce' = nextNonce,
  }
  action decommission = {
    nondet mac = MACS.oneOf()
    decommissionMac(mac)
  }

  // Config serve: an approved machine boots, fetches its config over
  // HTTPS; the hub injects a token bound to its MAC. Stateless — the
  // hub records nothing.
  action serveConfigTo(mac: str): bool = all {
    approvals.contains(mac),
    tokens' = tokens.union(Set({ mac: mac, issuedAt: now, nonce: nextNonce, forged: false })),
    leaked' = leaked,
    nextNonce' = nextNonce + 1,
    machines' = machines, approvals' = approvals, approvalActs' = approvalActs, decommissions' = decommissions,
    certs' = certs, issuedEver' = issuedEver, seen' = seen, epoch' = epoch,
    redemptions' = redemptions, now' = now,
  }
  action serveConfig = {
    nondet mac = MACS.oneOf()
    serveConfigTo(mac)
  }

  // Adversary: presents a token that was never HMAC-derived.
  action forge = {
    nondet mac = MACS.oneOf()
    all {
      tokens' = tokens.union(Set({ mac: mac, issuedAt: now, nonce: 0, forged: true })),
      leaked' = leaked,
      machines' = machines, approvals' = approvals, approvalActs' = approvalActs, decommissions' = decommissions,
      certs' = certs, issuedEver' = issuedEver, seen' = seen, epoch' = epoch,
      redemptions' = redemptions, now' = now, nextNonce' = nextNonce,
    }
  }

  // Adversary: copies a served config (the same exposure that today
  // yields the machine's private key outright — ADR-0015 "strictly no
  // worse"). The copy is never consumed.
  action leak = all {
    tokens != Set(),
    {
      nondet tk = tokens.oneOf()
      all {
        leaked' = leaked.union(Set(tk)),
        machines' = machines, approvals' = approvals, approvalActs' = approvalActs, decommissions' = decommissions,
        tokens' = tokens, certs' = certs, issuedEver' = issuedEver, seen' = seen,
        epoch' = epoch, redemptions' = redemptions, now' = now, nextNonce' = nextNonce,
      }
    }
  }

  // Redeem: the node minted its key (nondet — nothing in the model maps
  // to it, ADR-0012/0015 non-derivability stated as absence) and posts
  // {pubkey, token}. The hub verifies HMAC, TTL, git approval for the
  // token's MAC, and its volatile replay guard; mints the cert with
  // name/groups from git and the derived address.
  action redeem = all {
    tokens.union(leaked).exists(tk => not(tk.forged) and now < tk.issuedAt + TTL
                        and approvals.contains(tk.mac) and not(seen.contains(tk))),
    {
      nondet tk = tokens.union(leaked).filter(tk => not(tk.forged) and now < tk.issuedAt + TTL
                                      and approvals.contains(tk.mac) and not(seen.contains(tk))).oneOf()
      nondet key = KEYS.oneOf()
      val c = { mac: tk.mac, key: key, addr: addrOf.get(tk.mac), token: tk }
      all {
        machines' = machines.set(tk.mac, Member),
        tokens' = tokens.exclude(Set(tk)),
        leaked' = leaked,
        certs' = certs.union(Set(c)),
        issuedEver' = issuedEver.union(Set(c)),
        seen' = seen.union(Set(tk)),
        redemptions' = redemptions.union(Set({ token: tk, epoch: epoch, at: now, key: key,
                                                approvedAtRedeem: approvals.contains(tk.mac) })),
        approvals' = approvals, approvalActs' = approvalActs, decommissions' = decommissions, epoch' = epoch, now' = now,
        nextNonce' = nextNonce,
      }
    }
  }

  // Wipe/reinstall: the disk is gone — key, cert, served config with it.
  // Approval in git survives, so the machine is Approved, not Blank.
  action wipeMac(mac: str): bool = all {
    machines.get(mac) == Member,
    machines' = machines.set(mac, Approved),
    certs' = certs.filter(c => c.mac != mac),
    tokens' = tokens.filter(tk => tk.mac != mac),
    leaked' = leaked,
    approvals' = approvals, approvalActs' = approvalActs, decommissions' = decommissions, issuedEver' = issuedEver,
    seen' = seen, epoch' = epoch, redemptions' = redemptions, now' = now, nextNonce' = nextNonce,
  }
  action wipe = {
    nondet mac = MACS.oneOf()
    wipeMac(mac)
  }

  // Hub crash/redeploy, enabled anywhere: hub memory dies. Durable
  // facts (git approvals) and machine-held facts (tokens, certs) survive.
  action redeploy = all {
    seen' = Set(),
    epoch' = epoch + 1,
    machines' = machines, approvals' = approvals, approvalActs' = approvalActs, decommissions' = decommissions,
    tokens' = tokens, leaked' = leaked, certs' = certs, issuedEver' = issuedEver,
    redemptions' = redemptions, now' = now, nextNonce' = nextNonce,
  }

  action tick = all {
    now' = now + 1,
    machines' = machines, approvals' = approvals, approvalActs' = approvalActs, decommissions' = decommissions,
    tokens' = tokens, leaked' = leaked, certs' = certs, issuedEver' = issuedEver,
    seen' = seen, epoch' = epoch, redemptions' = redemptions, nextNonce' = nextNonce,
  }

  action step = any { approve, decommission, serveConfig, forge, leak, redeem, wipe, redeploy, tick }

  // ---- laws ----------------------------------------------------------

  // Human-ratified, once: one wallet act per machine lifetime (a
  // decommissioned machine coming back is a new ratification) —
  // reinstalls never re-approve.
  val invOneApprovalPerMachine = approvalActs <= MACS.size() + decommissions

  // Identity-bound: membership only via a token minted for THAT MAC,
  // and only while git says the MAC is approved — a token served before
  // a decommission must not redeem after it.
  val invMemberWasApproved =
    MACS.forall(m => (machines.get(m) == Member) implies approvals.contains(m))
  val invRedeemedWhileApproved = redemptions.forall(r => r.approvedAtRedeem)
  val invTokenMacBound = issuedEver.forall(c => c.token.mac == c.mac)

  // Stateless verification still rejects forgeries and stale tokens.
  val invNoForgedRedeem = redemptions.forall(r => not(r.token.forged))
  val invTokenTTL = redemptions.forall(r => r.at < r.token.issuedAt + TTL)

  // Address is a function of the MAC: re-keying / reinstalling never
  // moves a machine (invariant 1, ADR-0015 step 3).
  val invAddrStable = issuedEver.forall(c1 => issuedEver.forall(c2 =>
    (c1.mac == c2.mac) implies (c1.addr == c2.addr)))

  // Keys are not derived from hardware: two certs for one MAC may carry
  // different keys — the model must not accidentally forbid re-keying.
  // (Checked as a witness in reinstallTest rather than an invariant.)

  // What the replay guard actually guarantees: one redemption per
  // (token, hub process).
  val invSingleUsePerEpoch = redemptions.forall(r1 => redemptions.forall(r2 =>
    (r1.token == r2.token and r1.epoch == r2.epoch) implies r1 == r2))

  // A live Member holds a live cert; a wiped machine holds none.
  val invMemberHoldsCert =
    MACS.forall(m => (machines.get(m) == Member) == certs.exists(c => c.mac == m))

  val invAll = and {
    invOneApprovalPerMachine,
    invMemberWasApproved,
    invRedeemedWhileApproved,
    invTokenMacBound,
    invNoForgedRedeem,
    invTokenTTL,
    invAddrStable,
    invSingleUsePerEpoch,
    invMemberHoldsCert,
  }

  // The accepted residual (ADR-0015 as amended): a leaked token redeems
  // again after a redeploy inside TTL clears `seen`. EXPECTED TO FAIL;
  // not part of invAll. If this ever holds, the design changed — update
  // ADR-0015 and check.sh together.
  val residualTokenReplayAcrossRedeploy = redemptions.forall(r1 => redemptions.forall(r2 =>
    (r1.token == r2.token) implies r1 == r2))

  // Witness: approve once; join; wipe; rejoin with a NEW key and the
  // SAME address, with no second approval.
  run reinstallTest =
    init
      .then(approveMac("aa:aa"))
      .then(serveConfigTo("aa:aa"))
      .then(redeem)
      .then(all { assert(machines.get("aa:aa") == Member), wipeMac("aa:aa") })
      .then(all { assert(machines.get("aa:aa") == Approved and certs == Set()), serveConfigTo("aa:aa") })
      .then(redeem)
      .then(all {
        assert(machines.get("aa:aa") == Member),
        assert(approvalActs == 1),
        assert(issuedEver.size() == 2),
        assert(issuedEver.forall(c => c.addr == 20)),
        tick,
      })
}
