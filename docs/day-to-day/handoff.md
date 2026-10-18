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
  ./...` and `nix build .#actors-bin` green. **Not committed yet** at
  the time of writing this file — see below.

## Loose threads

- **Not deployed.** The parent image is unchanged on the cluster; the
  migration runs on the first start of the new image (`ALTER TABLE
  sessions RENAME COLUMN task TO run`). Back the PVC up first if
  `bsj` is still open — this is the first schema change on live data.
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

- Decide ADR-0043 (Proposed → Accepted), confirm the domain-model
  additions proposed in the session report, commit + push.
- Deploy the parent (`actors/build.sh`, pin in `k8s/apps/sap-parent`)
  after a PVC snapshot; run the first real task through `framed`.
- `bsj` — now blocking real use of the task layer, not just nice.
