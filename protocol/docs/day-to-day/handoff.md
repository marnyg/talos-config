# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-19 — **`actor.Locations(ids...)`** (`859e4a7`, driven by the
talos hub's name map, `359.8.2.3`): `GetLocation` over many ids under
one lock and one clock reading; expired records evicted the same way.
A consumer projecting a directory (the talos `#bundle` name map; an
M3 lighthouse view) reads through it instead of N `GetLocation`s.
`location.go` and the glossary now say the `reach-me-at` lifetime is
the issuer's choice (ADR-0001's ≈ 1 h is a roaming actor's default;
the hub publishes 7 d). Read-only API addition, no ADR.

Earlier (2026-09-18): `actor.Multi` (`4230731`, one identity on N
wires, `Dial` routes on `ErrUnreachable`) and protocol ADR-0004
Accepted + built (`c4f5389`, `cav.target` wildcard `"*"`; the talos
policy compiler is its first consumer). Details in the ADR, the
glossary and the commits.

## Loose threads

- Decided in drafting, confirmed by landing, never discussed: mixed
  target sets reject at decode (not "ignore the `*`"); a `*` consent
  roots nothing rather than failing rule 4 later. Veto ⇒ reopen ADR-0004.
- `actor.Hold` has no ADR — `t29` (draft ADR-0005 if the pattern
  spreads). `actor.Multi` is in the same bucket: two consumer-driven
  runtime additions now; a third makes the pattern.
- The `reach-me-at` lifetime is now documented as the issuer's choice
  (glossary, `location.go`); ADR-0001's text still reads ≈ 1 h — fold
  it in the next time ADR-0001 is amended, not before.
- The held `speak-as` stays out of `Result.Verified` (ADR-0003); no
  facet→verb table yet; `DefaultMailbox = 64` and the renewal-beat
  fraction remain unbeaded.

## Suggested next steps

- No protocol change is queued by the talos consumer: the cp1 agent
  (`359.8.3`) uses `cert`/`actor` as is. Watch for the consumer wanting
  a persisted location cache (agent restarts) — that would be the
  first "safe-to-lose but optionally persisted" cache to cross the
  `protocol/` boundary.
- M3 `0bc.3` (lighthouse actor, `#publish`/`#lookup`, PoW postage for
  strangers) is the next protocol-side build when picked up.
