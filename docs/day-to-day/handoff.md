# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-16: **HTTPS over the mesh, built and accepted** (`eu0m` closed,
ADR-0033 Accepted; forced by the workouts PWA). New C-free package
`config-server/meshtls` (per-device P-256 root, nameConstraints
`mesh.internal` critical, leaf per name, terminating reverse proxy);
`meshtun` sends `<fake IP>:443` to it over the `:80` facet's stream with
`X-Forwarded-Proto: https` + `X-Forwarded-Port: 443`. `irohup -tun` and
the Android tunnel mint the root under `<state>/tls`; the app gained
"Install HTTPS certificate". Gateway honours the peer's
`X-Forwarded-Proto: https` (image `2c1ecae`); ingress-nginx runs
`use-forwarded-headers`. Commits `fbe2f23` → `3b4a70e`, hub deployed +
unsealed. Acceptance: Mac Firefox lock + http 200; Sony (Android 13)
Chrome "Connection is secure", `sw.js` registered on https,
`Page.getInstallabilityErrors` empty. Pre-checks resolved from browser
source: nameConstraints on a local root enforced by Safari, Chrome/macOS,
Firefox — **not** Chrome/Android (recorded in ADR-0033 + the spike memo).

Side quest in `~/git/nixos` (pushed through `c13fa00`): talos-config
input bumped, the stray nixpkgs/home-manager/… lock bumps committed so
the repo matches the Mac's generation 123, quint-language-server moved
to `importNpmLock` (no `npmDepsHash`), atuin `doCheck = false`.

## Loose threads

- Android APK `a2cbc76` is on the Sony only — not published to
  `android-latest`; the TV has the old app (http still works there).
- Mac auto-trust does not work under launchd (admin trust write needs a
  session); the manual `add-trusted-cert` the log prints stays. Fine
  for now; a root rotation means repeating it + a Firefox restart.
- Chrome/Android ignores the root's name constraint: phone root reach
  = device custody. Accepted in ADR-0033; revisit if a shared device
  ever installs one.
- `--cookie-secure`/https OIDC issuer: deferred until every browser
  client is on https (ADR-0033 consequences); nothing flipped.
- From before, still open: `thz2` (ssh live run), `m6xc`, the workouts
  `deployment.yaml` seeding recipe, the Strong password rotation,
  `d0o0.*` / `e6pu.2.*` awaiting close, 9+ `<!-- stale? -->` notes.

## Suggested next steps

- `android/build.sh --publish` on the box so the TV/other devices get
  the https-capable app; install the root on any device that wants it.
- Tap "Install and create shortcut" on the phone and log a real
  session in the standalone PWA (the `yz75` follow-up).
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
