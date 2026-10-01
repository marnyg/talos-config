# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-01 — **HA sweep (`9l67`) closed by ruling, no code.**

- **Decision `nfmt`**: the in-cluster gateway stays a stateful
  member — key + Kit on the `gateway-state` RWO PVC, invariant 2's
  `359.9.3` sentence unchanged. A ~30–60 s `*.gw.mesh.internal`
  outage on node loss is accepted. The ephemeral-key gateway (slice
  3) is dropped: what made the stateful gateway painful was `5hek`
  (3.5 d runway, fixed to 90 d) and Longhorn never releasing the
  volume (`nodeDownPodDeletionPolicy=delete-both`, slice 1) — both
  gone.
- The sweep's last item split out as `jko0` and **closed the same
  session**: the gateway's real failover was 5–6 min, not 30–60 s —
  Longhorn `delete-both` only force-deletes *Terminating* pods, so
  the 300 s default toleration ruled. Gateway now has 30 s
  `unreachable`/`not-ready` tolerations (`9e6c2ad`, live, same key).
  Everything else control-ish sits on cp1, the sole control plane,
  so there is no node-loss story for it (notes 2026-10-01).
- Broken windows swept: dead kubevirt pods deleted; the cp1-stacked
  multi-replica pods (coredns, longhorn csi-\*, longhorn-ui) now spread
  across cp1/nas1 — a rolling restart alone does **not** spread
  `preferred` anti-affinity (new pods avoid the *old* ones and land
  together on the other node); delete one pod afterwards. KubeVirt's
  operator reverts restarts of its Deployments; left on cp1.
  `controller-patch.yaml` comment brought up to date (`3341a8d`).
- **Small ops** (`cbd992e`, hub deployed on it + unsealed): `c4vd`
  done — w1's directory is now `talos/machines/0c-37-96-5d-26-c4` (the
  dongle's MAC); hub verified composing for it, old MAC 404. `etzl`
  done — SA-issuer recreate runbook in `guides/gotchas.md`. `t7b2`
  **deferred** to cp1's next reinstall: it cannot go live (`apply`
  pushes to the running node; Longhorn single-replica volumes are bound
  to `talos-wu6-eib`); exact pre-wipe steps in `guides/reinstall.md`.

Slices 1–2 (2026-09-29/30) remain as landed: dead node no longer
freezes GitOps or pins RWO volumes; ingress-nginx + oauth2-proxy 2×
anti-affine with `maxUnavailable: 1`; siwe-oidc failover-only.

## Loose threads

- **w1 still off**, out-of-service taint, kit expired — untaint +
  `nix run .#apply -- 0c-37-96-5d-26-c4` when it returns (notes
  2026-09-29; the directory was renamed, its installed boot token names
  the old MAC, so the re-serve is required, not optional). HA is
  proven only across cp1 + nas1.
- Gateway pod moved cp1 → nas1 on 2026-09-30 morning unexplained —
  not investigated.
- `4ze8` (siwe-oidc replication), `5q33` (phone/TV APK, Mac daemon
  pre-P4.2), `d4p8` (test flake).

## Suggested next steps

- Fill nas1's four SATA bays (`UserVolumeConfig` per disk) — blocked
  on the disks arriving.
- Protocol v0 M5 money (`0bc.5`) or lighthouse discovery.
- `guides/reinstall.md` "Where this is not yet cheap" section is stale
  (Longhorn has been deployed since 2026-09-22; the etcd-backup gap is
  still real) — rewrite before the next wipe.
