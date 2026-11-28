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
- **`spikes/`** — Spike memos: one file per `bd` spike (`<slug>.md`, ≤ 250
  lines, recommendation + proposed ADR body), with draft manifests in a
  sibling `<slug>/` dir. Drafts park here because only directories
  listed in `k8s/clusters/homelab/cluster.yaml` deploy (ADR-0040, one
  Application per `k8s/apps/<name>`) — nothing under `docs/` does.
  Promote with `git mv` + a line there. Rulings live on the bead's notes.

See `AGENTS.md` for how agents use this tree.

Pre-scaffold documents (authoritative, being gradually absorbed):

- [`vision.md`](vision.md) — north star + trust model narrative
- [`mesh-v2-nebula.md`](mesh-v2-nebula.md) — **history**: Mesh v2 (nebula) design record, source for ADR-0002; superseded by ADR-0016, nebula deleted 2026-09-21
- [`mesh-v3-iroh.md`](mesh-v3-iroh.md) — Mesh v3 (iroh) design record + migration plan (`talos-config-359`); Phase 4 landed 2026-09-21, outcome to be folded into the exploration log (`359.11.4`)

## Sub-scopes

In monorepos, services/apps/packages may have their own `docs/` mirroring this layout. Link relevant sub-scopes here:

- **protocol** — no longer a sub-scope: since 2026-11-28 (ADR-0052)
  the sovereign-actor protocol is its own repo,
  [github.com/marnyg/sap](https://github.com/marnyg/sap), with its own
  `docs/` (desired-state, ADRs from 0001, the design sketch). This repo
  consumes it by tag; this root scope is authoritative for the talos
  deployment only. Read sap's `docs/desired-state/` before changing how
  the hub uses the protocol.

- [**workouts**](../workouts/docs/) — the self-hosted workout tracker
  (epic `talos-config-yz75`): a data-plane app on Longhorn behind the
  group gate, not part of the control plane. Only
  [`desired-state/domain-model.md`](../workouts/docs/desired-state/domain-model.md)
  so far (exercises, workouts/sets, templates, metrics; records are
  derived). Read it before touching `workouts/`.

- [**tasks**](../tasks/docs/) — the single-user task system: an
  ECS-shaped, event-sourced store with several views (triage deck,
  DAG, kanban, groom, narrative) over the same entities. Design only
  so far — [`desired-state/domain-model.md`](../tasks/docs/desired-state/domain-model.md).
  Deployed from `k8s/apps/tasks/` at tasks.gw.mesh.internal; the
  architectural decision is its ADR-0001 (`tasks/docs/technical/adrs/`).

<!-- e.g. - [config-server](../config-server/docs/) -->
