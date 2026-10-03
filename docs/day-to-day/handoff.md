# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04: **`cnb5` closed — the data plane has an eye on `/status`,
and ADR-0029 is Accepted.**

- **`/status` gained a `storage` row** (`7a4ee09`,
  `config-server/storage.go`): the gitops poll issues a second `GET`
  on the same kube-api client, Longhorn's Volume list, and the row
  names any volume whose `robustness` is `faulted` or `degraded`, or
  that cannot schedule a replica, by PVC (`media/tv`). Calm reads
  `13 volumes healthy (1 detached)`. Why: the library's three volumes
  sat `faulted` for 12 days while pods read `Running` and ArgoCD
  `Healthy`. ADR-0027 carries the dated note. Deployed to the hub
  (`fly/deploy.sh` ran plain on the nixos box), unsealed, row
  confirmed live.
- **ADR-0029 Proposed → Accepted.** Verified live: `media/{tv,movies,
  downloads}` each have two `running` replicas on nas1, one per SATA
  bay (`/var/mnt/longhorn-{1,2}`); `longhorn-bulk` has
  `replicaDiskSoftAntiAffinity: false` and the share-manager pinned
  to nas1. `jx78` (default class fenced to `nvme`) had landed in
  `4f596bb` the session before.

## Loose threads

- **`vzbf`** (gateway WebSocket-after-Close panic) still
  `in_progress`: fix rolled 2026-10-03 as image `21badd6`, soak window
  wants a day of SignalR traffic with 0 restarts before closing.
- The `storage` row logs only on a warn *change*; a healthy first
  poll is silent in `fly logs`. Expect `degraded` during a disk
  rebuild — that is the row working, not a fault.
- Old docker host: media containers stopped, not removed; its DBs are
  behind the cluster's (do not re-import). Owner's `todo` file at the
  repo root (untracked) lists seerr, syncthing, sillytavern, a Windows
  compute node, docker-host cleanup, TLS on VPN-exposed services —
  not yet in beads.
- `gitopsWatcher` now owns two snapshots; rename to `clusterWatcher`
  if a third read ever arrives, not before.

## Suggested next steps

- Close `vzbf` once `kubectl -n gateway get pods` shows 0 restarts
  over a day.
- File the `todo` items as beads (`idea`/`task`) and delete the file;
  the owner's app list (seerr, syncthing, sillytavern) is the natural
  next storage-tier consumer.
- `bsj` (Longhorn backup target) if the library ever stops being
  "re-downloadable" — ADR-0029's invalidation trigger.
