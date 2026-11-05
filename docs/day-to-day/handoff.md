# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-05): tasks — tags + scope (`p0i9`)

- **Tags are core data**, not a sibling system: `core.Tags` (sorted
  set of lower-case labels), `Created{tags}` so creating inside a
  scope is one event (tasks invariant 2 kept), `Tagged`/`Untagged`
  for later changes (no-ops rejected; empty set drops the component).
  The domain model's own ownership rule decided it — every view reads
  tags the same way, and a `tags` system would have forced a
  core ↔ tags cycle. Tasks invariant 7 added: tags are user-written
  only; a system wanting to tag programmatically declares its own kind.
- **Scope is UI state**: a `scope` cookie, never an event, set from a
  selector in the nav via the new `web.RegisterChrome` registry
  (`web.Render` now takes the request). Deck, all, graph, board,
  groom and log all narrow; the graph still counts out-of-scope
  blockers, the log keeps lines whose story touches an in-scope task,
  kanban moves step past the neighbour visible in scope. Tags panel
  on the detail page, chips on every card, new-form prefilled with
  the active scope.
- **Deployed** `2e1d843` (image `tasks:5b2d42e@cf8cd6cd…`); the new
  pod replayed the existing 24 tasks cleanly. ArgoCD needed a manual
  refresh annotation to pick up the pin inside its poll interval.
- Gate caught a real trap: `nix build .#tasks-bin` builds from the
  git tree, so an **untracked** new template (`tags.html`) passed
  `go test` locally and failed the nix checkPhase. Stage before gating.

## Loose threads

- `tasks`: the dag panel's candidate list is unscoped (every open
  task) — filter by scope with an "all" fallback once the vocabulary
  grows. `World.History` still replays the whole log per detail page /
  `/log` (now plus a `core.Scoped` pass under a scope); first hotspot.
  Multi-tag scope (`home,work`) not built — one tag per scope.
- `docs/README.md` spikes paragraph still says the root ArgoCD app
  recurses `k8s/apps/` (untrue since ADR-0040) — surfaced twice,
  undecided.
- Carried from 2026-10-24 (see git history of this file): ADR-0001
  (tasks) still Proposed — this session is a confirming data point
  ("at most a new owned kind"); `f0jl` first real stop→start
  untested; PR 10 `ready → done`; `xt40` token revocation; old
  plaintext PVs and `gateway-state` over LAN (`9io`); `dsuj` GPU
  hand-off + CP question; ntfy first page.

## Suggested next steps

- Use the tasks app with scopes for a week; the user's idea of **one
  board per tag** (views keyed off a tag) is the natural next view —
  it lands as a new package with no core change, which is the
  confirmation ADR-0001 asks for. Accept the ADR after that.
- Merge PR 10 and move `1d1c85ac` to `done`.
- Delete the old plaintext PVs; `gateway-state` over LAN.
