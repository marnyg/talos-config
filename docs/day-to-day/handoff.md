# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-31 (third session) — **the SSO arc landed end-to-end**
(`f6e69d2`..`98dd4c9`, tasks 35–38 all closed; sketch 162c630d fully
realized). Every exposed service now authenticates against the wallet.

- `config-server/siweoidc/` + `cmd/siweoidc`: minimal OIDC provider,
  EIP-191 signature is the only auth, PKCE S256 mandatory, per-boot
  RS256 key, everything in-memory, clients/admins as deployment args.
  Deployed in-cluster (`sso` ns) at `http://auth.cp1.mesh.internal` —
  decoupled from the hub so SSO survives seal state and redeploys.
  First custom-image CI: `.github/workflows/siwe-oidc-image.yml` →
  `ghcr.io/marnyg/siwe-oidc` (public), `:latest` + `:<sha>`.
- ArgoCD: `oidc.config` → bridge (PKCE), `admins` group → `role:admin`,
  dex deleted, `argocd.cp1.mesh.internal` ingress. All encoded in the
  installer job (bootstrap-time only) *and* applied live by hand.
- oauth2-proxy (sso ns, sealed cookie secret) gates sonarr/radarr/
  nzbget/jackett/transmission via `auth_request` annotations; one
  cookie (`.cp1.mesh.internal`) opens all five.
- Jellyfin: `jellyfin-plugin-sso` auto-installed by the configurator
  (emptyDir → re-runs on every pod start), provider "wallet", groups →
  admin; relative login-button action keeps TV local login intact.
- One live bug found+fixed: x/oauth2 sends `client_id` as HTTP Basic
  (`75fb4e7`); one dead end logged: Talos host DNS does not serve
  `/etc/hosts` to pods, hence hostAliases pins on every relying-party
  pod (exploration-log).

## Loose threads

- **Task 39 (`+later`)**: wallet-derived CA in nebup → HTTPS over the
  mesh. Cookie/redirect config all assumes plain HTTP; flipping to
  HTTPS touches bridge issuer, oauth2-proxy cookie-secure, jellyfin
  provider, ArgoCD url.
- Jellyfin local `admin`/`admin` bootstrap credentials still in git
  (broken window, deferred); dex-era `stable` install.yaml pin also
  deferred (installer job pulls whatever `stable` serves).
- Media library still empty; u-media re-adoption unexercised with data.

## Suggested next steps

- Decide the two deferred broken windows (pin ArgoCD install.yaml
  version; Jellyfin bootstrap credentials → sealed secret or accept).
- Refill the media library.
- Mesh backlog: tasks 30/31/33/34 (`talos-config.mesh`) are the only
  open work besides 39.
