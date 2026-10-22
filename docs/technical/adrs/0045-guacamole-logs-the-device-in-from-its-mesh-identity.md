# ADR-0045: Guacamole logs the device in from its mesh identity

- Status: Accepted 2026-10-22 (owner). Built and verified the same day
  (`ea6cbe8`, `2965fae`): the owner's device landed on the connection
  list as `marius-mac` with no form and opened the desktop.
- Date: 2026-10-22
- Related: ADR-0032 (the group gate; `X-Mesh-Token` as the app seam),
  ADR-0033 (`:443` device front has no Upgrade path — why Guacamole is
  `http://`), `config-server/jellyfinqc` (the same shape for Jellyfin,
  `95la`), features `talos-config-ey3a` (Guacamole shipped) and
  `talos-config-5is2` (this work), `docs/day-to-day/exploration-log.md`
  "Guacamole on the mesh login".

## Context and Problem Statement

`ey3a` put Apache Guacamole in front of the win2k25 guest so a browser
on any admin device gets an RDP session. Access is the controller's
default gate — `group: admins` by device cert, decided by siwe-oidc's
`/authz` on the gateway-signed `X-Mesh-Token` — and then a *second*
gate: Guacamole's own username/password from a sealed
`user-mapping.xml`, which also carries the guest's Administrator
credentials. Every other `*.gw.mesh.internal` page opens as the device
is (Jellyfin's appliances by Quick Connect through `jellyfinqc`). The
second gate costs a login per device per session, its password is the
bootstrap placeholder, its 5-failure IP ban could only ever lock every
device out together (the gateway forwards a key, not an address), and
a `subPath` secret mount that never refreshes made resealing a pod
delete. The question: can Guacamole take the device identity the
gateway already proves, like the other apps?

Guacamole's SSO extensions (OpenID, SAML, header) provide *identity*
only; its file provider hands out connections only to users it
password-checked itself. So "switch the login" means switching the
connection source too.

## Decision

A front proxy — `config-server/guacjson`, run as a sidecar in the
`guacamole` pod with the Service re-pointed to it and the Ingress
untouched — intercepts the one request Guacamole's own client makes on
every page load, `POST /guacamole/api/tokens`. When the form body
carries no `username` and no `data`, the proxy verifies `X-Mesh-Token`
against the gateway id pinned in git (the same `meshtoken.Verifier`
the bridge and `jellyfinqc` use), requires group `admins` in the
claims, and adds a `data` parameter: the `guacamole-auth-json`
extension's blob — `{username: <X-Mesh-Name>, expires: now+2 min,
connections: {win2k25: {protocol: rdp, parameters: …}}}`, signed and
encrypted with a key both containers hold. The bundled JSON extension
(enabled in the upstream image by `JSON_SECRET_KEY`) authenticates it;
the client stores the resulting token and never shows a login form.
Everything else, the WebSocket tunnel included, is forwarded as-is
with the identity headers dropped.

Consequences, deliberately:

- `user-mapping.xml`, its SealedSecret and the `subPath` mount go.
  One sealed secret `guacamole-auth` remains: `json-secret-key` (both
  containers) and `guest-password` (sidecar only). RDP parameters are
  flags on the sidecar, visible in git.
- A request without a valid token, or from a device outside `admins`,
  is forwarded untouched: Guacamole shows its login form, which with
  no file provider authenticates nobody. Fail closed, nothing to brute
  force; `BAN_ENABLED=false` stays.
- Guacamole's user *is* the device (`name` in the member cert, so a
  re-keyed device keeps its name), and so is its log line. Two devices
  are two Guacamole users sharing one Windows `Administrator` session
  — Windows' one-session-per-user takeover, as with two `xfreerdp`
  today. Per-device Windows accounts are a guest question, out of
  scope.
- Break-glass is not a parallel password path in Guacamole; it is
  `xfreerdp` over the raw `rdp` facet, unchanged.
- Invariant 1 is untouched (identity is the device cert → gateway
  token; the JSON key is a transport secret between two containers in
  one pod, not an identity root). Invariant 8 holds (sealed in git,
  env in the pod).

## Considered Options

- **`guacamole-auth-header` trusting `X-Mesh-Name`.** Ruled out: the
  gate is at nginx and the Service is reachable from any pod with a
  forged header — exactly the hole `meshtoken` was made to close.
- **OpenID via siwe-oidc.** Ruled out: that is the person gate (a
  wallet signature per login — heavier than today, worst on a phone),
  Guacamole's OpenID extension wants the implicit flow, and it still
  provides no connections.
- **A proxy-served landing page that writes `localStorage.GUAC_AUTH`
  and redirects.** Works, ruled out: couples to the client's storage
  format; intercepting the token POST is one hook and no HTML.
- **Folding the login into `vmpower`.** Ruled out: different
  volatility (login vs power), and vmpower's stdlib-only, no-deps
  property would go.
- **Keeping `user-mapping.xml` as break-glass.** Ruled out: a parallel
  password path is the surface being removed.

## Confirmation (verified from source and live before building)

- The client POSTs `api/tokens` on every route it resolves, with the
  URL's query params (`indexRouteConfig.js` → `updateCurrentToken`).
- Blob format from `UserDataService`/`CryptoService`: signature
  *inside* the ciphertext; `guacjson.Encode` matches openssl
  byte-for-byte. Blobs are `singleUse` with a 2 min `expires`.
- `token` + `data` with a live session keeps the session (providers'
  default `updateAuthenticatedUser`); with a dead one it is a fresh
  login — so idle-timeout re-login is silent too.
