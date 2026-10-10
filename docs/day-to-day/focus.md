# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Build the agent backend designed in ADR-0037. Done 2026-10-11:
Forgejo (`ivof.1`), `agent-run.sh -r/-b` with a live PR (`ivof.2`),
driver RuntimeClass + outfit (`e6pu.1`). Next: `e6pu.2` — the child
beat driving `pi --mode rpc` over a stream facet (`facethttp` moves to
`protocol/`, decision `k5hb`), `#task` → pointer; then the parent actor
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
