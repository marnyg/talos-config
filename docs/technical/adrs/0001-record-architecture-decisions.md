# ADR-0001: Record architecture decisions

- Status: Accepted
- Date: 2026-07-29

## Context and Problem Statement

We need a durable record of significant architecture decisions so future contributors (human and agent) understand *why* the system looks the way it does, not just *what* it looks like.

## Decision Drivers

- Future contributors lose context fast without written rationale.
- Agents need machine-readable history to avoid re-litigating settled choices.
- Decisions tied to alternatives we considered prevent re-attempting ruled-out paths.

## Considered Options

- Free-form decision notes in a wiki.
- No formal record (rely on git history and tribal knowledge).
- MADR-formatted ADRs in this repo.

## Decision Outcome

Chosen: **MADR-formatted ADRs in `docs/technical/adrs/`**, numbered sequentially, immutable once `Accepted`. Pairs with `docs/day-to-day/exploration-log.md`: ruled-out options graduate into ADR `Considered Options` when a decision lands.

### Consequences

- Every significant architecture decision gets a numbered ADR.
- Superseding an ADR is done by writing a new ADR that references the old one and updating the old one's status to `Superseded by ADR-NNNN`.
- Trivial decisions (library version bumps, code style) do not get ADRs.
- Prior decisions recorded in Taskwarrior (`+decision status:any +repo_5efa11ff`) remain valid; new significant ones get ADRs here (a Taskwarrior `+decision` entry may point at the ADR).
