# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session
2026-10-21: **Actual Budget** (`7yzv`, `194eefc`) at
`budget.gw.mesh.internal` — `k8s/apps/actual`, the Vaultwarden
posture copied: one 2Gi Longhorn volume (`server-files/` +
`user-files/`), `26.10.0-alpine` by digest run as uid 1000 (the image
ships no USER) with a read-only root fs, admins gate on the Ingress,
Actual's own server password as the second gate, https name because
the web app needs a secure context (ADR-0033). Gatus row + Homepage
tile. Verified live: `/health` UP through the gateway,
owner set the server password the same day; `7yzv` closed.

Same session: **the admins gate became ingress-nginx's
`global-auth-url`** (`9235085`, `332b463`) — every Ingress is gated
unless it opts out with `enable-global-auth: "false"` (argocd,
jellyfin, seerr, siwe-oidc do, each with a comment saying why); the
19 per-Ingress `auth-url` annotations went. Default flipped from
open-unless-annotated to gated-unless-opted-out; verified identical
401/open split from inside the controller and 200 through the
gateway. `notes.md` lost 8 stale entries (p0relay scratch, herdr
recipes the swarm skills own, superseded timings).

Previous session (2026-10-20): **Audiobookshelf + Chaptarr in `media`** (`htsj`, `2eff6fa`).
A `books` RWX volume on `longhorn-bulk` (200Gi, `storage/pvcs.yaml`)
holds `audiobooks/` and `ebooks/`; `k8s/apps/chaptarr` (Readarr fork,
`0.9.965` by digest, port 8789, uid 1000, one root init chowns the
library roots like transmission's) writes it, `k8s/apps/audiobookshelf`
(`2.37.1` by digest, `PORT=13378`, one 10Gi volume with `config/` +
`metadata/` subPaths) reads it. Both `*.gw.mesh.internal` behind the
admins gate; ABS keeps its own accounts (Vaultwarden posture, not
Jellyfin's OIDC). Chaptarr's configurator adds NZBget + Transmission
(categories `Audiobooks`/`Ebooks`, now declared in nzbget's conf),
NZBgeek (3030/7000/7020), and registers in Prowlarr as a **Readarr**
app (Prowlarr has no Chaptarr type, Prowlarr#2772). Verified live: both
200 through the gateway, clients/indexer/pairing present.

## Loose threads

- Actual: OpenID against the SIWE bridge (`ACTUAL_LOGIN_METHOD=openid`)
  is the path if the household ever needs per-person accounts; not
  wired.
- ADR-0032 still says "one Ingress annotation per app" for the gate;
  the mechanism is now the controller default (comments in
  `k8s/apps/media/ingress.yaml` and `ingress-nginx/application.yaml`
  are the authoritative description). An amendment line on the ADR
  would close the gap.

- **Chaptarr first run in the UI:** root folders `/books/audiobooks`
  and `/books/ebooks` (per-format profile object, not scriptable before
  first start — see `chaptarr/configmap.yaml`), with "write
  Audiobookshelf metadata" on. **ABS first run:** create the admin
  user, add a library at `/books/audiobooks` (and one for ebooks).
- Prowlarr still holds **no indexers** (added in the UI, none yet), so
  Chaptarr gets only NZBgeek until that happens. Chaptarr's Prowlarr
  PUT once failed transiently right after a pod restart (Prowlarr
  validates against a Chaptarr still booting); the next configurator
  run healed it. The PUT paths in all four *arr configurators have no
  retry, only the POSTs do.
- ABS 2.37 serves with `ROUTER_BASE_PATH` defaulting to
  `/audiobookshelf` and rewrites `/` into it internally; `/` works
  through the ingress, but if the apps misbehave, set the env to `""`.
- ABS OIDC against the SIWE bridge (PKCE, no secret) + an ungated
  Ingress is the path if household devices should listen; not started.
- From 10-17: HA onboarding/companion app, `b311`; hub image on fly
  still pre-pump `facethttp` (next `fly/deploy.sh` picks it up);
  Vaultwarden live sync / Grafana live untested after the gateway roll.
- From before: `thz2`, `m6xc`, `d0o0.*` / `e6pu.2.*` awaiting close,
  9+ `<!-- stale? -->` notes.

## Suggested next steps

- Do the two first-run setups above, drop one audiobook in, confirm
  Chaptarr import → ABS scan end to end; then close `htsj`.
- Add indexers to Prowlarr so the pairing carries something.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
