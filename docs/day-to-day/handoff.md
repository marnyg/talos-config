# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-06 (second session): the app seam built — `5kh` then `a0ys`,
ADR-0032 Accepted.

- **`5kh` + `zfy`** (`9d3b1bb`): the bridge mints `groups` from the
  per-wallet `-admin=0xaddr=user:group[,group]` map (mandatory,
  validated against `policy.DeviceGroup`), not a literal. The siwe-oidc
  image had been unbuildable since 09-19 (`../protocol` replace outside
  the docker context); `Dockerfile.siweoidc` now mirrors the repo
  layout and the `.dockerignore` allowlist is trimmed. First green
  `siwe-oidc-image` run since.
- **`a0ys`** (`31f4f24` code, this commit manifests): new
  `config-server/meshtoken` (EdDSA JWT, closed issuer set, no JWT lib);
  `gateway.Proxy` signs `X-Mesh-Token` per request (`aud`=Host, 60 s);
  the bridge's `/authz?group=<g>` is the group gate for nginx
  `auth_request` (200/403/401, no cookie, no `auth-signin`), and
  `/authorize` logs a device in by token for clients opted in with
  `-token-client` (ArgoCD; Jellyfin stays on the wallet). Seven
  Ingresses flipped (sonarr radarr nzbget transmission jackett
  sillytavern longhorn); `k8s/apps/oauth2-proxy/` deleted. Gateway id
  `ed:45fc82fc…6613f4` pinned in `k8s/apps/siwe-oidc/deployment.yaml`;
  gateway image `31f4f24` pinned.
- Two rulings recorded in ADR-0032 ("Rulings at build time"): no person
  fallback on the group gate; device login is per-client opt-in and
  mints the *device* (`sub` = `ed:…`), never a mapped person.

## Loose threads

- **Live confirmation still owed** (ADR-0032 "Confirmation"): after
  ArgoCD syncs, open `sonarr.gw` from an admin device (no prompt), from
  the TV's `media` identity (403), ArgoCD without a wallet prompt, and
  Jellyfin still with one. `kubectl -n sso logs deploy/siwe-oidc` shows
  `authz:` lines on refusals and `gateway(s) pinned` at start.
- A re-keyed gateway (new volume) is a new `ed:` id and a new
  `-gateway` line — the pod logs `identity token issuer ed:…` at start.
  Until the pin is updated, every gated app 401s (closed, not open).
- ADR-0033 (device-local TLS) stays Proposed until the HTTPS direction
  is first exercised.
- The exploration-log bullet "bare trusted identity header" is now
  resolved by ADR-0032 — delete it next docs pass (owner's call).
- Standing: `95la` (appliance Jellyfin login), herdr 0.9.1 vs server
  0.8.2 (restart kills panes), `jlgz`, `bsj`.

## Suggested next steps

- Do the live confirmation above; then `bd close talos-config-a0ys`.
- `95la`: per-device Jellyfin accounts vs Quick Connect for the TV.
- kagent 0.10.3 trial (`docs/spikes/agents-kagent/` → `k8s/apps/kagent`).
