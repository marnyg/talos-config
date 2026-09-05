# Protocol Documentation (sub-scope)

Structured docs for the **sovereign-actor protocol** — the reusable
core this repo is built around (decision `talos-config-5w1`). This is a
*sub-scope* of the repo-root `docs/` tree and mirrors its layout:

- **`desired-state/`** — the protocol's OWN goals, invariants, and
  domain model. Written independently of talos, fly, nebula, or
  Kubernetes: nothing here may name a specific deployment. The north
  star an agent reads before touching `protocol/` code.
- **`technical/adrs/`** — decisions scoped to the protocol itself.
  Protocol ADRs start at **0001** in this scope. The protocol's
  *founding* decisions are root ADRs **0017** (authority as
  caller-carried delegation certs), **0018** (unseal = `speak-as` to a
  hot key), and **0019** (time as a trust input / `iat` low-water
  mark); they are **referenced, not copied**.
- **`day-to-day/`** — active cross-session context. Stubs until this
  scope accrues its own sessions (see the root scope's `day-to-day/`
  in the meantime).
- [`sovereign-actor-protocol.md`](sovereign-actor-protocol.md) — the
  original design sketch (v4). Authoritative narrative; being
  gradually absorbed into `desired-state/`.

## Relation to the root scope

The repo root (`../../docs/`) documents the **talos deployment** — the
protocol's *first consumer*. Where the two scopes share a term
(Actor, Sovereign, Delegate, Verb, Grant, Consent grant, Facet,
Attenuation, Speak-as, Time / low-water mark, Authorize, Projection),
the **root glossary**
([`../../docs/desired-state/domain-model.md`](../../docs/desired-state/domain-model.md))
is authoritative for the deployment; this scope copies the definition
verbatim and cites it, and must never contradict it. The direction of
generality is the difference: the root scope is the N=1 single-sovereign
instance; this scope is the N-sovereign, deployment-free protocol.

## Traversal (from `AGENTS.md`)

When working under `protocol/`, read repo-root
`docs/desired-state/{goals,invariants,domain-model}.md` **and** this
scope's `desired-state/` files. Traverse at most ~3 scopes deep per
session. Budget: this scope's `goals.md` + `invariants.md` stay under
~300 lines total; `domain-model.md` is exempt.
