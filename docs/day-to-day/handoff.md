# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-19: **Vaultwarden deployed** (`l5v8`, commit `eb1ad52`):
`k8s/apps/vaultwarden/` — Longhorn 2Gi volume, PSS-restricted pod on
uid 65534 (`ROCKET_PORT=8080`), `DOMAIN=https://vault.gw.mesh.internal`
because the Bitwarden clients need a secure context, which ADR-0033's
device-side TLS supplies. Ingress behind the admins group gate (the
extension and apps ride the device tunnel, so they carry the
X-Mesh-Token like a browser); Vaultwarden's master password is the
second gate — no SSO by design, the password is the encryption key.
ArgoCD synced it on push. Acceptance from the Mac: `/alive` 200 over
https through the gate, `/api/config` advertises the https endpoints,
the `/notifications/hub` WebSocket upgrade reaches Vaultwarden through
gateway → nginx (401 from Vaultwarden, not a proxy error). Longer
nginx read/send timeouts on the Ingress keep that socket open.

Previous session (2026-10-16): HTTPS over the mesh built and accepted
(`eu0m`, ADR-0033, `config-server/meshtls`); the workouts PWA installs.

## Loose threads

- `SIGNUPS_ALLOWED=true` in `vaultwarden/deployment.yaml` until the
  first account exists; flip to `"false"` in a follow-up commit.
- Vaultwarden's `rsa_key.pem` (its own JWT signing key) sits on the
  Longhorn volume like Forgejo's `secret_key`; both are data plane but
  are what `9io` (encrypt the user volume) is about.
- Not yet tried: the Bitwarden extension on Firefox/Chrome (Mac) and
  the Android app against a user-installed root — Android's
  network-security config decides, not the mesh. Phone acceptance is
  the open check.
- Android APK `a2cbc76` is on the Sony only — not published to
  `android-latest`; the TV has the old app (http still works there).
- Mac auto-trust does not work under launchd (admin trust write needs a
  session); the manual `add-trusted-cert` the log prints stays.
- `--cookie-secure`/https OIDC issuer: deferred until every browser
  client is on https (ADR-0033 consequences); nothing flipped.
- From before, still open: `thz2` (ssh live run), `m6xc`, the workouts
  `deployment.yaml` seeding recipe, the Strong password rotation,
  `d0o0.*` / `e6pu.2.*` awaiting close, 9+ `<!-- stale? -->` notes.

## Suggested next steps

- Open https://vault.gw.mesh.internal in Firefox, create the account,
  point the extension at it (Self-hosted → Server URL), then flip
  `SIGNUPS_ALLOWED` and close `l5v8`.
- `android/build.sh --publish` on the box so the TV/other devices get
  the https-capable app; test the Bitwarden Android app on the Sony.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
