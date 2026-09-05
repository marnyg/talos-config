# ADR-0017: Authority is caller-carried delegation certs; policy compiles to grants

- Status: Proposed _(2026-09-03, from spike `talos-config-359.2`;
  promote when Mesh v3 Phase 1.1/1.5 land against it)_
- Date: 2026-09-03
- Revises: ADR-0014 (policy stays data in git, but it renders to
  grants carried by callers, not to firewall stanzas held by
  receivers); ADR-0007 (group + source-IP inference replaced by
  chain verification at the receiver)
- Amends: invariants 1 and 2 (see `desired-state/invariants.md`,
  2026-09-03 notes)
- Amended by: ADR-0018 (issuer is a `speak-as`-delegated per-process
  hub key; chain depth three; issuer rules compare *resolved* issuers),
  ADR-0019 (primitive gains `iat`; `authorize()` consumes the effective
  clock `max(local, lw)`). Inline 2026-09-05 notes: `z1z sqm xwz 3cx`.
- Related: ADR-0016 (Mesh v3), decision `talos-config-5w1` (protocol
  at the repo's center), `docs/sovereign-actor-protocol.md` §One
  primitive, `desired-state/domain-model.md` glossary (Verb, Grant,
  Facet, Name map, Consent grant, Authorize)

## Context and Problem Statement

Mesh v3 replaces nebula's firewall with a verifier we write (node
agent, gateway). Nebula authorized receiver-side: the group is in the
caller's cert, the *rule table* is compiled into every receiver's
config, and changing policy means pushing new tables to receivers
(`ap2`'s apid-push design, with its unseal-reconciliation problem).
The sovereign-actor sketch authorizes caller-side: one delegation
cert primitive `{iss, aud, can, cav, exp, sig}`, chains attenuate by
intersection, the receiver is root of authority over itself. The
mesh needed to pick one, and to define what a "group", a "verb" and
a "facet" are in whichever it picked — none of the three had a
precise definition.

## Decision Drivers

- Invariant 1 (stateless membership; device set bounded by signed
  acts, not enumerable) and invariant 2 (git is truth; "if a slice
  needs a database, redesign it").
- One primitive: the protocol's central claim (`5w1` makes it the
  repo's center) — a second authority mechanism beside the cert would
  refute it before it is built.
- N>1 sovereigns: a receiver cannot hold a table for issuers it has
  never met.
- The hub is sealed after every deploy until the wallet signs: cert
  lifetime is the mesh's runway without a hub.
- ALPN is visible in the QUIC ClientHello: facets must be coarse.

## Considered Options

### Option A: Receiver-side policy table (nebula model, ADR-0014 as built)

Caller presents membership (group in cert); receiver holds the
group×facet table and matches. Policy change = push tables to
receivers.

- Pros: what nebula does today; `mesh-policy.yaml` is the literal
  enforced object; simplest to build first.
- Cons: second authority mechanism with its own sync protocol and
  unseal reconciliation; impossible across sovereigns; verifier reads
  a table that is not a cert.

### Option B: Caller-carried grants; policy compiles to grants (chosen)

`mesh-policy.yaml` stays the Owner's recipe; the hub compiles it into
`invoke` grants whose `aud` is a group name. Callers fetch grants on
the renewal beat and present {member cert, grants} on connect. The
receiver verifies a chain that begins with its own **consent grant**
to the Owner, intersects, resolves group `aud` via the member cert
(same issuer, group ∈ `cav.groups`), and holds no table.

- Pros: one primitive; verifier is pure and offline; works for N>1;
  policy hot-reload falls out of grant renewal; git stays truth as
  compiler input.
- Cons: callers must present grants (bundle on connect); the Owner
  mints per-group grants (small set); exact "who has access now?" is
  unanswerable — bound + log only.

### Option C: Per-member grants, no groups in the chain

Every `aud` is a key.

- Pros: purest form.
- Cons: N×targets certs re-minted on every policy change; groups
  would need to be actors. Ruled out at N≈10.

### Option D: Receiver fetches grants by group from the hub

- Cons: the verifier dials a service to authorize — reintroduces a
  runtime lookup on the authorization path. Ruled out.

## Decision Outcome

Chosen: **Option B**, with these definitions (canonical text in the
domain-model glossary):

- **Verb (`can`)**: closed, versioned set; the object lives in
  structured caveats (`cav.target`, `cav.facet`), never in the verb
  string.
- **Facet**: a service-level class a receiver exposes, producer-owned
  accept table `facet → forward`; on the wire an ALPN class. One
  `ingress-http` for all HTTP UIs (per-app authorization is
  app-layer, ADR-0010); `jellyfin` raw TCP on the gateway; node agent
  owns only `apid`/`kube-api`. Ports appear in facet definitions and
  the device-local map only. Reachability is not a facet.
- **Group**: an issuer-scoped name for a set of members — the `aud`
  of grants and a `cav.groups` entry; no semantics of its own.
- **Consent grant**: explicit first link, `receiver → Owner, invoke
  {target: self, facet: *}, delegable`; replaces the implicit "trust
  the CA in my config".
- **Lifetimes**: consent bound to the accepted config; `member` 90 d;
  `invoke` 7 d polled daily; `reach-me-at` 1 h, self-issued by every
  actor (the hub never mints one on an actor's behalf); Owner
  `speak-as` deferred. Propagation is by poll, expiry is runway —
  **runway = lifetime − refresh cadence**, measured as *starvation*
  (time since the member's last completed beat), so `invoke` gives
  6 d and `member` 30 d. An expired cert does not renew (strict; the
  holder re-negotiates). _(Amended 2026-09-05 from `runway.qnt`:
  `z1z`, `sqm`, `xwz`.)_
- **Authorize**: once per stream, deterministic, offline,
  receiver-rooted, monotone under attenuation, fail-closed on any
  unknown; identity out comes from the member cert only, **and the
  member cert's issuer must be one the receiver holds a live consent
  grant for** (step 2b — without it a stranger-signed member cert
  reaches the gateway header; found by `authorize.qnt`, `3cx`,
  2026-09-05). Gateway caps stream lifetime at ≤ 1 h. Blocklist stays
  the plain git list in v0.
- **Name map**: name→NodeId is the Owner's namespace (git);
  NodeId→{port: facet} is the producer's advertisement; a dialing
  directory, never an authorization input.

### Consequences

- `ap2` (apid push of node firewalls) is superseded — receivers hold
  no table to push.
- `359.8.5` renders policy to grants + accept tables, not stanzas;
  Nickel contracts (`6z9`) and Quint `enroll.qnt` follow the new
  shapes; `authorize()` gets a rapid suite for the five properties.
- Grantor state becomes optional and non-authoritative (invariant 1
  amendment); the hub may log issuance for a projection.
- The domain model's §2 policy diagram (receiver-side render sites)
  describes the nebula-era implementation and must be redrawn when
  Phase 1 lands.

### Confirmation

Right if `authorize()` passes its property suite and Quint model with
the same inputs the gateway sees in Phase 2.3; if a policy change in
git reaches callers within one poll interval with no receiver
redeploy; and if **6 days of hub starvation** (no completed member
beat) cause no loss of access — the model-checked bound
(`verification/quint/runway.qnt`); the original "< 7 days sealed"
wording was refuted 2026-09-05 (`z1z`).
Wrong if any consumer needs a receiver-side table to express a rule —
that is Option A returning, and this ADR should be superseded rather
than patched.
