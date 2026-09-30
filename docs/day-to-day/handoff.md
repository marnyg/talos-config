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
- The sweep's last unticked item, "which control-loop pods must
  survive a node", split out as `jko0` (longhorn manager/CSI,
  cert-manager, external-dns, sealed-secrets, kms, siwe-oidc).

Slices 1–2 (2026-09-29/30) remain as landed: dead node no longer
freezes GitOps or pins RWO volumes; ingress-nginx + oauth2-proxy 2×
anti-affine with `maxUnavailable: 1`; siwe-oidc failover-only.

## Loose threads

- **w1 still off**, out-of-service taint, kit expired — untaint +
  re-serve config when it returns (notes 2026-09-29). HA is proven
  only across cp1 + nas1.
- Gateway pod moved cp1 → nas1 on 2026-09-30 morning unexplained —
  not investigated.
- `4ze8` (siwe-oidc replication), `5q33` (phone/TV APK, Mac daemon
  pre-P4.2), `d4p8` (test flake).

## Suggested next steps

- Fill nas1's four SATA bays (`UserVolumeConfig` per disk).
- `jko0` control-loop survivability pass (cheap: mostly tolerations
  and a replica count or two).
- Small ops: `t7b2`, `c4vd`, `etzl`.
