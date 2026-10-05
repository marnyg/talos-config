# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-05 (evening): two verifications closed and the Longhorn UI
exposed.

- **Longhorn UI** at `longhorn.gw.mesh.internal`
  (`k8s/apps/longhorn/ingress.yaml`): `longhorn-frontend` has no auth
  of its own and can delete volumes, so it carries the oauth2-proxy
  `auth-url`/`auth-signin` pair like the *arr apps. Was port-forward
  only before. `application.yaml`'s replica-count comment caught up
  with three nodes + ADR-0029/0031 (comments only).
- **`m1au` closed** — the shutdown hang is fixed. `talosctl -n nas1
  reboot` 15:54Z: drain 41 s, kernel back in 2.5 min, `Ready` +3 min,
  share-managers back on nas1, **zero rebuilds** (ADR-0031 holds a
  second time), `transfer` degraded→healthy without a rebuild,
  sonarr/radarr/jellyfin NFS mounts serving at +5 min. No power
  button. The fix is `721caf9` (RWX consumers `nodeAffinity NotIn
  nas1`).
- **`5q33` closed** — Mac `talos-mesh` daemon verified after the
  10-05 `darwin-rebuild switch`: launchd running, built from `7a3b21e`
  (post-`600d2d4` enrollmsg v3), `beat ok` 5 grants/10 names, member
  renewed to 2027-01-02. All three clients are now post-v3. The Mac
  binary predates the 10-04 fakeip RST/stats commits (`3f47eff`,
  Android-motivated); it rides the next `nix flake update talos-config`
  in `~/git/nixos`.

## Loose threads

- "No NFS client on nas1" is enforced only by five per-Deployment
  affinities, not structurally: `files/samba` runs on nas1 (RWO
  `transfer`, fine) — if it or any new pod ever mounts a `longhorn-bulk`
  RWX volume, the `m1au` hang returns. Worth a line in the media
  ingress/pvcs comments or a kyverno-style guard if a fourth RWX
  consumer appears.
- Untracked `auth notes` at the repo root (Oct 1): identities / grants /
  roles / groups as a DAG with a TODO list. Not in beads; overlaps
  spike `i1il`. Decide: attach to `i1il`, own spike, move under
  `docs/`, or delete.
- `transfer` off-node replica: watch the first SMB copy from the
  Windows PC for the synchronous-write ceiling (~105 MB/s).
- `jlgz` (v6-only hub answer on cellular) stays open. No `fly ssh` key
  on the nixos box; `dsuj` waits on the Windows data copy.

## Suggested next steps

- Owner's pick from the board: spikes `dsuj` / `ch74` / `9z4e` /
  `kanr` / `i1il` (the `auth notes` file feeds `i1il`).
- `bsj` (Longhorn backup target) is the only thing between `transfer`
  and a two-node loss, and the prerequisite for a routine cp1 wipe.
- Small: `pymm` / `d4p8` test flakes.
