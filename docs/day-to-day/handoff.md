# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-22, the task layer — spike `y8m0`, ADR-0043 Proposed)

A design conversation ("task as the unit, a state machine where each
state is an agent with its own prompt, framed by a value") crystallised
into spike `y8m0` and was then built in one pass under `actors/parent`:

- `flow.go` — `Flow`/`State` (driver `agent|human|children`,
  `next`, `doc`), personas as system prompts, `Validate`, the
  **default flow** (captured → framed → groomed → executing|delegated
  → reviewing → ready → landed → done; needs-human; rejected),
  `RenderPrompt` (persona + project/goal/why frame + previous step's
  artifact + the exit contract), `ParseTransition` (the agent's last
  ```transition block).
- `tasks.go` — tables `flows`, `projects`, `goals`, `tasks`,
  `task_log`; **`sessions.task` (the child's run id) renamed to
  `sessions.run` in place** so `sessions.task` points at the task.
  `Store.Move` is the atomic transition; `Retry`, `Attach`,
  `LastAssistantText`.
- `engine.go` — `CreateTask`, `Transition` (one op, three callers),
  `apply`/`enter`/`spawnFor`/`join`, `sessionEnded` (settled → read the
  block; aborted → park; failed/lost → retry, `Options.Retries`
  default 2, then park in `stuck`), `reconcileTasks` on the beat.
- `webtasks.go` + `ui/tasks.html`, `ui/task.html` — board per project,
  task page with history/artifacts/exits, JSON under `/api/{flows,
  projects,tasks}`; same person gate, task = project owner's + task
  owner's + admins'.
- Tests: `engine_test.go` (flow run, retries → park, split → join,
  abort/lost/orphan reconcile), `tasks_test.go` (live-DB migration,
  flow validation, store), `web_test.go` `TestWebTasks`. `go test
  ./...` and `nix build .#actors-bin` green. Committed (`f23f1da`,
  `4edbd93`: children filed in the move's tx, reconcile by (flow,
  version, state), "start right away" off by default) and **deployed**
  (`a082862`): Longhorn snapshot `sap-parent-pre-tasklayer-20261022`
  first, then `sap-parent` on `4edbd93` — store migrated in place,
  restored clean, `/healthz` 200.

## Loose threads

- **No real task has run yet** — the person gate needs the owner's
  wallet. `kmlg`: log in at `agents.gw.mesh.internal/tasks`, make a
  project with goals, file a task without a repo, start it, watch the
  framer. `sap-provisioner`/`sap-lighthouse` still pin `7751fa0`
  (same binaries unchanged; pin all three next time).
- The transition is a fenced block, not a tool; a persona running a
  real repo has never been exercised (the fake pi answers markers).
  First live run: file a task with no repo in a throwaway project and
  watch the framer.
- Beads coexists with the task layer; nothing imports yet (`hp79` is
  the CLI; a `bd` JSONL import is the obvious first bridge).
- Retries have no backoff; `#task` refusals burn leases fast.
- `State.Tools` deliberately not added until `#task` carries it.
- Carried: `d0o0` (durable `talos/pins`), `tmm5`, `88uq`, ntfy first
  page, the workouts plates-form ambiguity.

## Suggested next steps

- Run the first real task (`kmlg`); then decide ADR-0043 and confirm
  the domain-model additions proposed in the session report.
- `bsj` — now blocking real use of the task layer, not just nice.
