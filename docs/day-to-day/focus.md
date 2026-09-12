# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Protocol M2 — build what ADR-0001 ruled.** The authority
model (ADR-0017/18/19, Quint-checked, Go `protocol/cert` + `clock`
1:1) is done; M2's design is ruled (protocol ADR-0001, 2026-09-12):
one N-link chain verifier, self-authenticating envelopes, serial
actor mailbox, `#renew` as an ordinary facet, `reach-me-at` piggyback
as the discovery layer. Work runs in dependency order `0bc.2.1`
(Quint first) `→ .2 cert → .3 envelope → .5 actor → .6 iroh adapter`;
acceptance is two actors in one Go test over in-memory + iroh
transports. Orchestrated as before: one worktree per bead, branch
`swarm/<id>`, review + gate before fast-forward to `main`.
Phase 0 probes `359.1.1–.3` still need fly scratch / Android; the
Talos-node deployment of M2 (`0bc.2.7`) waits on `359.1.3`.

**Toward goal:** **Sovereign-actor protocol at the center** and
**Mesh v3** in `desired-state/goals.md` (ADR-0016, decision `5w1`);
protocol goals "one primitive" and "deployment-independent transport".

**Out of scope:**
- Nothing in the repo is protected (owner ruling 2026-09-06) — break
  nebula-era code where the new shape needs it.
- M3+ (lighthouse, frontdoor postage enforcement, spawn, money) —
  M2 only makes `aud:*` fail closed without postage; it enforces none.
- Choosing the open numbers (chain cap, `max_bytes`, mailbox depth,
  renewal fraction) — pick when a test forces it, record in the
  glossary.
- Parents'-TV deployment (`4te`).
