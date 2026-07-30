# ADR-0010: A stateless in-cluster SIWE→OIDC bridge is the only IdP

- Status: Proposed
- Date: 2026-07-31

## Context and Problem Statement

The SSO goal ("every exposed service authenticates against the
wallet") needs an OpenID Connect provider: ArgoCD, oauth2-proxy, and
jellyfin-plugin-sso all speak OIDC, none speak EIP-191. Something must
bridge "prove you hold the admin wallet" into "here is an ID token".
The question is what that something is, where it runs, and what state
it is allowed to hold.

## Decision Drivers

- Invariant 1: all authentication reduces to offline wallet signature
  verification; no third-party identity accounts, self-hosted issuance
  only with owner-anchored trust.
- Invariant 2: no state a server owns — anything remembered must be
  recomputable from (repo + pure functions); "if a slice seems to need
  a database, redesign it".
- Cluster access must not depend on the hub's seal state: every fly
  deploy re-seals (thread `19a4c316`), and ArgoCD/service logins dying
  on every hub redeploy would couple daily access to unseal ceremony.
- Relying parties are off-the-shelf: whatever issues tokens must speak
  standard OIDC (discovery, JWKS, code flow) without per-app forks.
- Public clients only: redirect URIs live in cluster manifests in a
  public repo; a client secret there would be theatre.

## Considered Options

### Option A: Off-the-shelf IdP (dex / Keycloak / authentik)

Run a stock identity provider in-cluster; teach it about the wallet
via a connector or custom federation.

- Pros: battle-tested OIDC surface; no custom crypto-adjacent code.
- Cons: none has a SIWE authenticator — a custom connector is custom
  code anyway, now inside a large foreign codebase; all want persistent
  storage (users, clients, keys), i.e. exactly the database invariant 2
  forbids; dex was already running unused and broken (Error pod) — a
  second idle IdP to patch. Keycloak/authentik are order-of-magnitude
  heavier than the problem.

### Option B: Extend the hub (config-server) with OIDC endpoints

The hub already verifies wallet signatures (ethsig), serves wallet UX
(/status), and holds the admin allowlist; add /authorize, /token,
/jwks there.

- Pros: zero new deployment; reuses session/nonce machinery directly;
  no image CI needed.
- Cons: couples every service login to hub availability and seal
  posture — a fly redeploy or a sealed hub would lock the cluster's
  web UIs (SSO is not derived state and must not behave like it);
  grows the hub's public HTTPS surface (invariant 5 pressure: the
  issuer would ride the single public entrypoint, but browsers only
  ever reach services over the mesh, so a public issuer is pure
  attack surface with no user); the hub is trusted infrastructure,
  and the blast radius of a bug in hand-rolled OIDC handlers is
  smaller in a scratch container that holds nothing.

### Option C: Purpose-built stateless bridge, in-cluster (chosen)

~600 lines of Go (`config-server/siweoidc/`, shares the module and
`ethsig`): OIDC code flow with mandatory PKCE S256, no client secrets,
single-use codes bound to client+redirect+challenge, RS256 JWT signed
by a per-boot in-memory key, clients and admin→username map declared
as deployment args in git. Deployed from a `FROM scratch` image built
by CI to ghcr (`ghcr.io/marnyg/siwe-oidc` — infrastructure like fly,
not a root of trust; the image holds no secrets and every identity
decision reduces to signature recovery against git-declared
addresses). Issuer `http://auth.cp1.mesh.internal`, mesh-only via
ingress-nginx (ADR-0009).

- Pros: invariants 1–2 by construction — nothing persisted, nothing
  hosted, restart-safe by re-authentication; decoupled from hub seal
  state; the code is small enough to own and only ever *signs* JWTs
  (never parses untrusted ones — the risky half of JWT stays out).
- Cons: we own an OIDC implementation (mitigated by scope: one flow,
  one algorithm, PKCE-only, and the wire quirks are now tested —
  x/oauth2's Basic-auth client_id cost the first real login); per-boot
  JWKS means a bridge restart logs ArgoCD out (oauth2-proxy cookies
  survive); a new supply-chain path (ghcr image + CI) exists solely
  for this service.

## Decision Outcome

Chosen: **Option C**, because the invariants decide it — every
off-the-shelf IdP wants the database invariant 2 forbids, and hub
colocation (Option B) couples daily cluster access to seal ceremony
the design deliberately keeps rare. The bridge makes token issuance a
pure function of (git-declared config, wallet signature, per-boot
key), which is the same shape as every other trust reduction in the
fleet.

### Consequences

- Adding a relying party = one `-client=` arg in the bridge deployment
  plus client-side config: ArgoCD (native OIDC, dex deleted in the
  installer job), sonarr/radarr/nzbget/jackett/transmission (one
  oauth2-proxy via `auth_request`, one cookie), Jellyfin
  (jellyfin-plugin-sso, auto-installed by the configurator).
- Relying-party pods must resolve the issuer name: per-pod
  `hostAliases` pin `auth.cp1.mesh.internal` → cp1's derived mesh
  address, because Talos host DNS serves no `/etc/hosts` entries to
  kube-dns (siderolabs/talos#9822; see gotchas and exploration log).
- Break-glass narrows to per-app local accounts (ArgoCD `admin`,
  Jellyfin `admin`) — the bridge itself has no bypass credential.
- HTTPS-over-mesh (task `75c8b6b3`) will touch the issuer URL, cookie
  security flags, and every relying party's config in one move.

### Confirmation

Right if: relying parties keep working across bridge restarts with at
most a re-sign; a reinstall converges to working SSO with zero manual
identity setup; no state file ever appears. Invalidated if: a relying
party requires a confidential client or refresh tokens (the bridge
deliberately has neither), or OIDC surface grows past what ~600 lines
can carry — at that point revisit Option A with a SIWE connector
rather than growing this.
