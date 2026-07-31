# Documentation

Structured docs for humans and agents. Layout:

- **`context/`** — Why this project exists, users, owners. Stable.
- **`desired-state/`** — Goals, invariants, domain model. The north star agents read every session.
- **`technical/`** — ADRs, guides, and the deployed-state snapshot. Landed
  knowledge ([`gotchas.md`](technical/guides/gotchas.md) — traps that have
  each cost real time; [`reinstall.md`](technical/guides/reinstall.md) —
  how to wipe and recover cp1 without losing the media library;
  [`deployed-state.md`](technical/deployed-state.md) — where the running
  system stands, facts dated because they decay).
- **`day-to-day/`** — Active cross-session context. Handoffs, focus, notes, exploration log.

See `AGENTS.md` for how agents use this tree.

Pre-scaffold documents (authoritative, being gradually absorbed):

- [`vision.md`](vision.md) — north star + trust model narrative
- [`mesh-v2-nebula.md`](mesh-v2-nebula.md) — mesh design record (source for ADR-0002)

## Sub-scopes

In monorepos, services/apps/packages may have their own `docs/` mirroring this layout. Link relevant sub-scopes here:

<!-- e.g. - [config-server](../config-server/docs/) -->
