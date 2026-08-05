# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-05 (sixth session) — loose-end fixes, and **w1 was found down**.

- **w1 has been unreachable since 2026-08-04 ~09:26** (no LAN ping,
  no apid; replacement pods on cp1 are 27h old). All three
  `longhorn-bulk` volumes are `faulted` — their single replica is on
  w1 — so the media library is offline: decision `d5f73e89`'s accepted
  risk, realized. Data is presumed intact on the disk. Needs a
  **physical power/console check**; nothing software-side can help.
- **`f9bac57c` fixed** (`fed04b4`): argocd-server's issuer pin moved
  out of the bootstrap Job into `k8s/apps/argocd/server-patch.yaml` —
  an SSA partial Deployment (`ServerSideApply=true,Validate=false`)
  ArgoCD reconciles — and points at the siwe-oidc ClusterIP, same as
  oauth2-proxy. Live-patched too; verified via `/etc/hosts` in the pod
  and 200 from the UI. The stuck `argocd-application-controller-0`
  (StatefulSet pod on the dead node) was force-deleted to resume sync.
- **`73cb3bf2` fixed** (same commit): installer Job pins ArgoCD
  `v3.4.5` instead of `stable`.
- **`6b72e112` resolved as sealed secret** (`f1f5dd4`): jellyfin's
  local admin password is now random, sealed as `jellyfin-admin`
  (retrieval/rotation documented in the manifest); the configurator
  reads it from env and fails loud if unset. While in the file: a
  **third instance of the pod-dials-mesh-IP class** was found and fixed
  in jellyfin's own hostAliases.
- Devshell now exports `KUBECONFIG` (`f79e993`).

## Loose threads

- **Everything storage-shaped waits on w1.** The new jellyfin pod sits
  `ContainerCreating` on the faulted RWX volumes; the old pod still
  runs — **with admin/admin — until it cycles** after w1 returns.
- ArgoCD SSO verified to alias + 200 only; the full wallet login flow
  has not been browser-tested since the fix.
- The emptyDir→Longhorn migration remains the last unclaimed piece of
  ADR-0011 (unchanged from last session).
- etcd/DHCP invariant 7 (`6c456522`) and the backup target
  (`8b9972fd`) untouched.

## Suggested next steps

- Power-cycle w1. Then verify: volumes leave `faulted`, the new
  jellyfin pod starts (new admin password takes effect — retrieve it
  for the TV), ArgoCD wallet login works in a browser.
- Then the emptyDir→Longhorn migration, checking each app's
  configurator is idempotent against pre-existing config.
