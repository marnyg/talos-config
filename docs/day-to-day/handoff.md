# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-05: Longhorn replica policy decided and applied (ADR-0031);
beads on the Mac re-bootstrapped.

- **Longhorn**: both media rebuilds had finished 10-04 evening (`tv`
  18:56Z, `movies` 19:30Z). Then ADR-0031: the library drops to **one
  replica** (`longhorn-bulk` class recreated at `numberOfReplicas: 1`;
  `tv` on bay 1, `movies`+`downloads` on bay 2 — Longhorn does not
  prune extras itself with auto-balance off, so the second replicas
  were deleted by hand). nas1 reboots no longer rebuild the tier;
  `k8sd` closed. Bulk went 1700 G → 1000/700 G scheduled per bay.
- **`files/transfer`** moved to a new **`longhorn-user`** class: two
  replicas, hard node anti-affinity, no tier selector → nas1 bay 2 +
  w1 NVMe. PVC class is immutable, so: commit, delete PVC, ArgoCD
  recreates, copy 3.8 G from the Retained PV (103 s — gigabit
  ceiling), delete old PV + Longhorn volume. Also removed an empty
  orphan PV (`pvc-47a8…`, twin from the 10-04 provisioning race).
- **nas1 moved rooms** (`talosctl shutdown` → power button after
  ~3 min, see below → boot). ADR-0031 confirmed: `Ready` in ~2 min,
  all four volumes `healthy`, **zero rebuilds**; `transfer`'s nas1
  replica was not even marked failed. Share-manager ClusterIPs
  unchanged; sonarr/radarr NFS mounts self-recovered ~3 min after
  the share-managers were back; jellyfin rescheduled to cp1.
- **Shutdown hang** (`m1au`): Talos's drain evicts the share-managers
  and their clients together; Jellyfin was *on nas1*, so its hard NFS
  mount outlived its server and `unmountPodMounts` never returned.
  Data filesystems were already synced+unmounted, so power-off was
  safe. Fix committed: the five RWX consumers carry a nodeAffinity
  away from nas1 (the mirror of the share-manager pin). Verify at
  the next nas1 shutdown, then close `m1au`.
- **Beads on the Mac**: `bd` 1.3.0 after a rebuild; the remote was
  migrated v32→v66 by the NixOS box on 10-04, so this clone was
  re-bootstrapped (old DB at `/tmp/beads-embeddeddolt-v32-backup`,
  export at `/tmp/beads-local-pre-pull.jsonl`; nothing was lost).
  The laptop's signing `git config` is set now (`gpg.format ssh`).

## Loose threads

- `transfer` has an off-node replica: watch the first SMB copy from
  the Windows PC for the synchronous-write ceiling (~105 MB/s).
- `m1au` verification: next `talosctl -n nas1 shutdown` should
  complete on its own now that no NFS client runs on nas1.
- **Mac daemon** (`5q33`): `darwin-rebuild switch` done today? —
  verify `talos-mesh` beats ok; the signing config half is done.
- `jlgz` (v6-only hub answer on cellular) stays open.
- Unchanged: no `fly ssh` key on the nixos box; `dsuj` waits on the
  Windows data copy (now onto a cross-node share).

## Suggested next steps

- Owner's pick from the board: spikes `dsuj` / `ch74` / `9z4e` /
  `kanr` / `i1il`.
- Small: `pymm`/`d4p8` test flakes; `bsj` (backup target) is now
  the only thing standing between `transfer` and a two-node loss.
