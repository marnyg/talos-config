// enroll.qnt — device enrollment lifecycle (talos-config-lad).
//
// Models ADR-0012 + invariant 1 (device half): device keys are born
// on-device (here: nondet, never derived from anything in the model);
// membership is a wallet-signed act minting a cert; the address is a
// function of the NAME only, so re-keying / re-enrolling never moves a
// device. Pending enrollments are ephemeral (die on redeploy); issued
// certs are NOT hub state — they live on devices and must survive.
//
// Check:
//   quint run    --invariant=invAll enroll.qnt
//   quint verify --invariant=invAll enroll.qnt
module enroll {
  // Small closed worlds keep the state space checkable.
  pure val NAMES = Set("phone", "tv", "laptop")
  pure val KEYS = 1.to(4)   // device-born keypairs (opaque ids)

  // addr : Name -> Addr — a PURE map, the model-level statement of the
  // law that the keypair never feeds the address (L7 in the rapid
  // suite; nebderive.DeviceIP takes master+name, never the key).
  pure val addrOf = Map("phone" -> 10, "tv" -> 11, "laptop" -> 12)

  type Pending = { nonce: int, name: str }
  type Cert = { nonce: int, name: str, key: int, addr: int, expiry: int }

  var pending: Set[Pending]   // hub memory: challenge issued, not yet ratified
  var certs: Set[Cert]        // held by devices, NOT by the hub
  var issuedEver: Set[Cert]   // history witness (never cleared by anyone)
  var now: int
  var nextNonce: int

  action init = all {
    pending' = Set(),
    certs' = Set(),
    issuedEver' = Set(),
    now' = 0,
    nextNonce' = 0,
  }

  // Device submits its pubkey, hub records a single-use challenge.
  action challenge = {
    nondet name = NAMES.oneOf()
    all {
      pending' = pending.union(Set({ nonce: nextNonce, name: name })),
      nextNonce' = nextNonce + 1,
      certs' = certs,
      issuedEver' = issuedEver,
      now' = now,
    }
  }

  // Wallet approval ratifies (name, group); one signature mints the
  // cert and CONSUMES the challenge. The key is drawn fresh here —
  // the model has no function from anything to the key (ADR-0012's
  // non-derivability, stated as absence).
  action mint = all {
    pending != Set(),
    {
      nondet p = pending.oneOf()
      nondet key = KEYS.oneOf()
      val c = { nonce: p.nonce, name: p.name, key: key,
                addr: addrOf.get(p.name), expiry: now + 90 }
      all {
        pending' = pending.exclude(Set(p)),
        certs' = certs.union(Set(c)),
        issuedEver' = issuedEver.union(Set(c)),
        nextNonce' = nextNonce,
        now' = now,
      }
    }
  }

  action tick = all {
    now' = now + 1,
    pending' = pending,
    certs' = certs,
    issuedEver' = issuedEver,
    nextNonce' = nextNonce,
  }

  // Crash/redeploy, enabled anywhere: hub memory (pending) dies;
  // device-held certs MUST survive — they were never hub state.
  action redeploy = all {
    pending' = Set(),
    certs' = certs,
    issuedEver' = issuedEver,
    now' = now,
    nextNonce' = nextNonce,
  }

  action step = any {
    challenge,
    mint,
    tick,
    redeploy,
  }

  // Nonce single-use: a challenge mints at most one cert. (mint
  // consumes the pending entry; this invariant is the observable
  // consequence and catches any regression to replayable challenges.)
  val invNonceSingleUse = issuedEver.forall(c1 =>
    issuedEver.forall(c2 => (c1.nonce == c2.nonce) implies (c1 == c2)))

  // Re-enrolling a name — new nonce, new device-born key — never moves
  // its address (invariant 1: "re-keying or re-enrolling never moves a
  // device").
  val invAddrStable = issuedEver.forall(c1 =>
    issuedEver.forall(c2 => (c1.name == c2.name) implies (c1.addr == c2.addr)))

  // Certs survive redeploy: nothing ever removes an issued cert, so
  // the live set equals the history. If a crash path ever dropped
  // device certs (treating them as hub state), this breaks.
  val invCertsSurviveRedeploy = certs == issuedEver

  val invAll = and {
    invNonceSingleUse,
    invAddrStable,
    invCertsSurviveRedeploy,
  }
}
