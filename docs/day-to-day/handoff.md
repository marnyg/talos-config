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
- **Media library moved onto the cluster**: 466 GB tv+movies from the
  old docker host via `scripts/media-import.yaml` (rsync daemon,
  LAN NodePort locked to this box, deleted after); PVCs grown to
  900/400/200Gi online. Sonarr's 29 series re-added through its API
  from an export (`~/disks/1TB-old/server/sonarr-series-2026-10-04.json`),
  246/246 files matched; Jellyfin rescanned. Downloads and every other
  app config deliberately not migrated (owner: expendable). Old docker
  containers stopped, not removed.
- **share-managers pinned to nas1** (`6119ccb`): the scheduler had put
  all three NFS servers on w1 → ~30 MB/s; pinned → ~105 MB/s.
  Recreating the class raced ArgoCD's self-heal once (old params put
  back, then 'parameters forbidden'): delete it *after* the new commit
  is the target revision.
- **Gateway crash-loop** `vzbf` (P1, found today): a proxied WebSocket
  panics in `iroh-transport` `Raw.Read` after Close.
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
- Owner's `todo` (untracked, repo root): raid + media migration done;
  left: torrent/seerr/syncthing/sillytavern, Windows PC as a compute
  node. The old box's docker media stack can be removed once the owner
  is happy with the cluster's.

## Suggested next steps

- `vzbf`: the gateway panic (every `*.gw.mesh.internal` flaps meanwhile).
- `jx78` (NVMe fence) before the next app-state PVC is created.
- `cnb5`: `/status` faulted-volume row, then close.
- `spvd` before any further `systemDiskEncryption` change.
