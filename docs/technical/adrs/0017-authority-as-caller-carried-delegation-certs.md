# ADR-0017: Authority is caller-carried delegation certs; policy compiles to grants

- Status: Accepted _(Proposed 2026-09-03 from spike `talos-config-359.2`;
  accepted 2026-09-21 with Mesh v3 Phase 4. Built against it:
  `protocol/cert.Authorize` (`authorize.qnt` + property suite), the
  policy compiler `config-server/policy` (`359.8.5`), the gateway's
  per-stream `authorize()` with `X-Mesh-Node/Name/Groups` injection
  (P2.3, `3e9fdef`, ADR-0026), the node agent's `apid`/`kube-api`
  facets. Option A's residue — the nebula firewall tables and
  `talos/mesh-policy.yaml` — was deleted 2026-09-21 (`600d2d4`);
  `talos/mesh-policy-v3.yaml` is the only recipe. Confirmation status:
  the property suite / Quint check holds; the one-poll-interval
  policy-propagation and the 6-day-starvation checks have not yet been
  observed on the deployed system — they stay open items under
  Confirmation below, not blockers to acceptance.)_
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
  **2026-09-18 amendment** (`359.8.5` grill-design; see "Amendment
  2026-09-18" below): grant `target` is the wildcard (protocol
  ADR-0004); kind ≡ facet vocabulary; the iroh relay is not a facet;
  two recipe files during the dual plane; "(b) accept tables" is a
  shared vocabulary, not a rendered artifact.
- Related: ADR-0016 (Mesh v3), decision `talos-config-5w1` (protocol
  at the repo's center), `protocol/docs/sovereign-actor-protocol.md` §One
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
- **Name map**: name→NodeId is the Owner's namespace — witnessed by
  the member certs the hub sees on the beat, not compiled from git
  (git holds names, members mint keys; ADR-0024 amendment 2026-09-19,
  decision `2fc`); NodeId→endpoints is the producer's own
  `reach-me-at`; a dialing directory, never an authorization input.

### Amendment 2026-09-18 (`359.8.5` grill-design)

Four pins the original text left open, settled before the compiler is
built. Ruled-out alternatives (per-receiver targets from the location
cache; `target: group:<kind>` against the receiver's member cert —
kept as the upgrade path; generalising ADR-0024 F to nodes; relay as a
grantable facet; one merged v2+v3 file; deriving the nebula render from
v3; compiling all groups and filtering in `#bundle`) were logged in
`day-to-day/exploration-log.md` on 2026-09-18 and pruned once this
amendment was built (`git log -S"Policy compiler \`359.8.5\`"`).

- **Grant target is `"*"`** (protocol ADR-0004). Git cannot enumerate
  receiver keys (ADR-0015), so the compiler names no receiver; a grant
  is honored at every receiver that consented to the Owner for the
  facet. **Kind ≡ facet vocabulary**: facet names are disjoint across
  receiver kinds, so a grant's reach is its facet's; the recipe's
  `node:/gateway:/hub:` keys are validation structure (Nickel `6z9`),
  not compiled data. `host:` rules compile at `#bundle` time from the
  caller's member cert (`cav.name` → `aud: <caller key>`), so the
  compile step has no name-map dependency.
- **The iroh relay is not a facet.** It is a keyless transport child
  whose access hook sees only a NodeId; no grant can be presented to
  it. Hub facets are `hub-http` (stream) and the Issuer's actor facets;
  the `relay` rows leave the recipe; relay access is
  membership-implied (`5gz`: blocklist + beat cache). The `relay`
  *verb* stays reserved for the protocol's envelope relay (M3).
- **Hub → node `apid` stays on nebula until Phase 4** (`359.11.2`);
  then the hub is an ordinary caller with a self-minted kit and one
  recipe row `{facet: apid, host: hub}`. No special path. _(Done
  2026-09-21: auto-bootstrap read `etcd-running` off cp1 over the
  identity plane 8 s after unseal.)_
- **Two recipe files during the dual plane**: `talos/mesh-policy.yaml`
  (v2, frozen, nebula render) beside `talos/mesh-policy-v3.yaml` (this
  ADR's shape; the compiler's input). Phase 4 deletes v2. _(Done
  2026-09-21, `600d2d4`; `nickel/mesh-policy.ncl` went with it.)_
- **"(b) per-receiver accept tables" restated**: the recipe carries no
  forward address, so the hub renders no table for anyone. What hub
  and receivers share is the **vocabulary** — kinds, the closed facet
  set per kind, `ALPN(facet) = "talos-mesh/<facet>/v1"`, and
  `AcceptTable(kind)` as the ALPN→facet map `authorize()` takes; the
  receiver's `facet → forward` stays its own constant. The compiler is
  a pure package (`config-server/policy`): `Compile(recipe, caller
  identity, now) → []cert.Cert` unsigned, deterministic; the Issuer
  signs at `#bundle`. `4un`'s round-trip law is stated on its bead.

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
  Phase 1 lands. _(Redrawn as recipe→grants 2026-09-21, `22a84de`.)_

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
