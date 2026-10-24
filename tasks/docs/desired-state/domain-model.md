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
- **Panel** — a fragment a system contributes to the task detail page
  (`web.RegisterPanel`). How dag puts its dependency editor on core's
  detail page without core importing dag: the arrow stays view → owner.
- **Derivation** — anything computed at read time from components +
  `now`. Never stored (see list below).

## Systems and ownership

| system    | owns (components)                                             | canonical events                                                          |
|-----------|---------------------------------------------------------------|---------------------------------------------------------------------------|
| core      | Title, Body, CreatedAt, Done, SleepUntil, NeedsGrooming       | Created, Edited{field,value}, Done, Reopened, Snoozed{until}, Woken, FlaggedForGrooming, Groomed |
| dag       | Edge{from,to,kind} (on edge entities)                         | Linked{from,to,kind}, Unlinked{from,to,kind} (both on the edge entity)    |
| kanban    | Position{rank}                                                | Moved{rank}; reduces core.Done → drop Position                            |
| triage    | —                                                             | — (raises core.Done / core.Snoozed / core.FlaggedForGrooming)             |
| groom     | —                                                             | — (raises core.Edited / core.Groomed / dag.Linked)                        |
| narrative | —                                                             | — (reads the log)                                                         |

Cross-system reductions are allowed and expected: kanban reduces
`core.Done` to drop its `Position`; core does not know kanban exists.

**Ownership rule:** a component belongs to the lowest system in which
every reader would be semantically wrong without it. If two siblings
need it the same way, it is not theirs — push it to core.

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

## Ruled out

- **Component-delta events** (`ComponentSet{kind, json}`): loses
  intent; the narrative view would have to guess it back.
- **Component tables as truth + audit log**: close in cost at this
  scale, but forfeits "change the reducer, history follows".
- **Groom = missing-components predicate with a per-system registry**:
  replaced by a user-raised flag; simpler and the user decides.
- **Parent/child edge kind**: tempts a hierarchy that fights the flat
  entity model. Later hierarchy views add their own kinds.
- **Game-engine ECS machinery** (archetypes, tick loops): systems
  here are query-time predicates, not per-tick iterators.

## Stack

Go, `html/template` + htmx, SQLite (log + rebuilt tables) on a
Longhorn PVC, one Deployment behind the group gate. Single user.
