# ADR-0001: The event log is the truth; systems own components

- Status: Proposed
- Date: 2026-10-24
- Beads: epic `talos-config-3vp0`, decision `talos-config-06sh`

## Context and Problem Statement

A single-user task app wants several views over the same tasks — a
tinder-style triage deck, a dependency graph, a kanban board, a groom
queue, a narrative — and each view interprets a task differently. The
question was how to store tasks so that adding a view (or a new kind
of fact about a task) never forces the others to change, and whether
event sourcing fits an ECS-style data model where an entity is only an
id and its meaning is the set of components attached.

## Decision Drivers

- Views must be addable without touching existing ones; a view's data
  needs must not leak into a shared schema.
- The narrative view wants *why*, not just *what changed*.
- Single user, tiny data: no appetite for projection stores, snapshots
  or catch-up machinery.
- The repo's invariant 2 treats this as data-plane payload: one volume,
  backed up as a whole; recoverability must follow from the files on it.

## Considered Options

### Option A: Component tables as truth + an audit log

Mutate `components(entity, kind, payload)` directly; append a
change-log row beside it.

- Pros: simplest mental model; no replay semantics.
- Cons: the log is a side effect, so the meaning of an action cannot be
  changed later; the narrative must reverse-engineer intent from
  deltas; two stores to keep consistent.

### Option B: Component-delta events as truth

Events are `ComponentSet{entity, kind, json}` / `ComponentRemoved`;
tables are rebuilt from them.

- Pros: new component kinds cost no event schema; trivial replay.
- Cons: the log says what changed, never why — "SleepUntil set to
  Friday" instead of "snoozed during triage".

### Option C: Intentful events as truth, components as projections (chosen)

Events are domain facts (`core.Snoozed{until}`, `dag.Linked{from,to}`),
namespaced by the system whose reducer is canonical. Each system owns
component kinds and reduces events into them through a writer scoped
to those kinds; it may read every kind. All reducers see the pre-event
state and their mutations apply after every reducer ran; a reducer
error means the event is not appended. Component tables live in
memory and are rebuilt by replay at boot.

- Pros: the narrative is a rendering of the log; a change of meaning is
  a change of reducer and history follows; views are derivations and
  need no storage; replay is deterministic and order-independent;
  ownership is enforced, not conventional.
- Cons: every new behaviour is a new event type plus a reducer;
  payload-shape changes need upcasters; a reducer bug surfaces as a
  boot failure (by design).

## Decision Outcome

Chosen: **Option C**, because the narrative view — the reason to keep
history at all — needs intent, and at this scale the in-memory rebuild
removes the usual cost of event sourcing (no persisted projections, no
snapshots). The scoped writer makes "a system owns its kinds" a
property of the program rather than a review comment.

Rules that fell out while building, now invariants of the scope
(`desired-state/domain-model.md`):

- One user action appends exactly one event; many systems may reduce
  it (kanban drops `Position` on `core.Done`; core does not know
  kanban exists).
- Any UI handler may raise any system's event; only the owning
  system's reducer interprets it.
- Reducers never emit events.
- Nothing derivable is stored: sleeping, blocked, ready, kanban column,
  the deck, the groom queue.
- Events are self-describing: `dag.Unlinked` echoes the edge data so
  the narrative can read it after the edge is gone.
- A component belongs to the lowest system in which every reader would
  be wrong without it; core therefore owns `Done`, `SleepUntil` and
  `NeedsGrooming`, which left triage and groom with no state at all.
- Dependency arrows point view → owner → core; cross-cutting UI
  (detail-page panels, narration) goes through registries in `web/`
  so core never imports a sibling.

### Consequences

- Adding a view is a package with `Mount` and, if it needs state, its
  own kinds; no existing package changes.
- Undo, "what did I do last week", and reinterpretation of old actions
  are available for free; none is built yet.
- The event log is the only file that matters on the volume; the
  component cache is disposable.
- A reducer change that rejects an old event crashloops the pod
  (`cmd/tasks` fatals on replay error). Fix the reducer or add an
  upcaster; never edit the log.
- `World.History` replays the whole log per read; acceptable
  single-user, the first thing to cache if it stops being so.

### Confirmation

Right if: the next two views (whatever use suggests — priority,
in-progress, estimates) land as new packages without edits to `core`
beyond, at most, a new owned kind; and the narrative stays readable
without a per-event special case in `narrative/`. Wrong if reducers
start reading each other's write intent (a sign ownership was drawn in
the wrong place) or if an event's payload has to change shape more
than once a quarter.
