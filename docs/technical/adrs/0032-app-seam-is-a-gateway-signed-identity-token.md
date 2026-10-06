# ADR-0032: The app seam is a gateway-signed per-request identity token; SIWE stays the person gate

- Status: Accepted (2026-10-06, built the same day; two rulings below)
- Date: 2026-10-06
- Revises: ADR-0010 ("the bridge is the only IdP" → the only *person*
  IdP; the app layer has two gates), ADR-0026 (consequence "apps may
  read `X-Mesh-*`; none is required to trust them" → apps never trust
  the bare headers; they trust the signed token the bridge verifies)
- Amends: `invariants.md` structural trade-off "capability discipline
  ends at the actor that terminates the stream" — it now ends one hop
  later, at the bridge, carried there by a signed artifact
- Related: ADR-0017 (authority is caller-carried), spike
  `talos-config-i1il` (`docs/spikes/auth-mesh-identity.md`), task
  `a0ys`, bug `5kh`, spike `95la`, ADR-0033

## Context and Problem Statement

The gateway (ADR-0026) terminates every identity stream, verifies the
caller's cert bundle offline, and injects `X-Mesh-Node/Name/Groups`
into each request — and then every app runs its own SIWE→OIDC login
anyway. Seven gated apps (sonarr, radarr, nzbget, transmission,
jackett, sillytavern, longhorn) keep no per-user state and already
trust their proxy; Jellyfin and ArgoCD keep per-user state. The
Tailscale `proxy-to-grafana` answer — trust the header — fails here:
the header is unforgeable only from *outside* the pod network, and
any pod can dial ingress-nginx or the bridge with a forged
`X-Mesh-Groups: admins`. Minting an OIDC token from such a header
would be ArgoCD cluster-admin for any pod. What may an app rely on,
and which gate fronts which app?

## Decision Drivers

- Invariant 1 (no identity state outside git + owner keys) and 2
  (verifiers decide from presented material; git is compiler input).
- ADR-0017: authority is carried by the party it empowers. A trusted
  header is ambient; a signed token is caller-carried.
- The identity names a **device and its groups**, never a person
  (`admins ⇒ the owner` is an N=1 inference; `5kh`).
- Forgery surface must not depend on reachability; flannel enforces
  no NetworkPolicy.
- Splices (`jellyfin:8096`, the TV) carry no HTTP at all.

## Considered Options

### Option A: keep SIWE everywhere

- Pros: nothing changes; wallet presence at session start.
- Cons: a login on every app for a device the network already
  admitted; on a device holding the hot wallet it is custody restated.

### Option B: bare trusted header as a group gate

nginx `auth_request` deciding from `X-Mesh-Groups` alone.

- Pros: zero code in the gateway; one Ingress annotation per app.
- Cons: unforgeability rests on reachability; any pod forges it.
  **Rejected by the owner 2026-10-06.**

### Option C: header-minted OIDC tokens at the bridge

- Pros: zero relying-party changes.
- Cons: with a bare header, a forged request mints `admins` — a
  regression from today, where a pod cannot get a token without the
  wallet.

### Option D: gateway-signed per-request token, verified by the bridge (chosen)

The gateway mints, per request, a short-lived EdDSA token under its
own member key — device key, name, groups, `aud` = Host, `exp` ≈ 60 s
— in `X-Mesh-Token`. The bridge verifies it against the gateway's
public key pinned in git: `/authz` for nginx `auth_request` (valid and
group matches → 200 with identity response headers; verified device
outside the group → 403; anything else → 401), and `/authorize`
honours the same token so OIDC apps issue a code without the wallet
prompt.

- Pros: self-authenticating artifact — forging it needs the gateway's
  key; `aud` stops replay across hosts, `exp` bounds the rest; no
  state anywhere; C becomes safe; oauth2-proxy retires.
- Cons: one sign per request (~20 µs); a verifier in the bridge; the
  token reaches the app too (an app is a pod-network attacker already;
  `aud` limits what a captured token buys).

## Decision Outcome

Chosen: **Option D.** The app layer has two gates. The **group gate**
(`auth_request` → `/authz`) fronts apps with no per-user state; the
**person gate** (SIWE→OIDC) fronts apps with per-user state and, via
the token at `/authorize`, logs a member device in without a prompt.
The bare `X-Mesh-*` headers remain informational; nothing may
authorize on them. The token is a presentation-layer artifact (plain
EdDSA JWT), not a protocol cert class — no new verb. Carrying the full
chain (wallet → `speak-as` → member cert → token) so the verifier
roots in the wallet address alone is an additive upgrade over the
pinned key.

### Rulings at build time (2026-10-06)

1. **No person fallback on the group gate.** The draft said "401 →
   `auth-signin` to SIWE as today", but "today" was oauth2-proxy's
   cookie, which retires; keeping a fallback means the bridge growing
   its own cookie session — oauth2-proxy rebuilt inside it. Who hits a
   401/403 at `/authz`? A pod-network caller (a redirect is pointless)
   or a member device whose cert lacks the group (a wallet signature
   cannot change the device's group). So: `auth-signin` is dropped for
   the group-gated class; the answer is 401/403 and nothing else.
2. **Device login is per-client opt-in, and mints the device.** The
   token names a device, never a person; `/authorize` honouring it
   mints `sub` = the device's actor id and the member name as username
   — mapping device → wallet owner from git would be exactly the N=1
   inference `5kh` removed. One user per device is right for ArgoCD
   (the audit log names the device) and wrong for Jellyfin (watch
   state would split per device), so a client opts in with
   `-token-client=<id>`; ArgoCD does, Jellyfin keeps the wallet page.

Implementation: `config-server/meshtoken` (mint/verify, no JWT
library, one algorithm, closed issuer set), `gateway.Proxy(upstream,
signer)`, `siweoidc` `/authz` + `Client.Token`, bridge flags
`-gateway=ed:…` / `-token-client`; the gateway id is pinned in
`k8s/apps/siwe-oidc/deployment.yaml`.

### Consequences

- oauth2-proxy, its SealedSecret and `hostAliases` go; the required
  group per app is one Ingress annotation in git; `media` becomes
  grantable per app.
- Gated-class revocation drops from a 7-day cookie to the token's
  60 s plus the connection bound (1 h).
- `5kh` (groups hardcoded `["admins"]`) landed first (`9d3b1bb`), so a
  second admin does not inherit the N=1 inference.
- Splices are untouched: appliances still need a Jellyfin credential
  (`95la` re-opened; Quick Connect stays).
- The trade-off text in `invariants.md` is amended: capability
  discipline ends at the bridge, one signed hop past the gateway.

### Confirmation

Admin device opens `sonarr.gw` with no prompt; a `media` device gets
403; a header-less request gets 401 (no redirect — ruling 1); a pod on
the pod network sending `X-Mesh-Groups: admins` and no valid token
gets 401; a member device reaches ArgoCD with no wallet prompt and
Jellyfin still shows it; a token captured at `sonarr.gw` is refused at `auth.gw`
(`aud`); `rg -l oauth2-proxy k8s/` is empty; no state file appears on
the bridge. Invalidated if a group-gated app grows per-user state
(move it to the person gate) or if per-request signing shows up in
gateway latency.
