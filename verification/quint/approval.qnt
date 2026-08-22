// approval.qnt — machine approval linearity (talos-config-pwm).
//
// Models invariant 6: "Machine identity is hardware-anchored and
// human-ratified. Approval is per-machine, single-use, identity-bound."
// An approval is an affine resource: minted for exactly one MAC,
// consumable at most once, and consuming it is the only way a machine
// becomes a member.
//
// Check:
//   quint run    --invariant=invAll approval.qnt
//   quint verify --invariant=invAll approval.qnt
module approval {
  pure val MACS = Set("aa:aa", "bb:bb")

  type MPhase = Blank | Approved | Member

  type Grant = { token: int, mac: str }

  var machines: str -> MPhase
  var outstanding: Set[Grant]   // minted, not yet consumed
  var consumed: Set[Grant]      // history witness
  var nextToken: int

  action init = all {
    machines' = MACS.mapBy(_ => Blank),
    outstanding' = Set(),
    consumed' = Set(),
    nextToken' = 0,
  }

  // The human ratifies one specific machine: the grant is bound to its
  // MAC at mint time (identity-bound half of invariant 6).
  action approve = all {
    MACS.filter(m => machines.get(m) == Blank) != Set(),
    {
      nondet mac = MACS.filter(m => machines.get(m) == Blank).oneOf()
      all {
        machines' = machines.set(mac, Approved),
        outstanding' = outstanding.union(Set({ token: nextToken, mac: mac })),
        nextToken' = nextToken + 1,
        consumed' = consumed,
      }
    }
  }

  // Joining consumes the grant — for ITS machine only. The model
  // offers no action that consumes a grant for a different MAC or
  // consumes one twice (it leaves `outstanding` on use).
  action join = all {
    outstanding != Set(),
    {
      nondet g = outstanding.oneOf()
      all {
        machines' = machines.set(g.mac, Member),
        outstanding' = outstanding.exclude(Set(g)),
        consumed' = consumed.union(Set(g)),
        nextToken' = nextToken,
      }
    }
  }

  // Terminal state: every machine ratified and joined. Explicit
  // stutter so the bounded checker doesn't report the finite world's
  // natural end as a deadlock.
  action halt = all {
    MACS.forall(m => machines.get(m) == Member),
    machines' = machines,
    outstanding' = outstanding,
    consumed' = consumed,
    nextToken' = nextToken,
  }

  action step = any {
    approve,
    join,
    halt,
  }

  // Single-use: no grant is both outstanding and consumed, and no
  // token appears twice in the consumed history.
  val invSingleUse = and {
    outstanding.intersect(consumed) == Set(),
    consumed.forall(g1 => consumed.forall(g2 =>
      (g1.token == g2.token) implies (g1 == g2))),
  }

  // Identity-bound: a member machine got there via a grant minted for
  // exactly that MAC — membership without a matching consumed grant is
  // forgery.
  val invIdentityBound = MACS.forall(m =>
    (machines.get(m) == Member) implies consumed.exists(g => g.mac == m))

  // Human-ratified: at most one grant is ever minted per machine
  // (approval is per-machine, not a reusable capability class).
  val invOneGrantPerMachine = MACS.forall(m =>
    outstanding.union(consumed).filter(g => g.mac == m).size() <= 1)

  val invAll = and {
    invSingleUse,
    invIdentityBound,
    invOneGrantPerMachine,
  }
}
