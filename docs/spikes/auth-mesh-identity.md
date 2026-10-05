# Spike: should the mesh identity header log apps in? (`talos-config-i1il`)

Memo, 2026-10-05. Recommends Option D; ADR draft below. Inputs: ADR-0010/
0017/0026, `gateway.go`, `siweoidc.go`, `k8s/apps/*/ingress.yaml`,
`talos/mesh-policy-v3.yaml`, the owner's `auth notes` (2026-10-01).

## Question

The gateway terminates every identity stream and injects
`X-Mesh-Node/Name/Groups` from the caller's verified member cert
(ADR-0026); every app then runs its own SIWE→OIDC login (ADR-0010).
Should the header log apps in — replacing or bypassing SIWE for some or
all of them — and what does that do to the identity model, the forgery
surface and the appliance (splice) path?

## Identity model

**What the header names.** `cert.Authorize` yields `Identity{Key, Name,
Groups}` from the member cert only (`protocol/cert/authorize.go:58`).
`Name` is a *device* (`marius-mac`, the TV); `Groups` is one of the
closed set `admins | media`, chosen at enrollment under a wallet
signature (`deviceenroll.go:126`). The wallet roots every cert but is
**not carried per request**: the header is a device-and-group
principal, never a person.

**Device vs person at N=1.** The bridge maps one wallet to one username
(`-admin=0xf568…=mar`), so every `admins` device is the owner's and
"admins ⇒ mar" is a *valid inference* — but an app-layer inference from
a git fact, not a cert fact, and it breaks on the second admin wallet.
That is bug `5kh`'s shape (`siweoidc.go:72` hardcodes `groups:
["admins"]` for every wallet) — mention, do not fix. `media` devices
are shared appliances: there the device *is* the right principal.
Conclusion: a header can stand in for a *group* decision anywhere, for
a *person* decision only where person = device — nowhere with
per-user state.

**The `auth notes` DAG against the invariants** (each conflict named):

1. *"Revoking a role from a group strips members immediately; dynamic
   resolution."* vs **revocation latency ≥ runway**: groups live in the
   90 d member cert (30 d runway), facet grants are 7 d; immediate needs
   an online check or a push (refuted by invariant 2). *Reconcile:* the
   **app layer** may hold a receiver-side table ("per-app authorization
   is app-layer", ADR-0017 Facet); a required group on an Ingress is
   git → ArgoCD → nginx in minutes. Option B puts revocation there.
2. *"Anyone can grant a subset of their own."* Matches ADR-0017's
   intersection for grants (if `Delegable`); **not** for groups —
   member certs are hub-issued, `Delegable: false`. Partial match.
3. *"Editor of each user's subgraph; audit history."* vs **the grant is
   the record**: enumeration "is not a query this system answers".
   *Reconcile:* render the recipe (git) + issuance log (a projection).
4. *"App callback re-renders its DSL on each DAG change."* A push is
   receiver-side rendering. *Reconcile:* ArgoCD's `policy.csv` and
   Jellyfin's configurator already are that render, on commit/pod
   start. Static re-render on commit is honest; event-driven is not.
5. *"Permissions are the app's; auth transports attributes."*
   Compatible: a Group has "no semantics of its own" (ADR-0017);
   `X-Mesh-Groups` is the attestation layer the notes ask for.
6. *"Identities = key pair or oauth credentials."* Drop oauth
   (invariant 1). 7. *Permission hierarchy.* Flat today; an order
   belongs in the recipe (compiler input), never in a verifier.

## App matrix

Columns: gated today · consumes a trusted header natively / via plugin / via
a proxy gate (nginx `auth_request` to a header check) · per-user state · verdict.

| app | gated today | native | plugin | proxy gate | per-user state | verdict |
|---|---|---|---|---|---|---|
| sonarr | auth_request→oauth2-proxy (`admins`); `AuthenticationMethod=External` | no user mapping | — | yes | no (single user) | **B** |
| radarr | same as sonarr | no | — | yes | no | **B** |
| nzbget | auth_request; `ControlPassword=` + dummy user | no | — | yes | no | **B** |
| transmission | auth_request; `rpc-authentication-required=false` | no | — | yes | no | **B** |
| jackett | auth_request; no app password | no | — | yes | no | **B** |
| sillytavern | auth_request; `whitelistMode=false` | basicAuth only | — | yes | no (multi-user off) | **B** |
| longhorn UI | auth_request; chart ships no auth; can delete volumes | no | — | yes | no | **B** |
| jellyfin `:80` | plugin-sso OIDC (`roles/adminRoles: admins`) + Quick Connect | no | SSO plugin is OIDC/SAML — `unverified:` no header provider exists | would only gate, not log in | **yes** (users, watch state) | **A** |
| jellyfin `:8096` | none at app door; `jellyfin` facet, group `media` | raw splice: no headers | — | — | yes | — (see below) |
| argocd | native OIDC; `g, admins, role:admin` | no (`--disable-auth` only) | — | gate only; login still OIDC | yes (audit log names the user) | **A** |
| seerr | ungated by us; signs in against Jellyfin accounts | n/a | — | n/a | yes | **A** (unchanged) |
| rdp (`vms`) | `rdp` facet, group `admins`; guest login | raw splice | — | — | yes | — |

oauth2-proxy and siwe-oidc are infrastructure, not apps. **oauth2-proxy's
only consumers are the seven `auth_request` rows** (ArgoCD and Jellyfin
speak to the bridge directly), so Option B retires it entirely.

## Where it cannot work

- **Splices.** `jellyfin:8096` and `rdp:3389` are raw TCP: no headers.
  The appliance path — what `95la` and the spike's "prize" are about —
  is exactly this path.
- **An HTTP-terminated jellyfin facet would not help either.** The
  TV/Android clients need a Jellyfin access token (`AuthenticateByName`,
  Quick Connect); nothing server-side turns a header into one short of
  an `IAuthenticationProvider` plugin trusting a proxy header
  (`unverified:` none known) — new code plus Jellyfin-local user state.
- So **the header does not answer `95la` in the hoped-for direction**;
  Quick Connect stays. The appliance gain is already banked: the splice
  itself is group-gated.
- A path that is not gateway → ingress-nginx (port-forward, in-cluster curl)
  carries no header; a header gate must fail closed on absence.

## Forgery surface

- **Outside the pod network: unforgeable.** The gateway strips inbound
  `X-Mesh-*` and is ingress-nginx's only dialer (ADR-0026 rev 2) —
  reachability alone; the `1gv` geo gate is gone.
- **Inside the pod network: free.** Any pod can dial the ingress-nginx
  ClusterIP (or an app Service) with any headers; `unverified:` flannel
  enforces no NetworkPolicy, so no cheap fence.
- **Blast-radius delta per option, pod-network attacker:**
  - B on the seven stateless apps: *zero change*. Each already trusts
    its caller (`External`, empty password, rpc-auth off, no auth) and
    is directly dialable today. B only moves the gate from a cookie to
    the header the same pod could forge either way.
  - C (header → ID token at the bridge) for ArgoCD/Jellyfin:
    *regression*. Today a pod cannot obtain an `admins` ID token
    without the wallet; under C a forged header mints one — for ArgoCD
    that is cluster-admin. C is unsafe until the gateway→bridge hop is
    *authenticated*, not merely reachable (open question 1).
- **Against today's bearer.** SIWE yields a 7 d oauth2-proxy cookie
  (`--cookie-expire` default) on the device; B's bearer is the member
  cert + 7 d `invoke` grant, bounded to 1 h per connection
  (`ConnMaxAge`). Both device-held; SIWE adds "wallet present at
  session start", which on a device holding the hot wallet is custody
  restated. B tightens gated-class revocation from 7 d to ≤ 1 h after
  the blocklist beat. Group names are comma-joined; the set has none.

## Options

**A — keep SIWE everywhere** (status quo). Pros: one ceremony, wallet
proof per session, nothing to build. Cons: the friction the spike was
filed for; `media` can reach nothing gated (every gate says `admins`,
every wallet mints `admins` — `5kh`); seven "trust the proxy" knobs
while a verified identity is dropped; oauth2-proxy + secret + pin stay.

**B — header-only for group-gated apps with no per-user state.** A
`/authz?group=<g>` endpoint (≈40 lines, in the bridge binary or a
sibling) returns 200 iff `g ∈ X-Mesh-Groups`, 401 when the header is
absent, 403 otherwise; the seven Ingresses point `auth-url` at it and
drop `auth-signin`. oauth2-proxy, its SealedSecret, `hostAliases` pin
and `-client=` arg go. Pros: no login on admin devices; required group
per app is one git line (conflict 1); `media` becomes grantable;
forgery delta zero. Cons: wallet-presence signal gone for these apps;
an nginx `configuration-snippet` would be simpler but is disabled by
default (CVE-2021-25742), hence the endpoint.

**C — header as a second OIDC-equivalent identity.** The bridge's
`/authorize` sees `X-Mesh-*` (same gateway) and mints a token for the
device (`sub: ed:…`, `groups` from the header, `admins ⇒ mar` by the git
map) with no signature. Pros: zero relying-party changes; ArgoCD and
Jellyfin included. Cons: the forgery regression above; a device `sub`
makes per-device Jellyfin users; needs the hop authenticated. **Deferred.**

**D — hybrid per app**: B for the stateless class, A for Jellyfin, ArgoCD,
seerr; splices untouched; C held behind hop authentication.

## Recommendation

**Option D.** Specifically:

1. Build the `/authz` group gate; flip the seven stateless Ingresses
   (`sonarr radarr nzbget transmission jackett sillytavern longhorn`);
   delete oauth2-proxy. Acceptance: admin device opens `sonarr.gw` with
   no prompt; `media` device gets 403; header-less request gets 401;
   forged LAN headers still get nothing (ADR-0026 check).
2. Jellyfin and ArgoCD keep SIWE (person gate). The same endpoint may
   *additionally* group-gate them later (403 before the login page).
3. `95la` is **not** answered here; un-defer it. Quick Connect stays.
4. Fix `5kh` separately: mint `groups` from the `-admin=` map (e.g. a
   `=mar:admins` form), not a literal.
5. Record the model change in the ADR below. The trade-off "capability
   discipline ends at the actor that terminates the stream" is
   unchanged — the header *is* ambient authority; B checks it at the
   app's door instead of laundering it through a cookie. Invariant 1:
   no new state. Invariant 2: `/authz` is a pure function of (headers,
   query); the required group is git → ArgoCD compiler input.

Why not A: the gated class already trusts its caller; SIWE there buys a
cookie, not a check. Why not C now: pod-network reach = cluster-admin.

## Proposed ADR

Paste as `docs/technical/adrs/00NN-…md`; demote headings one level.

> **ADR-00NN: The mesh identity header is the group gate for stateless
> apps; SIWE→OIDC remains the person gate**
>
> - Status: Proposed
> - Date: 2026-10-05
> - Revises: ADR-0010 ("the only IdP" → the only *person* IdP; the
>   app layer has two gates), ADR-0026 (consequence "none is required
>   to trust them for a session" → the stateless gated class does)
> - Related: ADR-0017 (Group has no semantics of its own), spike
>   `talos-config-i1il`, bug `5kh`, `95la`, `docs/spikes/tls-over-mesh.md`
>
> ### Context and Problem Statement
> The gateway injects a verified device identity on every ingress
> request; every app still runs a SIWE→OIDC login. Seven gated apps
> have no per-user state and already trust their proxy; Jellyfin and
> ArgoCD keep per-user state. Which gate fronts which app?
>
> ### Decision Drivers
> Invariants 1–2 (no identity state; app-layer tables are git compiler
> input); the "ambient past the gateway" trade-off; forgery rests on
> reachability (ADR-0026 rev 2); the header names a device+group, never a person.
>
> ### Considered Options
> A keep SIWE everywhere · B header group gate for stateless apps · C
> header-minted OIDC tokens · D hybrid. (Analysis: the spike memo.)
>
> ### Decision Outcome
> **D.** A *group gate* — nginx `auth_request` to `/authz?group=<g>`,
> deciding from `X-Mesh-Groups` alone, 401 on absence — fronts apps
> with no per-user state. A *person gate* — SIWE→OIDC — fronts apps
> with per-user state. The header never mints a token: C is deferred
> until the gateway→bridge hop is authenticated rather than reachable.
> Splices carry no header and are unaffected.
>
> ### Consequences
> oauth2-proxy and its secret go; the required group per app is one
> Ingress annotation in git; `media` becomes grantable per app; gated-
> class revocation drops from a 7 d cookie to the connection bound;
> `95la` stays the appliance answer; `5kh` precedes a second admin.
>
> ### Confirmation
> Right if: admin devices open the gated class with no prompt; `media`
> gets 403; header-less gets 401; forged LAN headers still fail; no
> state file appears. Invalidated if a stateless app grows per-user
> state (move it to the person gate) or the hop gets authenticated
> (revisit C).

## Open questions

1. **Authenticating the gateway→bridge hop** (unblocks C): a shared HMAC
   in a SealedSecret (a second mechanism beside the cert), or make the
   bridge a protocol receiver with an `authz` facet the gateway dials over
   iroh, so the stream's identity *is* the gateway's — the longer road.
2. **Is the owner's wallet a hardware wallet?** If so SIWE is a real second
   factor and B weakens the gated class; if hot, B is custody-equivalent.
3. **Where does `/authz` live** — bridge (app-layer seam owner) or gateway
   (holds the decision)? Memo assumes the bridge, keeping the gateway
   network-layer only per its package doc.
4. **Secure-cookie fallout.** B removes oauth2-proxy cookies; the question
   in `docs/spikes/tls-over-mesh.md` shrinks to Jellyfin/ArgoCD sessions.
5. **Configurator-minted per-device Jellyfin accounts** for appliances: the
   honest "device logs the app in" for splices; Jellyfin-local state, so
   a `95la` option, not this spike's.
6. Does Sonarr/Radarr v4 `External` read `Remote-User` (`unverified:`)?
   Irrelevant to B; would make a per-user audit trail free.
