# ADR-0014: Mesh access policy as git data with an ephemeral wallet-gated overlay

- Status: Proposed
- Date: 2026-08-16

## Context and Problem Statement

The mesh's who×what admission table (which cert identity reaches which
port on which member class) lived as Go constants spread across three
render sites (`nebconf.go`, `nebmachine.go`, `nebdevice.go`). Changing
who-reaches-what was a code change, and experimenting with a rule
(e.g. widening a group's reach to test a device) cost a full
commit→deploy→unseal cycle per iteration. Where should policy live,
and how can a candidate policy be exercised before it is committed?

## Decision Drivers

- Invariant 2: git is the single source of truth for the control
  plane; servers derive, they do not own durable state. "If a slice of
  this design seems to need a database, redesign it."
- Invariant 6 adjacency: policy is *payload*, not identity — syncing
  rules must never move certs, keys or addresses (sketch `6462fed4`'s
  key insight).
- The experiment loop: trying a rule should not cost a commit per
  iteration, but nothing tried may silently become permanent.
- The /status security model: sessions gate viewing; every mutation is
  a per-action wallet signature (a stolen cookie must not rewrite the
  mesh firewall).
- Propagation reality: device rules apply at enrollment, node rules at
  `apply`, hub rules at unseal — live push is future work (phases 3–4).

## Considered Options

### Option A: Policy stays in Go constants

Status quo ante. Rules are code; tests guard them.

- Pros: zero new machinery; strongest possible review gate (a diff).
- Cons: who-reaches-what is an ops decision expressed as a code
  change; no experiment loop at all; three render sites can drift.

### Option B: Hub-UI-owned durable ACL

The hub stores the policy table (file/DB on the fly volume), edited
through a UI; git holds nothing or a stale copy.

- Pros: instant edits, no propagation story needed for the hub itself.
- Cons: rejected outright by invariant 2 — a reseal either loses the
  table or the hub needs a database; git and reality diverge with no
  reconciliation; exactly the "server owns state" failure mode.

### Option C: Node-side policy agent

An agent on each member watches a policy endpoint and rewrites the
local firewall (the Omni line).

- Pros: live propagation everywhere.
- Cons: new machinery on every member; apid push needs no agent when
  live sync does land; expands the trusted surface on nodes; rejected
  in the design session.

### Option D: Git file + ephemeral in-memory hub overlay (chosen)

`talos/mesh-policy.yaml` is the durable who×what table; all three
render sites derive from it (phase 1, `019ce97`). The hub can hold a
validated in-memory replacement — installed and cleared via
wallet-signed actions on `/policy`, shown as a git→overlay diff with
export text — that every subsequent config render composes from
(phase 2, `06014d6`). A restart or redeploy reverts to git.

- Pros: git remains the only durable owner (the overlay *cannot*
  outlive the process); experiment loop without commits; same
  validation path for file and overlay; export-to-commit closes the
  loop back into git.
- Cons: while an overlay is installed, configs the hub composes are
  not recomputable from git alone (bounded by the process lifetime and
  visible on /policy); hub's own firewall renders at unseal, so the
  hub scope effectively always rides git until live sync; an operator
  can forget to export before a deploy (the loss is the safe
  direction).

## Decision Outcome

Chosen: **Option D**, because it gives the experiment loop without
surrendering invariant 2: the overlay is transient wallet-authorized
state in the same class as sessions and enrollment flows — designed to
be lost, never load-bearing. Reading of invariant 2 affirmed here:
"anything a server remembers must be recomputable" constrains *durable*
memory; ephemeral state whose loss is safe and whose authority is a
wallet signature does not violate it.

### Consequences

- Changing standing policy is a YAML commit; testing a candidate is a
  signature; the two can never be confused because only the commit
  survives.
- Phase 3–4 (live sync: `/policy` endpoint + device hot-reload, apid
  push to nodes + unseal reconciliation) build on the same
  `effectivePolicy()` seam.
- The propagation lag table (device/enroll, node/apply, hub/unseal)
  must be kept visible wherever policy is edited, or overlay
  experiments will be misread as no-ops.

### Confirmation

- A hub redeploy with an overlay installed comes back serving exactly
  the git file (verified by design: memory-only, no persistence path).
- Tests: overlay round-trip, render-through into device configs,
  rejection of invalid documents, signature binding (sha256 + nonce),
  and the repo file remains guarded by the phase-1 tests.
- Invalidated if: an overlay ever needs to survive a restart (that is
  Option B knocking — redesign instead), or live sync lands and makes
  the propagation-lag rationale obsolete (revisit the page copy, not
  the ownership model).
