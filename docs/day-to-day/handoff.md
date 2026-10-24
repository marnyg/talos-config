# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-24): the tasks app — designed, built, deployed (`3vp0`)

- **New sub-scope `tasks/`** (epic `3vp0`, decision `06sh`): an
  ECS-shaped, event-sourced single-user task app. Design in
  `tasks/docs/desired-state/domain-model.md`; the architectural
  decision drafted as `tasks/docs/technical/adrs/0001` (Proposed).
  Shape: bare-id entities, namespaced components, an append-only event
  log in SQLite as the *only* truth (component tables rebuilt in memory
  at boot), systems that own kinds and reduce events through writers
  scoped to those kinds, every view a derivation. Reducers see the
  pre-event state and never emit; a rejected event is never appended.
- **Five systems, all five views**: `core` (title/body/done/sleep/
  groom flag), `dag` (edge entities, `blocks`, acyclic by reducer),
  `kanban` (`Position{rank}`, derived columns), and the stateless
  `triage` deck (day-seeded order), `groom` queue and `narrative`
  (`/log` + per-task Story). Two registries in `web/` keep the arrows
  pointing view → owner: `RegisterPanel` (dag/narrative add sections to
  core's detail page) and `RegisterNarrator` (each system narrates its
  own events). `dag.Unlinked` echoes the edge data — events are
  self-describing.
- **Gate + deploy**: `nix build .#tasks-bin` (tests in checkPhase),
  `tasks-static`, `tasks-image`; CI `go` + `vendor-hash` matrices and
  the pre-push hook cover `tasks/`. Live at **tasks.gw.mesh.internal**
  (`platform-tasks`, image `tasks:07a1fbd@42cd69a1…`, 1Gi
  `longhorn-encrypted` PVC, group-gated, homepage tile). Dark theme.
- **Trap hit once**: a directory under `k8s/apps/` deploys nothing
  until it has a line in `k8s/clusters/homelab/cluster.yaml`
  (ADR-0040) — `089abec` sat Synced-and-absent until `3aa7b94`. The
  new GHCR package had to be flipped public by hand (gotchas).

## Loose threads

- `tasks`: `World.History` replays the whole log per detail page /
  `/log`; fine single-user, first hotspot if it ever isn't. Triage
  "do" means *done* — revisit if an in-progress notion is wanted.
  No htmx yet (plain forms); no mobile pass on the board.
- `docs/README.md` spikes paragraph still says the root ArgoCD app
  recurses `k8s/apps/` — untrue since ADR-0040; surfaced, undecided.
- Another session's uncommitted `k8s/apps/guacamole/deployment.yaml`
  edit (13 lines) has been in the worktree all session; not committed
  here.
- Carried from 2026-10-22 (see git history of this file for detail):
  `f0jl` first real stop→start untested, idle auto-stop undecided;
  `agents/scratch` stale PRs, PR 10 is the real one (`ready → done`);
  `xt40` token revocation; old plaintext PVs
  (`kubectl get pv -l encrypt-pvc/old`) and `gateway-state` encryption
  over LAN (`9io`); `dsuj` GPU hand-off + CP question; ntfy first page.

## Suggested next steps

- Use the tasks app for a week; the model's open questions (priority?
  in-progress? estimates for groom?) should come from use, not design.
  Accept/amend `tasks/docs/technical/adrs/0001`.
- Merge PR 10 and move `1d1c85ac` to `done` — the full flow-v2 loop.
- Delete the old plaintext PVs; `gateway-state` over LAN.
