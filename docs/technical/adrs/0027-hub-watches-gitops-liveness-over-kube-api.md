# ADR-0027: The hub watches GitOps liveness over the control plane's kube-api facet

- Status: Proposed
- Date: 2026-09-30
- Related: ADR-0024 (the hub as a caller), `talos-config-9l67` (HA sweep
  slice 1), `config-server/clusterwatch.go`

## Context and Problem Statement

Git is the single source of truth (invariant 2), and ArgoCD's
application controller is what turns a push into cluster state. Twice
in one week it silently stopped doing that:

- 2026-09-21 → 09-29: `argocd-application-controller-0` (a StatefulSet
  pod) sat `Terminating` on a powered-off worker. StatefulSet pods on
  an unreachable node never reschedule, so nothing reconciled for 8
  days.
- 2026-09-29: a sync operation stayed `Running` for 9 h, waiting on the
  health of a wave‑0 resource (the gateway's ghost pod), while `apps`
  still read *Synced* because live state matched the last applied
  revision.

In both cases the only symptom was a push that never landed, and
nobody saw it until someone went looking. There is no monitoring stack
in the cluster. How should the owner find out that GitOps has stopped,
without adding a hosted account or a second source of truth?

## Decision Drivers

- The thing to detect is **absence**: a controller that no longer
  writes anything, or an operation that never finishes. A watcher that
  fires on status *changes* is blind to both.
- The watcher must not share the failure it watches. An in-cluster
  alerter can die with the same node, or starve on the same stuck sync.
- Invariant 3 / invariant 1: no third-party accounts as a root of
  anything. An alert sink (email, Telegram, PagerDuty) is an account
  someone else hosts.
- Invariant 2: the hub derives; it must not come to *own* cluster
  state. What it reads has to be a safe-to-lose observation
  (ADR-0019's cache class), never an input to authorisation.
- The owner already goes to one page when something looks wrong: the
  hub's `/status`.

## Considered Options

### Option A: the hub reads the root Application over cp1's `kube-api` facet (chosen)

Every 5 min, while auto-bootstrap sees etcd running, the hub dials the
control plane's `kube-api` facet as an ordinary caller (recipe row
`{facet: kube-api, host: hub}`, compiled like its existing `apid`
row). It sends one TLS `GET` for `argocd/apps`, authenticated by a
one-hour `system:masters` client cert minted from the cluster CA. The
hub already holds that CA key because it composes the control-plane
config. `/status` shows a `gitops` row and marks it warn if
`reconciledAt` is more than 20 min old, if a sync op has been
`Running` for more than 30 min, or if the poll fails.

- Pros: runs outside the cluster (fly), so it survives any node loss
  and any stuck sync. Detects both absence cases directly from two
  timestamps. No new account, no stored credential, no new
  dependency. Reuses the identity-plane dial auto-bootstrap already
  proved. Sealed or unreachable shows up as an explicit row state,
  not silence.
- Cons: `system:masters` is far broader than one `GET`. A pull page
  is not a push alert, so the owner still has to look. It watches one
  Application. It only runs while the hub is unsealed, and it rides
  auto-bootstrap's single-control-plane target.

### Option B: in-cluster Prometheus + Alertmanager

- Pros: industry standard, would cover much more than GitOps.
- Cons: large new footprint for a three-node homelab. Its pods fail
  the same way the ArgoCD controller did (StatefulSets on a dead node)
  unless given the same care. It still needs an external sink to
  reach the owner (Option C's problem).

### Option C: ArgoCD notifications controller → external sink

It is already installed, with an empty config.

- Pros: nearly zero code, and it pushes.
- Cons: its triggers evaluate application state that the dead
  controller no longer updates, so the 8-day case would not have
  fired. It runs inside the cluster it watches. The sink is a hosted
  account (invariant 3).

### Option D: Option A with an RBAC-scoped ServiceAccount token instead of a minted cert

- Pros: least privilege at the credential: `get` on
  `applications.argoproj.io` in `argocd`, nothing else.
- Cons: a durable bearer token has to live in fly secrets and be
  rotated. That is new state for the hub, which invariant 2 pushes
  against. And it buys nothing against a compromised hub: whoever
  holds the hub holds the cluster CA key and can mint
  `system:masters` anyway. The narrowing is cosmetic next to the
  authority the hub already has.

### Option E: do nothing; check `kubectl get app` when a push seems stuck

- Pros: free.
- Cons: this is what happened, twice.

## Decision Outcome

Chosen: **Option A**. It is the only option that detects the absence
cases from outside the failure domain without adding a hosted account
or stored credential. The broad client cert is accepted because it is
not new authority: the hub's authority over the cluster already *is*
the cluster CA key it compiles configs from (the same reasoning that
lets auto-bootstrap mint `os:admin` for `apid`).

Read-only is a property of the code, not of the credential. The
watcher issues exactly one `GET`, and any write would be a new
decision (see Confirmation).

### Consequences

- The recipe gains a second hub `host:` row. The hub dials cp1's
  `kube-api` every 5 min on top of the 30 s `apid` poll.
- `/status` answers "is GitOps alive?" with two ages. The snapshot is
  a safe-to-lose cache: a restart re-reads on the first poll.
- Push alerting is still missing. The owner sees the problem when
  they open `/status`. A push channel, if ever wanted, should hang off
  this same observation rather than a second watcher.
- With more than one control plane, auto-bootstrap refuses to pick a
  target and the row goes idle. The multi-CP ADR has to give it a
  target.
- The hub now has an idle, code-level path to the Kubernetes API. That
  is the easiest place to grow "the hub also fixes things", and it
  should not grow there by accident.
- _2026-10-04 (`cnb5`)_: the same poll gained a second `GET`, Longhorn's
  Volume list, rendered as a `storage` row (`config-server/storage.go`).
  It flags volumes whose `robustness` is `faulted` or `degraded` or
  that cannot schedule a replica, by PVC name. Why: the media library's
  three volumes sat `faulted` for 12 days while their pods stayed
  `Running` on hung NFS mounts and ArgoCD read `Healthy`. Same
  posture: one dial, one client cert, read-only, safe-to-lose
  snapshot. The cons line "it watches one Application" is now "one
  Application and one CRD list"; a third read should still be a
  conscious addition here, not a habit.

### Confirmation

- Correct when: scaling `argocd-application-controller` to 0 turns the
  row warn within ~25 min, and a sync op held `Running` for over
  30 min does the same. Not yet exercised live; the thresholds are
  unit-tested (`TestGitopsLine`).
- Revisit if: the hub needs to *act* on what it reads (it becomes a
  control loop: new ADR, and invariant 2's "servers derive" applies
  squarely); the cluster gains a monitoring stack that runs outside
  the failure domain; or `system:masters` minting becomes something
  the hub should lose (e.g. the cluster CA moving off the hub).
