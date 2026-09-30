# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-30 — **HA sweep slice 2 (`9l67`) done: the ingress path
survives a node loss.** `9171c87`, `56e193d`, live and verified.

- **ingress-nginx** and **oauth2-proxy** run 2 replicas with required
  hostname anti-affinity + PDB `minAvailable: 1` (chart-generated for
  nginx, explicit in `k8s/apps/oauth2-proxy/deployment.yaml`). Both
  are stateless across replicas — oauth2-proxy's session and PKCE
  verifier ride cookies under the sealed cookie secret. Live: one
  replica each on cp1 and nas1.
- **siwe-oidc stays at 1 replica on purpose**: its RS256 key and
  auth-code/token maps are per-pod, so a second replica fails
  redemption at random. It gets 30 s `unreachable`/`not-ready`
  tolerations instead (failover ≈ 1 min, cost = one re-login). The
  redesign (durable signing key vs. invariant 1, self-contained codes)
  is thread `4ze8`.
- **Found the hard way**: required anti-affinity with replicas ==
  schedulable nodes deadlocks the default rolling update (surge pod
  has no node; `25%` maxUnavailable rounds to 0). `maxUnavailable: 1`
  is now set on both. The wedge also stalled ArgoCD's wave-0 health
  gate; the working unstick is in notes 2026-09-30.

## Loose threads

- **w1 still off**, still carries the out-of-service taint, kit
  expired — untaint + re-serve config when it returns (notes
  2026-09-29). Everything HA-wise is currently proven only across
  cp1 + nas1.
- Gateway pod was ~20 min old on nas1 at session start (moved from
  cp1 since 2026-09-30 morning) — not investigated.
- `4ze8` (siwe-oidc replication), `5q33` (phone/TV APK, Mac daemon
  pre-P4.2), `d4p8` (test flake).

## Suggested next steps

- Slice 3 of `9l67` (gateway ephemeral key, no PVC) — needs an
  invariant-2 ruling first (`359.9.3` placed the gateway's key on its
  own volume; `5hek` makes the stateful gateway cheaper to keep).
- Or leave the sweep here and fill nas1's SATA bays.
- Small ops: `t7b2`, `c4vd`, `etzl`.
