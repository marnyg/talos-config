# ADR-0051: The protocol network is its own public surface; the hub is one consumer

- Status: Proposed 2026-11-28. **Ruled by the owner the same day:** the
  amendment to invariant 5 and the goal line ("the hub is one consumer
  among many") — both are in `desired-state/`. **Proposed, not yet
  reviewed:** the shape of the new deployment below. Nothing is built
  or deployed; the one thing measured is cross-relay dialing (see
  Confirmation).
- Date: 2026-11-28
- Related: spike `talos-config-k7sd` (this), spike `talos-config-qx5c`
  (relay admission for sovereign actors — moves to the new surface),
  `talos-config-5gz` (the hub relay's member gate, observe-only today),
  ADR-0022 (relay behind the hub's 443), ADR-0024 (hub = iroh
  endpoint), ADR-0020 / decision `5w1` (the protocol is the repo's
  center), protocol ADR-0007 (lighthouse), thread `talos-config-kckm`
  (the hub as a spawner of machines-as-actors).

## Context and Problem Statement

The owner wants agents to use the sovereign-actor protocol with no
dependency on the talos deployment or the cluster, and long term wants
the hub to be one of many consumers of the protocol, probably from a
different repo.

Today that is not possible, for two reasons that are both deployment,
not protocol:

- **Every actor homes on the hub's relay.** `iroh-relay` runs as a
  child of `config-server` on the one fly app
  (`actors/cmd/internal/boot` `DefaultRelay`). A hub deploy bounces
  every actor's relay connection, and the hub's member gate cannot
  enforce because actors hold protocol keys, not member certs
  (`relayAdmitter.gate=false`, `qx5c`).
- **Rendezvous needs the cluster.** The lighthouse is already its own
  binary (`actors/cmd/lighthouse`) but runs in the cluster with its
  key on a Longhorn volume (`k8s/apps/sap-lighthouse`).

Invariant 5 said the hub is the only public surface, unscoped, so a
second deployment was a violation until amended.

## Decision Drivers

- Protocol goal "deployment-independent" and protocol invariant 3 (no
  authority above an identity): the network's own infrastructure must
  not be a talos component.
- Root invariant 4 (the mesh is post-bootstrap) and 5: talos
  provisioning, recovery and member paths must not gain a dependency.
- No fleet reconfiguration for members (Android app, gateway,
  `irohup` all carry the hub's relay URL).
- Root invariant 2: the new surface holds no state beyond an actor's
  own key.

## Considered Options

- **A — one app, as today.** No new surface, but the protocol cannot
  run without the hub, and the member gate stays observe-only.
- **B — a second app for the protocol network: `iroh-relay` +
  `cmd/lighthouse`; the hub keeps its relay for members.** Two public
  surfaces, each with one job. No member moves.
- **C — one relay for everyone, in the new app; the hub homes on it.**
  The hub becomes a pure consumer at once, but every member's relay
  URL changes, and the talos member path then depends on the protocol
  network's surface.
- **D — split `config-server` itself across apps** (issuer, enroll,
  provisioner). Rejected: those actors share one unseal; it buys
  nothing toward the goal.

## Decision Outcome

**B.** Invariant 5 is scoped to the talos deployment, with two rules:
nothing on the talos provisioning, recovery or member path depends on
the protocol network's surface, and nothing in that surface depends on
the hub, the cluster or a wallet unseal.

The new deployment (proposed shape):

- One fly app, built from `actors/image.nix`: `iroh-relay` and
  `cmd/lighthouse`. No `config-server` code, no talos tree, no unseal.
- The lighthouse's key lives on a fly volume — its id is the network
  bundle its members hold, so it outlives the process.
- The provisioner, the parent and their children home on the new
  relay; the in-cluster lighthouse is retired once members hold the
  new id.
- `DefaultRelay` stops naming the hub.

C is where "the hub is one consumer" eventually points, but it is not
taken now: it spends the first rule above.

### Consequences

- Good: the protocol runs with the hub sealed, redeploying, or absent,
  and with the cluster down.
- Good: with no actors on it, the hub relay's member gate can enforce.
- Bad / to watch: relay admission is not solved, it moves. An open
  relay is free bandwidth for strangers; `qx5c` is now the new
  surface's question. _(2026-11-28: answered as a proposal in protocol
  ADR-0012 — the actor presents a `relay` chain to a gate in front of
  the relay; that gate, not bare `iroh-relay`, is what the new app
  runs.)_
- Bad / to watch: **the caller, not the receiver, connects to the
  foreign relay.** Under an enforcing hub gate a non-member actor
  cannot dial a member homed on the hub relay, while a member can dial
  an actor if the protocol relay admits it. No such edge exists today
  (the parent's web UI is reached through the gateway over a k8s
  Service, not over iroh); the first one needs a decision.
- Bad / to watch: `boot.ViaRelay` seeds the lighthouse dial hint with
  the actor's **own** `-relay`. Correct while every actor shares the
  lighthouse's relay; an actor homed elsewhere needs the lighthouse's
  relay stated separately.
- Ops: a second deploy script and a second Gatus check. Fly cost not
  checked; no dedicated IPv4 is needed (443 only).
- Not decided here: extracting `protocol/`, `actors/` and
  `iroh-transport/` to their own repo. It waits until this split has
  proven the boundary.

### Confirmation

- Measured 2026-11-28: `TestCrossRelayDial` (`iroh-transport`) — two
  endpoints homed on two different `iroh-relay` servers, the caller's
  relay map holding only its own, meet on an `iroh:relay=<other>` hint
  alone; a control caller pointed at a relay the receiver is not homed
  on does not connect. Local dev relays over plain HTTP on loopback,
  not through fly's proxy.
- Right if: an agent on a machine that has never seen this deployment
  joins the network and spawns a child (the goal's check), and a hub
  deploy no longer shows up in any actor's log.
- Wrong if: the new surface ends up needing hub state to admit actors
  — that is the second rule failing, and the answer is a different
  admission design, not a call back to the hub.
