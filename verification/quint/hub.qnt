// hub.qnt — seal lifecycle of the config-server hub (talos-config-bw5).
//
// Models docs/desired-state/domain-model.md "Unseal" + invariant 2:
// the hub's durable content is a pure function of (git, master); every
// thing else — sessions, pending enrollments, the policy overlay — is
// ephemeral BY DESIGN and dies with the process. `deploy` is enabled
// in every state: the checker explores a crash/redeploy at every
// point, which is exactly what prose reasoning is bad at.
//
// Check:
//   quint run    --invariant=invAll hub.qnt
//   quint verify --invariant=invAll hub.qnt   (Apalache, needs JVM)
module hub {
  type Phase = Sealed | Unsealed
  type MaybeOverlay = NoOverlay | Overlay(int)

  var phase: Phase
  var sessions: Set[int]     // device-flow sessions, pending enrollments
  var overlay: MaybeOverlay  // ephemeral policy overlay (ADR-0014)
  var nextId: int

  def hasOverlay(o: MaybeOverlay): bool = match o {
    | NoOverlay => false
    | Overlay(_) => true
  }

  action init = all {
    phase' = Sealed,
    sessions' = Set(),
    overlay' = NoOverlay,
    nextId' = 0,
  }

  // One wallet signature (re)creates the master. Nothing carries over:
  // a fresh unseal starts from derived state only.
  action unseal = all {
    phase == Sealed,
    phase' = Unsealed,
    sessions' = Set(),
    overlay' = NoOverlay,
    nextId' = nextId,
  }

  // Crash or redeploy — possible at ANY moment, no guard. Ephemeral
  // state must die with the process (invariant 2: nothing is lost,
  // because nothing durable lived outside git + master).
  action deploy = all {
    phase' = Sealed,
    sessions' = Set(),
    overlay' = NoOverlay,
    nextId' = nextId,
  }

  action startSession = all {
    phase == Unsealed,
    sessions' = sessions.union(Set(nextId)),
    nextId' = nextId + 1,
    phase' = phase,
    overlay' = overlay,
  }

  action installOverlay = {
    nondet p = 1.to(3).oneOf()
    all {
      phase == Unsealed,
      overlay' = Overlay(p),
      phase' = phase,
      sessions' = sessions,
      nextId' = nextId,
    }
  }

  action clearOverlay = all {
    phase == Unsealed,
    overlay' = NoOverlay,
    phase' = phase,
    sessions' = sessions,
    nextId' = nextId,
  }

  action step = any {
    unseal,
    deploy,
    startSession,
    installOverlay,
    clearOverlay,
  }

  // A sealed hub holds NO ephemeral state: if either of these is
  // violated, something survived a process boundary that should not
  // have — a durable-state leak (invariant 2 violation).
  val invOverlayNeedsUnseal = hasOverlay(overlay) implies (phase == Unsealed)
  val invSessionsNeedUnseal = (sessions != Set()) implies (phase == Unsealed)

  val invAll = and {
    invOverlayNeedsUnseal,
    invSessionsNeedUnseal,
  }
}
