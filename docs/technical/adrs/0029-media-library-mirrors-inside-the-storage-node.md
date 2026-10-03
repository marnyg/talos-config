# ADR-0029: The media library mirrors inside the storage node

- Status: Proposed
- Date: 2026-10-04
- Amends: ADR-0011 (the `longhorn-bulk` class: replica count and placement)

## Context and Problem Statement

ADR-0011 put the media library on `longhorn-bulk`: RWX, **one** replica,
placed wherever Longhorn liked. The library was declared disposable
(re-downloadable), and doubling it on a 1073 GB pool would not fit. In
practice that one replica landed on w1. When w1 went off on 2026-09-21,
all three media volumes faulted for 12 days and nobody noticed (`cnb5`).

On 2026-10-04 nas1 gained two 4 TB SATA bays (`lug3`). Two things now
had to be decided: where the library lives, and how many copies it gets.
The owner's own phrasing was "raid the two disks". The same session
found a third, unplanned question. The kube-scheduler had put the
library's NFS servers (Longhorn share-manager pods) on w1. Every byte
crossed w1's USB gigabit dongle twice, and so did every Jellyfin stream.

## Decision Drivers

- The library is no longer an afterthought. The owner moved 466 GB onto
  it and retired the old docker host. It is still re-downloadable
  (ADR-0011, invariant 2's corollary), but losing a disk should not
  mean a re-download.
- Talos has no mdraid for user volumes. A user volume is one disk or
  one partition, so "raid" has to happen at the Longhorn layer.
- The library does not fit the NVMe tier: cp1 has 322 GB, w1 751 GB
  and nas1 911 GB, all shared with app state.
- The library is offline whenever the node holding it is offline. That
  was already true on w1. Spreading the library across nodes is only
  possible on the NVMe tier, and the library does not fit there.
- The NFS hop is the accepted cost of RWX (ADR-0011). Putting that hop
  on a different node than the data is not an accepted cost.

## Considered Options

### Option A: Two replicas, both on nas1's bulk disks, never on the same disk

`numberOfReplicas: 2`, `diskSelector: bulk` (the SATA bays are tagged
`bulk`, the NVMe disks `nvme`), `replicaSoftAntiAffinity: true` (both
replicas may share a node), `replicaDiskSoftAntiAffinity: false` (they
must be on different disks).

- Pros: survives one disk failing, which is RAID-1 without mdraid. 4 TB
  usable. The library sits on the spinning tier, not on NVMe that app
  state needs.
- Cons: nas1 is a single point of availability, as w1 was. Half the raw
  capacity goes to the second copy.

### Option B: One replica on the bulk tier

- Pros: 8 TB usable.
- Cons: same zero redundancy as before, just on a different node. One
  dead Toshiba means a full re-download.

### Option C: Two replicas spread across nodes

- Pros: survives a whole node going down.
- Cons: only nas1 has bulk disks, so the second copy would have to live
  on cp1's or w1's NVMe. That caps the library at their free NVMe space
  and fills the app-state tier with media.

### Option D: mdraid / LVM mirror under one user volume

- Cons: Talos user volumes cannot express it, so it would be state
  outside git, against invariant 2.

## Decision Outcome

Chosen: **Option A**, with **the share-manager pinned to the same
node** (`shareManagerNodeSelector: kubernetes.io/hostname:nas1`).

The pin costs no availability. With both replicas on nas1, nas1 being
away means the library is away whatever node the NFS server runs on.
Measured during the import: ~30 MB/s with the share-managers on w1,
~105 MB/s (gigabit line rate) with them on nas1.

### Consequences

- StorageClass parameters are immutable. Changing the class means
  deleting it so ArgoCD recreates it. Delete it only once the new
  commit is ArgoCD's target revision; otherwise self-heal puts the old
  parameters back first.
- Existing volumes do not follow a class change. Their Volume CRs
  (`numberOfReplicas`, `diskSelector`, the two anti-affinities) must be
  patched, and off-tier replicas evicted.
- A share-manager moves only when its pod is recreated. With
  `rwx-volume-fast-failover` off, that also restarts the workload pods
  that mount the volume.
- The default `longhorn` class has no `diskSelector`, so an app-state
  PVC can now land on spinning disk. Follow-up `jx78` fences it to
  `nvme`.
- Bays 3 and 4 join the same tier using the recipe in nas1's
  `patch.yaml`, with no class change.

### Confirmation

Right if: one Toshiba failing leaves the library online with a degraded
volume that rebuilds onto its replacement, and Jellyfin over the NFS hop
streams without stalls. Invalidated if the owner starts treating the
library as irreplaceable (then it needs a backup target, `bsj`, not
more replicas), or if a second storage node arrives (then Option C on
the bulk tier is possible and better).
