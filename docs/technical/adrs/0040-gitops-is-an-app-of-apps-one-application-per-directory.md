# ADR-0040: GitOps is an app of apps — one ArgoCD Application per `k8s/apps` directory

- Status: Accepted 2026-10-22 (owner). Live: cutover the same day,
  44 Applications Synced/Healthy, no workload restarted.
- Date: 2026-10-22
- Related: ADR-0027 (the hub's gitops row reads the root Application),
  invariant 2 (git is the single source of truth); chore
  `talos-config-1ova` (the motivating failure); task `talos-config-pe8m`
  (this change); thread `talos-config-2c2x` (hub visibility into the
  children). Shape borrowed from mono-dees-nuts'
  `clusters/<cluster>/cluster.yaml`.

## Context and Problem Statement

Since the first cluster the root ArgoCD Application `apps` recursed
`k8s/apps` as one resource set: ~300 resources, one sync operation.
A sync operation is atomic per Application, so one resource that
never became healthy anywhere in the tree — a DataVolume import that
hung (vms/, 2026-09-29), a runner that crashed on first start
(forgejo-runner, 2026-10-17) — held every other directory's changes
until the operation was terminated by hand. Sync waves, SSA and
health gating were likewise repo-wide choices (`p0ar`), and the only
Applications with their own lifecycle were the nested Helm charts.

## Decision Drivers

- A push to one directory must land regardless of another directory's
  health; the failure domain of a sync should be the thing that
  changed.
- Per-directory sync semantics (waves, SSA, hooks) without inflicting
  them on everything else.
- Git stays the whole truth (invariant 2): adding a workload is a
  directory and a line, with no bootstrap-time step.
- One cluster, one owner: no environment variance, no tenancy, so
  layering (overlays, per-tenant projects) is cost without a buyer.
- Cutover on a live cluster without restarting or re-creating anything.

## Considered Options

### Option A: Keep one recursive root; drop the sync waves

Remove the waves from `vms/` so no resource gates another. The stall
goes away for waves but not for health: an Application's automated
sync still waits on the previous operation, and one hook or one
wedged CRD still holds the tree.

- Pros: zero structural change.
- Cons: fixes one instance of the symptom, not the shape that produces
  it; `vms/` loses the ordering it needs.

### Option B: Hand-written child Applications under the root

A `k8s/apps/<name>/application.yaml` per directory, the root recursing
only those. Same decoupling as C.

- Pros: no ApplicationSet; every child is a plain file.
- Cons: 37 near-identical files whose template drifts by hand; the
  retry/sync policy fix that saved cold boots elsewhere would have to
  be applied 37 times.

### Option C: ApplicationSet app of apps (chosen)

Root syncs `k8s/clusters/homelab/`: an ApplicationSet with a list
generator emits `platform-<name>` for each `k8s/apps/<name>`, next to
the AppProjects. One template, one list, 37 Applications.

- Pros: one policy in one place; adding a workload is one line;
  removing one deletes its resources through the ApplicationSet-added
  finalizer, explicitly; matches a shape already proven on another
  cluster.
- Cons: a generator between git and the Application (one more
  controller to understand); the hub's gitops row now sees only the
  root (`2c2x`).

### Option D: C plus kustomize bases and overlays per component

The full mono-dees-nuts layout, `components/<name>/base|overlays/`.

- Pros: byte-level parity with the source pattern.
- Cons: 37 `kustomization.yaml` files that list the same manifests the
  directory already contains, and an overlay level with exactly one
  overlay. Deferred until a directory needs a patch or a generator —
  ArgoCD picks the tool per source, so one directory can adopt
  kustomize without the rest.

## Decision Outcome

Chosen: **Option C**, with children as plain directories, the
`platform` AppProject holding everything, and the built-in `default`
project emptied so that an Application without a project fails
validation instead of inheriting wildcard reach.

Children keep the old root's `destination.namespace: default` fallback
and `SkipDryRunOnMissingResource` option, and gain the retry block
(limit 10, 10 s × 2 to 3 m) that a cold boot needs: automated sync
does not retry a failed sync of the same revision without one, and
every directory with a CR whose CRD another child installs would
otherwise wedge until the next commit.

Cross-directory ordering is deliberately *not* expressed: the list
emits all children at once and each retries until its CRDs exist.

The cutover: re-apply the root with `prune: false`, let the children
rewrite the `app.kubernetes.io/instance` label on every resource, verify
nothing is still tracked by the root, then re-enable prune. Took ~30 s
and bounced no pod.

### Consequences

- `1ova` is closed by construction: a stuck `platform-vms` holds
  `platform-vms`.
- SSA, waves and hooks are per directory; `p0ar` becomes "flip SSA on
  `platform-argocd`" if ever wanted — the argocd patches already carry
  the per-resource annotation, so nothing needs it today.
- Removing a directory from `cluster.yaml` **deletes its resources**
  (finalizer). The nested Helm Applications carry no finalizer, so
  removing `longhorn/` orphans the chart's objects rather than
  cascading — still not something to do casually (the Application's
  own header says why).
- The hub's `/status` gitops row watches the root only; a stuck child
  sync is the `9l67` symptom it was built to catch and is now
  invisible to it (`2c2x`).
- Reinstall: the installer Job applies the `platform` AppProject from
  the raw git URL before the root, since the root refers to it.

### Confirmation

Right if a push to one directory lands while another directory is
unhealthy, and if a new workload is a directory plus one line. Wrong
if the generator itself becomes the thing that needs hand-holding, or
if a second cluster shape appears and the per-directory layout has to
grow overlays after all (then Option D, directory by directory).
