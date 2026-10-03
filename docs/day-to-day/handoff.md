# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 — **nas1's SATA bays declared; the media library is a
mirror inside nas1; ADR-0028 accepted.**

- **`lug3`**: `u-longhorn-1`/`-2` (TOSHIBA MN10ADA4 4 TB each) as
  `UserVolumeConfig` in `talos/machines/6c-bf-b5-05-51-a8/patch.yaml`,
  selected by **WWID** — the patch's own `disk.serial` recipe was
  unusable, these report no serial. Kubelet extraMounts for both;
  Longhorn label `true` → `config` + `default-disks-config` annotation
  (NVMe `nvme`, bays `bulk`). Hub `a027dd1` deployed + unsealed;
  `apply` reboot-free (dry-run guard exercised, clean); xfs formatted
  and mounted ~9 s after apply, kubelet restarted once, OK.
- Longhorn **ignores the annotation on a node it already has disks
  for** (confirmed live): `bulk-1`/`bulk-2` + the `nvme` tag were
  added to `nodes.longhorn.io/nas1` by hand; the annotation is the
  reinstall path. Noted in the patch file.
- **cnb5's placement half decided (A)**: `longhorn-bulk` recreated
  (`2918135`; SC params are immutable → `kubectl delete sc` + ArgoCD
  hard refresh) as 2 replicas, `diskSelector: bulk`,
  `replicaSoftAntiAffinity: true`, `replicaDiskSoftAntiAffinity:
  false`. The three Volume CRs patched to match; w1 replicas deleted
  after the first nas1 copy was healthy; end state one replica per bay,
  all healthy, ~9 GB actual, no media pod restarted.
- **ADR-0028 → Accepted** (`48c96fd`); invariant 7 and the domain model
  already carried it from `10ebcbc`.
- Spike `r4fw` filed: the hub bakes `talos/` into its image, so every
  `patch.yaml` edit costs build + deploy + wallet re-unseal; resolve
  from git at runtime (ArgoCD-style) instead?

## Loose threads

- **`jx78`**: the default `longhorn` class is not fenced to the NVMe
  tier — a selector-less PVC can now land a replica on a bulk disk.
- `cnb5`'s other half: surface faulted volumes on `/status`.
- `installMAC` is transitional; its exit is `spvd`.
- Agent fixes `7fb6473`/`10ebcbc` reach the fleet only via `9af0`.
- The Mac still runs the pre-`38882a9` daemon; fine (same wire).
- Owner's `todo` (untracked, repo root): migrate media onto the new
  disks (done by the mirror), torrent/seerr/syncthing/sillytavern,
  Windows PC as a compute node.

## Suggested next steps

- `jx78` (NVMe fence) before the next app-state PVC is created.
- `cnb5`: `/status` faulted-volume row, then close.
- `spvd` before any further `systemDiskEncryption` change.
