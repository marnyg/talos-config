# Exploration Log — sovereign-actor protocol

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Protocol scope only; the
     talos deployment's log is ../../../docs/day-to-day/exploration-log.md. -->

<!-- 2026-09-13: §M2 (nine ruled-out alternatives, grill-design
     2026-09-12) pruned — ADR-0001 is Accepted and built (0bc.2.1–.6).
     The rulings live in technical/adrs/0001-*.md and the glossary
     (Envelope, Invocation, Reply, Location record, Renewal beat,
     Facet, Transport, Mailbox). Recover from git history if needed. -->

<!-- 2026-09-29: §M4 (birth handshake, provider seam, extend trigger,
     acceptance shape; grill-design 2026-09-21) pruned — ADR-0008 and
     ADR-0009 are Accepted after the live run (0bc.4.6), and every
     ruled-out alternative is in their Considered Options. Recover from
     git history if needed. -->

## Root rotation (spike `talos-config-aja2`, 2026-10-13)

- 2026-10-13 — Tried **the chain as the rotation anchor** (verifiers
  read an on-chain key-set epoch, or the current keys sign an epoch
  snapshot). Ruled out: circular if current keys sign it (an old-root
  thief signs epoch n+1), a new RPC dependency class at every leaf if
  verifiers read it; breaks inv 10. Landed on: the guardian set as the
  anchor — a thief holds the root key, not the guardians (ADR-0010).
- 2026-10-13 — Tried **root-signed guardian removal with a timelock +
  guardian veto**. Ruled out: a second ordering mechanism, time as an
  authority (a verifier that misses the veto accepts on its clock),
  and it generates the equivocation fork. Only the guardian set
  rotates; `guardians0 = {root}, k=1` is today's security, declared.
- 2026-10-13 — Tried **first-seen-wins on equivocation**. Ruled out: a
  safe-to-lose cache that is not safe to lose (ADR-0019's test).
  Landed on: reject both, dead until re-pinned out of band — and, by
  the model (`rotation.qnt` FINDING), only when the pair is *rooted*.
