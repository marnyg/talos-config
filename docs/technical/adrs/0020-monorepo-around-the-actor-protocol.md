# ADR-0020: The repo is a monorepo around the actor protocol; talos-config is its first consumer

- Status: Proposed _(2026-09-06, from decision `talos-config-5w1`;
  promote when the protocol Go module has a consumer wired — Mesh v3
  Phase 1)_
- Date: 2026-09-06
- Related: decision `talos-config-5w1` (the protocol lives in this repo,
  at its center; a separate repo was rejected), ADR-0016 (Mesh v3
  builds the protocol's transport, membership cert and gateway),
  ADR-0017/0018/0019 (the protocol's founding decisions — authority as
  caller-carried delegation certs, unseal as a `speak-as` to a hot key,
  time as a trust input), `protocol/docs/sovereign-actor-protocol.md`
  (the design sketch), epic `talos-config-0bc` (protocol v0 roadmap),
  `talos-config-0bc.1` (M1 cert primitive)

## Context and Problem Statement

Decision `5w1` (2026-09-03) ruled that the sovereign-actor protocol is
not a side project: this repo becomes a **monorepo with the protocol at
its center** and talos-config (hub, mesh, cluster) as its **first
consumer**. The protocol is the reusable core — actors as
keypair + wallet, authority as one delegation-cert primitive
(ADR-0017/0018/0019), negotiation as signed proposals. talos-config is
one N=1 single-sovereign instantiation of it.

That leaves a concrete structural question this ADR settles: *where does
the protocol code and its documentation physically live, and how does it
relate to the existing config-server module and the root `docs/` tree?*
The protocol must be reusable beyond talos, which means it cannot depend
on the hub, on Talos, on fly, or on nebula — but it must also be
buildable and importable by config-server when Mesh v3 wires it in.

## Decision Drivers

- **One primitive, reusable** (`5w1`): the protocol's central claim is
  that it is deployment-independent. A layout that let talos types leak
  into it would refute that.
- **No dependency inversion:** the protocol must not import
  config-server; config-server imports the protocol.
- **Docs contract** (`AGENTS.md`): every scope with its own code gets
  its own `docs/desired-state/`; the root scope stays authoritative for
  the deployment. Traversal is at most ~3 scopes deep per session.
- **Founding decisions already exist as root ADRs** (0017–0019); they
  should be referenced from the new scope, not duplicated.

## Considered Options

### Option A: Keep the protocol inside config-server

Protocol types live in a package under `config-server/`, sharing its Go
module.

- Pros: nothing new to wire; one module, one build.
- Cons: the protocol inherits config-server's dependency closure (Talos
  machinery, fly, nebula) and its module identity — it is no longer
  reusable by anything that is not the hub. Directly refutes `5w1`'s
  "reusable core". The docs would have no independent desired-state, so
  the protocol's own invariants would be entangled with the
  deployment's.

### Option B: A separate repository

The protocol lives in its own git repo, imported as a normal dependency.

- Pros: hardest possible boundary; obviously reusable.
- Cons: rejected by `5w1` explicitly. Splits the design conversation
  across two repos while the protocol and its only consumer are being
  co-designed; every cross-cutting change (a cert field, an authorize
  law) becomes a two-repo, two-PR dance with version skew. Premature
  for a v0 with exactly one consumer.

### Option C: Monorepo with the protocol as its own Go module + docs sub-scope (chosen)

`protocol/` is a **separate Go module**
(`github.com/marnyg/talos-config/protocol`, go 1.26.0) with its own
`docs/` sub-scope mirroring the root layout. config-server imports it
with a `replace` directive pointing at `./protocol` when Phase 1 wires
it in.

- Pros: the protocol has no dependency on config-server (enforced by
  being a separate module with its own, minimal go.mod); it is
  co-located for co-design; its docs carry its own goals, invariants,
  and domain model, so its claims are stated deployment-free and the
  root scope stays authoritative for talos. Referenced-not-copied
  founding ADRs avoid drift.
- Cons: two Go modules in one repo (a `replace` directive and a second
  `go vet ./...` target); the nix build must be taught about the new
  module when it is first consumed.

## Decision Outcome

Chosen: **Option C.** Concretely:

- `protocol/` is its own Go module
  (`github.com/marnyg/talos-config/protocol`, go 1.26.0; `go.mod` and
  `doc.go` already exist). Its dependency closure stays free of Talos,
  fly, nebula, and Kubernetes — that freedom is the reusability claim,
  checked by the module boundary.
- `protocol/docs/` is a **docs sub-scope** mirroring the root `docs/`:
  `desired-state/{goals,invariants,domain-model}.md` state the
  protocol's OWN goals/invariants/model; `technical/adrs/` starts
  numbering at 0001 (none yet); `day-to-day/` are stubs until the scope
  accrues its own sessions. The design sketch
  (`sovereign-actor-protocol.md`) moves under it.
- The protocol's **founding** decisions remain root ADRs
  0017/0018/0019 and are referenced from the sub-scope, not copied. The
  root glossary stays authoritative for shared terms (Actor, Grant,
  Facet, Speak-as, …); the sub-scope copies definitions verbatim and
  cites them.

### Consequences

- **Separate Go module, no dependency on config-server.** The protocol
  never imports the hub; the arrow points one way. `go vet ./...` and
  `go test ./...` run per module (`cd protocol && …` is its own gate).
- **config-server imports it via a `replace` directive** when Mesh v3
  Phase 1 wires it in (e.g. `replace github.com/marnyg/talos-config/protocol
  => ./protocol` in `config-server/go.mod`). Until then config-server
  does not depend on it at all.
- **Nix build:** the `buildGo126Module` fileset in `flake.nix` will
  need `./protocol` added to its source set at the point Phase 1 wires
  the import — otherwise the module is invisible to the sandboxed
  build. **Do not change `flake.nix` now**; it is noted here so the
  Phase 1 worker does not miss it.
- **Docs traversal (from `AGENTS.md`):** an agent working under
  `protocol/` reads repo-root `docs/desired-state/{goals,invariants,
  domain-model}.md` **and** `protocol/docs/desired-state/`; at most ~3
  scopes deep per session. The root `docs/README.md` links the
  sub-scope.
- The root domain-model's "Relation to the sovereign-actor sketch"
  section and every reference to `sovereign-actor-protocol.md` now
  point at `protocol/docs/`.

### Confirmation

Right if: `cd protocol && go vet ./...` passes with a dependency
closure free of config-server, Talos, fly, and nebula; config-server
builds against the protocol through a `replace` directive when Phase 1
lands with no code moving back into config-server; and an agent
following the `AGENTS.md` traversal rule reaches the protocol's own
invariants before touching `protocol/` code.
Wrong if the protocol ever needs to import config-server (dependency
inverted — Option A returning) or if a talos/fly/nebula type appears in
a protocol package (the reusability claim is refuted, and this ADR
should be superseded).
