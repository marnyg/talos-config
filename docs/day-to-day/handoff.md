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

- Live-confirmed by the owner 2026-10-06 (sonarr/longhorn no prompt,
  ArgoCD by token, Jellyfin wallet page, pod forgery 401). The
  `media`-device → 403 case is unit-tested only (no `media` device
  uses an ingress). `kubectl -n sso logs deploy/siwe-oidc` shows
  `authz:` lines on refusals.
- A re-keyed gateway (new volume) is a new `ed:` id and a new
  `-gateway` line — the pod logs `identity token issuer ed:…` at start.
  Until the pin is updated, every gated app 401s (closed, not open).
- ADR-0033 (device-local TLS) stays Proposed until the HTTPS direction
  is first exercised.
- Spike `7ymy` (person binding on the member cert) is the filed
  long-term answer to the Jellyfin exception; `a0ys` closed after the
  owner's live test.
- Standing: `95la` (appliance Jellyfin login), herdr 0.9.1 vs server
  0.8.2 (restart kills panes), `jlgz`, `bsj`.

## Suggested next steps

- `95la`: per-device Jellyfin accounts vs Quick Connect for the TV.
- kagent 0.10.3 trial (`docs/spikes/agents-kagent/` → `k8s/apps/kagent`).
