# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session
2026-10-17: **Home Assistant deployed** (commit `17e576d`):
`k8s/apps/homeassistant/` — Longhorn 10Gi `/config`, image
`2026.10.2` pinned by digest, Ingress `home.gw.mesh.internal` behind
the admins gate with the 1 h nginx timeouts for `/api/websocket`.
Two departures from the Vaultwarden shape: the namespace is PSS
**baseline** (the official image is s6-overlay and `/init` must be
root; non-root is unsupported upstream), and an init container seeds
`configuration.yaml` once with `http.trusted_proxies: [10.244.0.0/16]`
— without it HA 400s every request ingress-nginx forwards. ClusterIP
only: no mDNS/SSDP discovery, no USB radios (bead `b311`). HA's own
onboarding account is the second gate; no header auth in core.
**Not yet accepted live** — pushed, ArgoCD syncs on its own; nobody
has opened the onboarding page yet.

Previous session (2026-10-16, docs say 10-19): Vaultwarden deployed
(`l5v8`, `eb1ad52`) — Longhorn 2Gi, PSS-restricted on uid 65534,
`DOMAIN=https://vault.gw.mesh.internal` (ADR-0033 secure context),
admins gate, master password as the second gate; accepted from the
Mac over https including the `/notifications/hub` WebSocket.

## Loose threads
- Home Assistant first-start acceptance: open
  `https://home.gw.mesh.internal` from a mesh device, expect the
  onboarding page (not a 400 — that would mean the trusted_proxies
  seed did not take or the pod CIDR moved); then the companion app on
  the Sony over the tunnel.
- `b311`: discovery/USB for HA needs hostNetwork or a device mount on
  a pinned node, which also puts :8123 on the LAN outside the gate.

- Vaultwarden signups are off since `d13b5a1` (first account made,
  `l5v8` closed); further users by invitation from inside the vault.
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

- `android/build.sh --publish` on the box so the TV/other devices get
  the https-capable app; test the Bitwarden Android app on the Sony.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
