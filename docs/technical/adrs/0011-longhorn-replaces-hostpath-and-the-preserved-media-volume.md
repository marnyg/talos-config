# ADR-0011: Longhorn replaces hostPath and the preserved media volume

- Status: Accepted
- Date: 2026-07-31
- Supersedes: ADR-0008

## Context and Problem Statement

ADR-0008 solved a single-node problem: with one machine and one disk,
the media library had to survive reinstalls, so it went onto a
label-preserved `u-media` user volume and the media PVs became
`hostPath` mounts into it. The cluster stopped being single-node on
2026-07-31 when w1 joined, with more nodes expected within days.

Two things broke immediately. First, `hostPath` PVs carry no
`nodeAffinity`, so any media pod rescheduled onto a node other than cp1
silently gets an empty library via `DirectoryOrCreate` — a correctness
hazard that only stayed harmless because the library was empty. Second,
ADR-0008's preservation trick protects the media library, which is
re-downloadable, while the state that actually hurts to lose —
sonarr/radarr/jellyfin metadata — still lives on EPHEMERAL and dies with
every reinstall. The valuable data was the unprotected data.

## Decision Drivers

- Multi-node scheduling must not depend on which node a pod lands on.
  Node pinning is a workaround; volume mobility is the property.
- What deserves durability is app state (small, irreplaceable), not the
  media library (large, re-downloadable). ADR-0008 optimised the
  opposite way.
- Invariant 2's carve-out currently names the `u-media` volume as the
  one deliberate instance of state outside git. Any replacement must
  either fit that carve-out or amend it.
- Invariant 4 as scoped 2026-07-31: cluster membership may depend on the
  mesh. Storage replication traffic riding the mesh is therefore
  acceptable; it is not on the provisioning path.
- Reinstalls must stay cheap. Replication that makes a node wipe
  expensive would trade one problem for another.
- Sidero recommends a disk dedicated to storage, separate from the
  install disk — on single-disk machines, a partition is the closest
  approximation.

## Considered Options

### Option A: Keep hostPath, add nodeAffinity to the PVs

Pin the three media PVs to cp1 with `nodeAffinity`.

- Pros: two-line fix, no new components, no new state.
- Cons: pins the whole media stack to one node forever; app state still
  dies with EPHEMERAL; no backups; new nodes cannot host media
  workloads at all. Solves the hazard, not the problem.

### Option B: Longhorn (chosen)

Replicated block storage via CSI. A dedicated user volume per node backs
Longhorn (`/var/mnt/longhorn`, 700GiB on w1); `dataLocality:
best-effort` keeps a replica on whichever node runs the pod; backup
targets (S3/NFS) plus recurring jobs provide actual backups.

- Pros: volumes follow pods, so the scheduling hazard disappears rather
  than being pinned around; app state becomes durable and replicated;
  real backups, which replication alone is not; Talos-verified;
  cp1 needs neither reinstall nor repartition — its existing `u-media`
  partition is wiped and handed over as a Longhorn disk.
- Cons: adds a stateful control plane (volume/replica/snapshot CRDs) not
  recomputable from git; requires a new factory schematic
  (iscsi-tools + util-linux-tools) and an upgrade of every node;
  Longhorn's own minimum recommendation is 3 nodes; replicating bulk
  media costs real capacity.

### Option C: Piraeus / LINSTOR (DRBD)

DRBD-backed replicated block storage.

- Pros: two-node replication is DRBD's classic case, matching the
  cluster's size better than Longhorn's 3-node recommendation.
- Cons: lower-level, smaller community, no comparable built-in backup
  story; needs the DRBD system extension. Node count stops being the
  binding constraint as soon as the extra nodes land.

### Option D: Rook/Ceph or OpenEBS Mayastor

- Cons: Ceph wants 3 mons for quorum and Sidero warns it is slow and
  resource-hungry on small clusters; Mayastor needs hugepages, a
  dedicated core and NVMe. Both are heavier than the problem.

### Option E: Keep hostPath, add a backup tool (k8up/restic, Velero)

- Pros: delivers backups — the actual stated want — with no storage
  cluster and no new schematic.
- Cons: no volume mobility, so the scheduling hazard needs Option A
  anyway; no HA; restore is a manual event rather than a property.

## Decision Outcome

Chosen: **Option B (Longhorn)**, because it is the only option where the
scheduling hazard disappears as a *consequence of the design* rather
than being pinned around, and because it moves durability onto the data
that actually deserves it. Media is explicitly demoted: it becomes an
ordinary replicated volume, and its owner has accepted losing the
current library contents to get there.

### Consequences

- **ADR-0008 is superseded.** The `u-media` preservation trick, the
  label-scoped reset that protected it, and the "what may a reinstall
  forget" framing all go away: with replicated volumes, a node wipe
  costs a replica rebuild, not a library.
- **Invariant 2's carve-out was replaced, not extended** (amended with
  this ADR's acceptance). The old wording named the `u-media` volume as
  a per-disk exception. The successor draws a *layer* line instead: git
  owns the control plane (identity, membership, certs, addresses,
  machine config); the data plane owns its payload **and the
  bookkeeping a stateful service keeps about that payload** — Longhorn's
  volume/replica/snapshot CRDs. That bookkeeping shares the fate of the
  payload it describes, so its durability is a
  replication-and-backup problem rather than a git problem. Net effect:
  the invariant got *stronger* per node — no single disk is exempt from
  a wipe — while gaining an honest data-plane scope. Two riders:
  "if a slice seems to need a database, redesign it" is now bounded to
  slices of *this* design (Longhorn is a database of replicas, and
  adopting it would otherwise violate the sentence literally); and with
  no preserved volume anywhere, **a backup target stops being optional**
  — it is the only remaining durability story for app state.
- **`guides/reinstall.md` is largely obsolete** and must be rewritten;
  its central warning ("never run a bare `talosctl reset`") exists to
  protect a volume that will no longer hold anything unique.
- Every node needs the new schematic before it can run Longhorn, so
  node onboarding and this migration are coupled (`ca77f427`,
  `214661d2`).
- Encryption posture becomes an open question: ADR-0004 clears cp1's
  media volume as public and re-downloadable, which will no longer
  describe the contents of a Longhorn disk (thread `8e46f3a5`).
- Capacity accounting becomes replica-aware — 300GiB of media at two
  replicas is 600GiB of disk.

### Adoption note (2026-07-31)

Implemented the same day it was accepted. One thing the ADR did not
anticipate: the media volumes are **shared** — `tv` by sonarr+jellyfin,
`downloads` by four pods — which hostPath only permitted because every
pod sat on cp1. A Longhorn RWO volume attaches to a single node, so a
naive migration would have re-pinned all six pods and delivered none of
the mobility this decision was chosen for. Resolved by splitting the
StorageClasses **by data class rather than by app**: default `longhorn`
(RWO, 2 replicas) for app state, `longhorn-bulk` (RWX, 1 replica,
Retain) for the library. That also applies this ADR's own thesis to
capacity — the library is disposable, so it does not earn a 2x
multiplier on a 1073GB pool.

The hazard in bug `0b374653` turned out to be **wrong in our favour**: a
media pod on a node without the volume does not silently get an empty
library, because `/var/mnt` is read-only on Talos unless a user volume
mounts there. It fails loudly (`mkdir /var/mnt/media: read-only file
system`) — an outage, not silent data divergence. Observed live when
cp1's reboot rescheduled the media stack onto w1.

### Confirmation

The decision is right if, after adoption: (a) a media pod can be deleted
and rescheduled onto any node and come back with its data intact —
tested deliberately, not observed by luck; (b) a full `talosctl reset`
of a worker costs only a replica rebuild, with no manual data step; and
(c) sonarr/radarr/jellyfin metadata survives a node wipe, which is the
thing ADR-0008 never delivered.

It is wrong — revisit toward Option C or E — if Longhorn's overhead on
these machines proves disproportionate (instance-manager CPU/memory
reservations crowding out workloads), or if replica rebuild traffic over
the mesh degrades the LAN path enough to matter.
