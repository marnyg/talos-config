# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-06 (third session): `95la` ruled and built — ADR-0034
Proposed, live, awaiting the TV.

- **`95la`** (`9cadfd2` code, `e5cd23c` manifests): the appliance
  logs into Jellyfin by Quick Connect, approved automatically from its
  mesh identity. `config-server/jellyfinqc` is a reverse-proxy sidecar
  in the Jellyfin pod and the new backend of the `jellyfin.gw` Ingress
  (`:8097`); it verifies `X-Mesh-Token` like the bridge (same pinned
  gateway id, now in two manifests) and on `POST /QuickConnect/Initiate`
  from a `media`-group device ensures a non-admin Jellyfin user named
  after the device and calls `Authorize?code=…&userId=…` before the
  response returns. Never onto an admin user; `admins` devices, no
  token, forged/stale token ⇒ proxied and left to manual approval. It
  also keeps `QuickConnectAvailable` on (was a hand-made setting).
- `Dockerfile.siweoidc` → `Dockerfile.cmd` (`ARG CMD`), shared by the
  bridge and the sidecar; new `jellyfinqc-image` workflow; both images
  green on first run; ghcr packages public.
- Live-verified from the laptop over the mesh: full chain
  gateway → nginx → sidecar, token verified as `marius-mac [admins]`,
  left to manual approval (the intended outcome for an owner device).
  **The `media` path is untested until the TV tries it.**

## Loose threads

- **TV test pending** (owner): on the TV's Jellyfin app, point it at
  `http://jellyfin.gw.mesh.internal` (port 80 — the ingress door, not
  `:8096`), choose Quick Connect; expect the code to flash and the app
  to sign in as the TV's device name. Then `kubectl -n media logs
  deploy/jellyfin -c qc` shows `logged in as jellyfin user "<name>"`,
  and `/Users` has the new hidden user. On success: ADR-0034 →
  Accepted, close `95la`, and decide whether the raw `jellyfin` facet
  (`:8096` splice, policy row `{facet: jellyfin, group: media}`) is
  retired (ADR-0034 ruling 5).
- The TV's existing session is as `admin` (Quick-Connected from the
  cluster 09-20). Sign it out so it re-enters as itself; consider
  revoking `admin`'s stray device sessions (`/Sessions`, or the
  dashboard's Devices page).
- A re-keyed gateway is now **two** pin lines: `k8s/apps/siwe-oidc`
  and `k8s/apps/jellyfin` (`-gateway=`). Sidecar logs
  `token refused: issuer not pinned` when the pin is stale.
- `jellyfinqc` image is `:latest` + `Always` like the bridge; the
  gateway is digest-pinned. Pin once Accepted, or decide that
  `:latest` is the house rule for the C-free in-cluster binaries.
- Standing: herdr 0.9.1 vs server 0.8.2 (restart kills panes),
  `jlgz`, `bsj`; two `<!-- stale? -->` flags in `notes.md`
  (Quint entries ~L167/L401) still await the owner.

## Suggested next steps

- TV test → ADR-0034 Accepted → facet retirement decision (above).
- `7ymy` now has a landing place for Jellyfin: the sidecar approves as
  the person's user when the cert carries a binding (ADR-0034,
  Consequences). Still protocol-level work first (cert field, approval
  UI, token claim).
- kagent 0.10.3 trial (`docs/spikes/agents-kagent/` → `k8s/apps/kagent`).
