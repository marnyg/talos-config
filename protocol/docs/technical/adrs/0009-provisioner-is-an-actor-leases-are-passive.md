# ADR-0009: The provisioner is an actor; leases are passive; children self-lapse

- Status: Proposed
- Date: 2026-09-24 (grill-design, `talos-config-0bc.4`)
- Builds: M4 (`protocol/spawn`, the provisioner binary and its
  drivers). Adds: invariant 13. Amends: sketch § Spawning ("P signs a
  lease with a provider" becomes a `#spawn` invocation; the lease
  handle becomes `(provisioner id, lease id)`), glossary **Spawner**,
  **Provisioner**, **Driver**, **Lease**, **Self-lapse**.

## Context and Problem Statement

The epic sketched a "provider iface (k8s Job + docker; Akash later)"
— a Go interface on the parent. That makes the parent hold platform
credentials (a kubeconfig on the laptop), lets each platform leak its
shape into the parent, and does not resemble how the one real market
we aim at (Akash) is used: you send someone a lease. Separately, "let
the lease lapse" needs a meaning on substrates with no money: what
kills a k8s child whose parent died, and a docker child on a host with
no native deadline?

## Decision Drivers

- **Everything is an actor** reached by a chain; no side channel.
- **Funding-tree semantics without money**: parent death must lapse
  children (sketch: supervision = funding).
- **Self-contained child image**: two substrates in v0 so nothing
  platform-specific can hide in the image or the parent.
- **Third parties can run a provisioner** with zero protocol
  knowledge beyond "actor with three facets".
- **Trust honesty**: the provisioner already sees the intro; do not
  also tell it when the handshake succeeded.

## Considered Options

Seam: a Go `Provider` interface on the parent vs **a provisioner
actor** (`#spawn`/`#extend`/`#kill`) with the Go interface demoted to
the provisioner's per-platform **driver**.

Lease semantics: active (parent calls `Kill` on a missed beat) vs
**passive** (deadline, extended on the beat).

Extension trigger: an `Actor.OnRenew` runtime hook; a second beat
(`#heartbeat`/poll); **a decorator on the installed `#renew` handler**.

Lapse without a native deadline (docker): adapter-side timers;
entrypoint wrappers; **the child exits on "no live edge"** + a
best-effort orphan sweep by label.

Provisioner state: tracks birth (observability) vs **never learns
about birth**.

Acceptance: protocol-only; one real substrate; **fake driver + k8s +
docker**.

## Decision Outcome

- **Spawner** (`protocol/spawn`, parent side) knows actors; sends
  `#spawn {image@digest, params: intro, until}` → `{lease}`,
  `#extend {lease, until}`, `#kill {lease}` to a provisioner it is
  configured with by default (selection optional per spawn). A
  provisioner is just another correspondent.
- **Provisioner** (its own actor, one generic binary) knows leases:
  `pending → running → {lapsed, killed}`, the same machine on every
  platform; renders them through `Driver{Start, Extend, Kill}`. It
  never learns about birth. Drivers live outside the protocol module.
- **Passive leases**: the birth window is the first deadline; the
  spawner's decorator on `#renew` sends `#extend {until: fresh.exp}`
  per cert re-issued to a born child, so lease lifetime = the renew
  cert's ttl. A failed `#extend` does not touch the reply.
- **Self-lapse**: the child binary exits when it holds no live edge
  (not: when its parent's edge expired — a grown-up child survives
  its parent). The provisioner's deadline guards against a subverted
  child; on docker that guard is an orphan sweep by label at the next
  start.
- **M4 acceptance**: fake driver in the protocol tests; a real spawn
  from a laptop over iroh into a k8s Job on the cluster and a docker
  container, through the same provisioner binary with different
  drivers.

### Consequences

- Good: the parent needs no platform credentials; a market is "a
  provisioner with a frontdoor"; the wire contract is three facets.
- Good: parent death lapses k8s children through
  `activeDeadlineSeconds` (mutability on a live Job to verify in the
  driver; fallback is the docker-style sweep).
- Bad: `#extend` is I/O inside a serial-mailbox handler; synchronous
  in v0, a goroutine if it ever hurts.
- Bad: docker's lapse is best-effort — a subverted child on docker
  runs until the next sweep.
- Open: the provisioner's own consent to its customers (v0: the
  parent's key; the frontdoor/negotiated-offer path is M5-adjacent).
