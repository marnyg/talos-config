# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Build the agent backend designed in ADR-0037 (2026-10-11),
in order: Forgejo on the cluster (`ivof.1`), the interim
`agent-run.sh -r/-b` workspace+PR flow (`ivof.2`), then agent-as-child
(`e6pu`: driver RuntimeClass + per-image secret sets, child beat
driving `pi --mode rpc`, `#task` → pointer), then the parent actor
itself (SQLite+FTS5 store, list/watch/steer/search UI behind the
person gate). Epic `ivof`.

**Toward goal:** "Sovereign-actor protocol at the center" (`goals.md`)
— the parent is the first long-lived actor that spawns children for
real work, and the first with a durable datastore of its own; it is
the N=2 consumer the protocol needs. Budget stays the open gap until
M5 (`0bc.5`).

**Out of scope:** kagent/ax (decision `6d0u`); volumes on the lease
(`264f`, until a spawned consumer); Forgejo deploy keys per child (the
per-repo endgame); SSH to Forgejo; a terminal client of the parent's
stream (after the web UI); NetworkPolicy for the sandbox; a second
control plane (`bhui`, `dsuj`); KMS onto 443 (`os8s`).
