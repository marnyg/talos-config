# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-06 (third session): `95la` ruled, built and **confirmed on the
TV** — ADR-0034 Accepted.

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
- Live-verified both ways: laptop (`admins`) → left to manual, as
  intended; the Shield (`tv`, `media`), driven over network adb —
  server switched to `http://jellyfin.gw.mesh.internal` (port 80),
  Quick Connect pressed, signed in as user `tv` (non-admin, hidden)
  within one poll, Big Buck Bunny played over `gw/ingress-http`. One
  fix found by the TV and shipped (`b2fc15c`): the app sends
  `Accept-Encoding: gzip`, so the hook saw compressed bytes
  ("unreadable result"); the Initiate now goes upstream without it.

## Loose threads

- **Owner decisions:** close `95la`; retire the raw `jellyfin` facet
  (`:8096` splice — policy row `{facet: jellyfin, group: media}`,
  gateway `-jellyfin=` flag, `policy.facets`, glossary) now that the
  TV is on the HTTP door (ADR-0034 ruling 5)?
- The TV's old saved server entry (`…:8096`, sessions `mar`/`admin`)
  is still in the app's server list; harmless, but the `admin`
  device token it holds is still valid — revoke under Dashboard →
  Devices, or delete the entry on the TV.
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
