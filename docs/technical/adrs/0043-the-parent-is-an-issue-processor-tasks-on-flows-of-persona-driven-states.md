# ADR-0043: The parent is an issue processor — tasks move through a flow of persona-driven states, by one transition, with the product as the only front door

- Status: Accepted 2026-10-19 (owner). Deployed 2026-10-22 (`a082862`),
  backoff + model-error classification 2026-10-19 (`1a38490`); first
  real task `62a0ec24` ran framed → needs-human (clarify) → framed →
  groomed → executing (5538 events) → needs-human by steer (`kmlg`).
  Executing-to-PR is not yet proven live.
- Date: 2026-10-22
- Related: ADR-0037 (the parent is the agent backend; this grows out of
  its "session" into a "task" above it, and overrides its consequence
  "if share lists ever become *policy* they move to git"), ADR-0036
  and decision `6d0u` (no orchestrator — re-examined below), root
  invariant 2 (actor-owned state; the data-plane exception), protocol
  ADR-0005 (consumer-driven additions), spike `talos-config-y8m0`
  (the design session, 2026-10-22), `bsj` (backup — now a hard
  dependency), `9io` (encrypting the user volume), `hp79` (the CLI).

## Context and Problem Statement

ADR-0037 gave the parent **sessions**: one prompt, one child, one
PR. Real use wants the thing above a session — a **task** that is
captured, tied to a reason, groomed, executed, reviewed, merged and
closed, where each of those steps is an agent with its own system
prompt and the human is in the loop at a few known points. Today
that pipeline exists only as hand-driven skills (`swarm-prep`,
`swarm-worker`, `swarm-run`, `docs-update`) over beads, a laptop-rooted
tracker. The question was what the unit is, where its state lives, who
moves it, and how it applies to any repo or to a project spanning
several.

## Decision Drivers

- "Automate as much as possible, with the human at a few gates."
- Works for **any repo and for projects with several repos**; repos
  are what a task touches, not what owns it.
- Every task is **framed by a value** — a project goal and a sentence
  of why — that every later step reads first.
- Protocol invariant 12 / ADR-0037: durable state is a data-plane
  resource outside the actor; the parent already is the durable-key
  actor with a store.
- Decision `6d0u`: no third-party orchestrator, no CRDs.
- ADR-0005: the protocol's `#task {repo, ref, prompt}` stays; a
  persona is a prompt, not a new envelope.
- Root invariant 2: an actor's durable state must be data-plane
  payload or bookkeeping about it, never control-plane truth.

## Considered Options

### Where the state machine's definition lives

- **In the parent's store, as immutable versioned documents** (flows
  and their personas; a task pins the version it started under) —
  chosen. Editing never moves a running task; defaults ship embedded
  in the binary and are seeded on first open.
- In the repo (`agents/flow.yaml`, `agents/personas/*.md`) — ruled
  out: a task may span repos, and a project is not a repo; the product
  must work for a repo with no such file.
- Beads labels/status as the state — ruled out: the label set is
  closed, and a cluster-side engine driving `bd` against a
  laptop-rooted dolt DB is a divergence machine.

### Where task instances live

- **A `tasks` table above `sessions` in the parent's store** — chosen:
  already durable, already FTS'd, already behind the person gate,
  already what the UI reads. Repo-scoped by naming the repo.
- Files in the repo (every transition a commit) — ruled out: concurrent
  personas conflict, main fills with noise, you end up on an orphan
  branch reinventing beads' dolt ref.
- Forgejo issues — ruled out: `talos-config` is on GitHub (`4la8`) and
  mirrors carry no issues; tracking GitHub repos' tasks in an unrelated
  Forgejo org.
- Beads as the system of record — ruled out, see above. Beads is
  superseded for this repo by this decision; its vocabulary (types,
  `discovered-from`/`blocked-by`, notes, spikes, handover) is the
  schema to port, its JSONL export the first import.

### How a state is left

- **One operation, `transition`, asked for by three kinds of caller**
  — chosen. An *agent* state spawns a session under its persona and
  the agent asks by ending its last message with a ```transition
  fenced block `{to, note, artifact, set, children, followups}`; a
  *human* state spawns nothing and a principal with rights asks via
  the API; a *children* state is left by the parent itself once every
  child task ended. The parent validates every ask: the exit must be
  in the state's `next`, the caller must be the session bound to the
  task (or a person in a human state, or in an agent state with no
  live session). Human gates are therefore not special machinery —
  they are states without a persona.
- A `transition` tool exposed into the sandbox (a new facet or a
  local MCP) — deferred: the fenced block needs no child change and
  the parent already reads the stream. Upgrade path: the same JSON as
  a typed stream event. (Thread.)
- The engine deciding transitions from the run's outcome — ruled
  out: the agent knows when it is done; the parent knows whether it
  is allowed.

### Failure

- **A run that ends without a block, with a bad block, or failed/lost,
  counts a retry; after `Retries` more attempts the task is parked in
  the flow's stuck state (human). An aborted run parks at once. The
  next attempt's prompt carries the previous failure.** — chosen. No
  transition happened, so nothing to roll back ("let the lease
  lapse").
- **Retries back off (`Backoff`, doubling per attempt, capped), and
  the beat spawns the attempt once due; a run whose model call the
  provider refused is reported as that (`model error: …`), and a
  refusal no retry can change (401–404, billing, authentication)
  parks at once without spending the attempts.** — added 2026-10-19
  after the first live run: OpenRouter `402` burned three leases in
  four seconds and the board said "no transition block".

### Multi-repo work

- **Execution is always one repo; a project-level task is groomed
  into leaf tasks (one per repo) that enter at the flow's leaf state,
  and the parent task waits in a children-driven state** — chosen.
  `#task` is unchanged; the join is the only orchestration primitive.
- A session cloning several repos — ruled out: changes the child, the
  credential story, and the PR-as-result shape.

### Front door

- **The product (the parent's web UI / API; the CLI `hp79` later) is
  the only way a task is created** — chosen (owner, 2026-10-22). No
  import from GitHub/Forgejo issues. Persona-created leaves enter at
  the leaf state; everything else (humans, the closer's follow-ups)
  at the initial state.

## Decision Outcome

The parent grows a **task layer**: `projects` (name, owner, flow
version, **goals** as the values tasks are framed against), `flows`
(immutable versions of `{initial, leaf, stuck, states{driver,
persona, next, doc}, personas}`), `tasks` (project, owner, title,
description, goal, why, parent, origin, repo, ref, flow@version,
state, retries, error, session) and `task_log` (every transition:
from, to, by, session, note, artifact). Sessions point at the task
they serve; the child's run id, which `sessions.task` used to hold,
is now `sessions.run` (migrated in place on first open). The default
flow is the swarm pipeline with the human at `captured`, `ready`
(merge) and `needs-human`: `captured → framed → groomed → executing |
delegated → reviewing → ready → landed → done`, with `rejected`.

This is not the orchestrator `6d0u` rejected: no reconciliation of
foreign resources, no CRDs — a parent moving its own children between
states it defines, which is what a supervisor is. The "agent framework"
and the "issue processor" are one program.

### Consequences

- **Beads is superseded for this repo.** Not migrated yet; the CLI
  (`hp79`) and an import are the path. Until then the two coexist and
  beads stays the human-level tracker.
- **ADR-0037's "policy moves to git" line is overridden**: flows,
  personas and goals are product configuration held by a data-plane
  tenant (the posture ADR-0041 gave observability), like Vaultwarden's
  or Actual's own data — invariant 2's data-plane exception. They are
  not control-plane truth (identity, membership, certs, machine
  config), which stays in git. The cost is honest: `bsj` (backup) is
  now required before real use, and the volume inherits `9io`.
- **Credentials move from per-image to per-project** eventually: the
  project names credential sets, provisioner policy maps names to
  secrets. Not built; today the image's outfit still applies.
- `#task` likely grows `tools[]` so a persona can narrow the sandbox
  (ADR-0005: when the first persona needs it).
- The `tmux`/`spawn -prompt`/direct session path survives unchanged;
  a task is a layer above, not a replacement.
- The backoff is wall-clock from the failure (`updated`), not from
  the provider's `Retry-After`; a 429 with a long window still spends
  an attempt per backoff step.

### Confirmation

A task filed from the phone is framed by an agent against the
project's goals, groomed into a brief, executed into a PR, reviewed,
parked at `ready` until the owner merges, and closed by an agent that
files the follow-ups — with the owner touching it twice. A split task
waits for its leaves and moves on by itself. A parent restart mid-flow
retries the lost attempt on the next beat. (The engine's test suite
covers each; the live confirmation is open.)
