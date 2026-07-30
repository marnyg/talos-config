# ADR-0008: Media library on a preserved user volume outside EPHEMERAL

- Status: Accepted
- Date: 2026-07-31

## Context and Problem Statement

Every reinstall of cp1 recreates EPHEMERAL, which held `/var/media`:
etcd state rebuilds from git via ArgoCD, but the media library was lost
each time. Reinstalls are meant to be routine (the whole design makes
"blank metal → member" cheap), so the question was: what may a
reinstall legitimately forget? The node has a single 512GB NVMe; there
is no second disk to dedicate.

## Decision Drivers

- Invariant 2 ("git is the single source of truth") governs
  control-plane and derived state; the media library is owner-held
  workload payload that is neither derivable nor worth re-downloading
  wholesale on every reinstall.
- ADR-0004: disk encryption on this cluster is disposal/RMA protection
  only, and its keys are injected at serve time from the unsealed
  master — coupling a data partition to that machinery has a real cost.
- Reinstall must stay cheap and routine (invariant 4 spirit: recovery
  from LAN with owner keys, no exotic steps).
- Single physical disk; EPHEMERAL cannot shrink in place, so any
  repartition is a one-time destructive migration.

## Considered Options

### Option A: Status quo — media on EPHEMERAL

Keep `/var/media` on EPHEMERAL and accept the loss on reinstall.

- Pros: zero work; no partition management.
- Cons: makes reinstalls expensive in practice (re-download hundreds of
  GiB), which erodes the "reinstalls are routine" property.

### Option B: Separate physical data disk

Move media to a second disk that installs never touch.

- Pros: cleanest isolation; survives even a whole-disk reset.
- Cons: the minipc has one NVMe slot; hardware purchase for a problem
  solvable in the partition table.

### Option C: User volume on the system disk, unencrypted (chosen)

Cap EPHEMERAL at 160GiB (`VolumeConfig`, `grow: false`) and declare a
300GiB xfs `UserVolumeConfig` named `media` (`/var/mnt/media`, label
`u-media`), both in the machine patch in git. Leave it unencrypted.

- Pros: fully declarative; Talos re-adopts the partition by label
  across reinstalls; label-scoped reset (`--system-labels-to-wipe
  STATE,EPHEMERAL`) preserves it while keeping the bootloader, so
  recovery needs no USB medium; no serve-time key coupling.
- Cons: a plain `talosctl reset` (whole-disk) destroys the library;
  the 160/300 split is fixed until the next destructive repartition.

### Option D: As C, but LUKS-encrypted

Same layout with the encryption block, keys injected at serve time
like STATE/EPHEMERAL.

- Pros: uniform encryption posture across all partitions.
- Cons: buys nothing real — ADR-0004 already concedes encryption here
  is disposal protection only, and the content is re-downloadable
  public media; couples data-partition availability to hub-derived
  keys for no benefit.

## Decision Outcome

Chosen: **Option C**, because it answers "what may a reinstall
legitimately forget" precisely — everything derives from git except
the one declared payload volume — at the cost of a single one-time
repartition (performed 2026-07-31) and a sharper footgun on plain
`reset`.

Invariant 2 was amended the same day to record the boundary: workload
payload is excepted from "recomputable from git", and `u-media` is the
one deliberate instance.

### Consequences

- Reinstalls become genuinely routine: label-scoped reset + insecure
  apply + bootstrap, media intact.
- The standard reinstall command changes; a plain `talosctl reset`
  now destroys the media library and needs USB/PXE to recover
  (documented in `technical/guides/deployment.md`).
- Media PVs hostPath into `/var/mnt/media/*`; the pre-migration
  library restarted empty.
- The 160GiB EPHEMERAL cap bounds etcd/images/logs; revisit only via
  another destructive repartition.

### Confirmation

Right when a future reinstall re-adopts `u-media` with the library
intact (mechanism verified 2026-07-31, not yet exercised with real
data). Invalidated if EPHEMERAL's 160GiB proves too small for the
workload set, or if media ever stops being re-downloadable commodity
content (encryption calculus changes).
