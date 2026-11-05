# Tasks — domain model

<!-- Sub-scope of the root docs (docs/desired-state/). Scope: the
     single-user task system — a data-plane app on Longhorn behind the
     group gate, under invariant 2's exception (payload on a volume,
     not control-plane state). Source lives here; manifests in
     k8s/apps/tasks/, registered in k8s/clusters/homelab/cluster.yaml. -->

An ECS-shaped, event-sourced task store with several **systems**
(triage deck, dependency graph, kanban, groom queue, narrative) that
read the same entities through different component sets. The event
log is the only truth; everything else is rebuilt from it at boot.

```
            raises (any namespace)
  UI handler ───────────────► Event{seq, at, system, type, entity?, payload}
                                   │  appended to log (truth)
                                   ▼
        ┌──── reducer per system (writes ONLY its own kinds) ────┐
        ▼                        ▼                               ▼
  core.*  components      dag.* components              kanban.* components
        └──────────── components(entity_id, kind, payload) ────────┘
                                   │  read by ANY system
                                   ▼
          triage deck · groom queue · DAG · kanban · narrative (views)
```

## Glossary

- **Entity** — a bare id. Has no type; its meaning is whatever
  components are attached. Tasks and edges are both entities.
- **Component** — a typed payload keyed `(entity_id, kind)`. `kind` is
  namespaced by the owning system: `core.Done`, `kanban.Position`.
- **System** — a named module that *owns* zero or more component
  kinds, *reduces* events into its own kinds, and *mounts* views. A
  system may read every component; it may write only its own.
- **Event** — an intentful, append-only fact: `{seq, at, system,
  type, entity?, payload}`. Namespaced by the system whose reducer is
  canonical (`core.Snoozed`), not by whichever UI raised it. `entity`
  is nullable (an event may touch several entities or none).
- **Reducer** — `(event, writer) → component mutations` for one
  system. Writer is scoped to the system's own kinds. Reducers never
  emit events.
- **UI handler** — an HTTP handler in any system that turns a user
  action into exactly one event, of any namespace.
- **Edge** — an entity carrying `dag.Edge{from, to, kind}`. Only kind
  `blocks` exists today ("from blocks to"). The reducer rejects
  self-edges, duplicates and anything that would close a cycle, so the
  graph is acyclic by construction; done blockers stop blocking by
  derivation, the edge stays.
- **Tag** — a plain lower-case label on a task (`core.Tags`, a sorted
  set). The generic grouping vocabulary: the thing every view narrows
  by, and the hook a future view can key off (one board per tag).
  Any system may read it; only core writes it, and only from a user
  action (`Created{tags}`, `Tagged`, `Untagged`). A system that wants
  to write a tag programmatically wants its own component kind. There
  is no "create a tag" step — the vocabulary is derived.
- **Scope** — the active context: one tag every view narrows to (deck,
  all, graph, board, groom, log), or none. UI state, not domain state:
  "which context am I working in" is a fact about the user, not about
  any task, so it is a cookie (`scope`), never an event or component.
  The new-task form prefills the scope so a task born in a context
  stays visible there — in one `Created` event (invariant 2).
- **Panel** — a fragment a system contributes to the task detail page
  (`web.RegisterPanel`). How dag puts its dependency editor on core's
  detail page without core importing dag: the arrow stays view → owner.
- **Chrome** — keys a system contributes to every page's data
  (`web.RegisterChrome`), for what the shared layout shows but `web`
  does not own: core feeds the nav its scope selector this way.
- **Derivation** — anything computed at read time from components +
  `now`. Never stored (see list below).

## Systems and ownership

| system    | owns (components)                                             | canonical events                                                          |
|-----------|---------------------------------------------------------------|---------------------------------------------------------------------------|
| core      | Title, Body, CreatedAt, Done, SleepUntil, NeedsGrooming, Tags | Created{title,body,tags}, Edited{field,value}, Done, Reopened, Snoozed{until}, Woken, FlaggedForGrooming, Groomed, Tagged{tag}, Untagged{tag} |
| dag       | Edge{from,to,kind} (on edge entities)                         | Linked{from,to,kind}, Unlinked{from,to,kind} (both on the edge entity)    |
| kanban    | Position{rank}                                                | Moved{rank}; reduces core.Done → drop Position                            |
| triage    | —                                                             | — (raises core.Done / core.Snoozed / core.FlaggedForGrooming)             |
| groom     | —                                                             | — (raises core.Edited / core.Groomed / dag.Linked)                        |
| narrative | —                                                             | — (reads the log)                                                         |

Cross-system reductions are allowed and expected: kanban reduces
`core.Done` to drop its `Position`; core does not know kanban exists.

**Ownership rule:** a component belongs to the lowest system in which
every reader would be semantically wrong without it. If two siblings
need it the same way, it is not theirs — push it to core. (This is why
`Tags` is core and not a `tags` system: every view filters on it the
same way, and as a sibling it would have forced a cycle — core's list
view reading tags, tags checking `core.Title`.)

**Dependency direction:** views may depend on state-owning systems
(triage reads `dag.Edge` to exclude blocked tasks); views must not
depend on views; the system graph stays acyclic and points at core.
A state-owning system may read core (dag checks both ends exist) but
never a sibling owner.

## Derived, never stored

- **sleeping** — `SleepUntil > now`. No wake cron; `Woken` exists only
  for an explicit user un-snooze.
- **blocked / ready** — ready ⇔ no `blocks` edge into the task from a
  non-Done entity.
- **kanban column** — precedence done > sleeping > blocked > groom >
  ready. `Position{rank}` only orders *within* a column; an unmoved
  task's effective rank is its creation time, so explicit and implicit
  ranks share one number line and a move is a midpoint.
- **groom queue** — `NeedsGrooming ∧ ¬Done`. (A future "looks
  ungroomed" hint — missing estimate, no edges — would be a
  derivation too, not a flag.)
- **triage deck** — eligible = `¬Done ∧ ¬sleeping ∧ ¬blocked ∧ ¬NeedsGrooming`; order
  random, seeded per calendar day. No priority component.
- **in scope** — `scope = "" ∨ scope ∈ Tags`. Applied by every view
  after its own predicate; the graph still counts blockers outside
  the scope (a task waiting on one is still waiting), and the log
  keeps a line if its story touches any in-scope task.
- **vocabulary** — the union of `Tags` over all tasks, done or not.
  A tag exists while a task carries it; an empty set means no
  `Tags` component at all.
- **narrative** — the log filtered to an entity plus its edge
  neighbours, rendered as prose. This view is why events carry intent.
  Each system registers a `web.Narrator` for its own events (prose +
  touched entities); narrative itself knows no event shapes. Hence
  `dag.Unlinked` echoes the edge data: events are self-describing.

## Invariants (local to this scope)

1. The event log is the sole source of truth; component tables are a
   cache rebuilt at boot. Deleting them must lose nothing.
2. One user action appends exactly one event.
3. A reducer writes only kinds its system declared; enforced at
   registration, not by convention.
4. Reducers are pure over `(event, current components)` and never
   emit events — replay is deterministic.
5. Nothing in the "derived" list above is ever persisted.
6. Events are never rewritten; a change of meaning is a change of
   reducer (plus an upcaster if the payload shape changes).
7. `Tags` is written only by core, only from a user action. It is a
   user vocabulary, not a bag systems stash state in; a system that
   wants to tag programmatically declares its own kind.

## Ruled out

- **Component-delta events** (`ComponentSet{kind, json}`): loses
  intent; the narrative view would have to guess it back.
- **Component tables as truth + audit log**: close in cost at this
  scale, but forfeits "change the reducer, history follows".
- **Groom = missing-components predicate with a per-system registry**:
  replaced by a user-raised flag; simpler and the user decides.
- **Parent/child edge kind**: tempts a hierarchy that fights the flat
  entity model. Later hierarchy views add their own kinds.
- **Tags as their own system** (`tags.Tags`, `tags.Tagged`): fails the
  ownership rule (every view reads it the same way) and needs a
  `web.RegisterScoper` registry to dodge the core ↔ tags cycle.
- **Scope as an event** ("switched to #home"): not a fact about a
  task; would need a singleton component and a reducer for UI state.
- **Create-in-scope as two events** (`Created` then `Tagged`): breaks
  invariant 2; `Created` carries `tags` instead.
- **Game-engine ECS machinery** (archetypes, tick loops): systems
  here are query-time predicates, not per-tick iterators.

## Stack

Go, `html/template` + htmx, SQLite (log + rebuilt tables) on a
Longhorn PVC, one Deployment behind the group gate. Single user.
