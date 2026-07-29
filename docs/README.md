# Documentation

Structured docs for humans and agents. Layout:

- **`context/`** — Why this project exists, users, owners. Stable.
- **`desired-state/`** — Goals, invariants, domain model. The north star agents read every session.
- **`technical/`** — ADRs and guides. Landed knowledge.
- **`day-to-day/`** — Active cross-session context. Handoffs, focus, notes, exploration log.

See `AGENTS.md` for how agents use this tree.

Pre-scaffold documents (authoritative, being gradually absorbed):

- [`vision.md`](vision.md) — north star + trust model narrative
- [`handover.md`](handover.md) — legacy session handover (superseded by `day-to-day/handoff.md` going forward)
- [`mesh-v2-nebula.md`](mesh-v2-nebula.md) — mesh design record (source for ADR-0002)

## Sub-scopes

In monorepos, services/apps/packages may have their own `docs/` mirroring this layout. Link relevant sub-scopes here:

<!-- e.g. - [config-server](../config-server/docs/) -->
