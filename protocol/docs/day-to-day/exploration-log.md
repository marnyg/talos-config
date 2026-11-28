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

<!-- 2026-10-19: §Root rotation (spike aja2, 2026-10-13 → 2026-10-18)
     pruned — ADR-0010 and ADR-0011 are Accepted (d0o0 closed, pins
     from talos/pins/ live on hub 3267876). The rulings — chain vs pin
     as the anchor, rooted equivocation (the rotation.qnt FINDING),
     the subtractive pin hop (authorize.qnt mutant p1), `res` pinned
     (djdl) — live in their Decision Outcome / Consequences. Recover
     from git history if needed. -->

## Relay admission (spike qx5c, 2026-11-28; ADR-0012 Proposed)

- 2026-11-28 — Tried carrying the `relay` chain as the relay client's
  bearer token (iroh 1.1.0 `RelayConfig.auth_token`, read by a proxy in
  front of the relay). Ruled out: iroh reads the token once per
  endpoint and re-sends it on every reconnect; `insert_relay`, with or
  without `remove_relay` first, does not replace it on a live endpoint
  (measured, 11 reconnects all with the old token). A credential that
  expires every beat would need a new endpoint every beat. The token
  survives as a stable claim (the id) only.
- 2026-11-28 — Considered the parent vouching for the child's key at
  the gate (`#admit`). Ruled out: the key does not exist before birth
  and birth needs the relay. The wildcard link in the intro is what
  replaces it.

