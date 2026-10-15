# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Build the agent backend designed in ADR-0037. Done: Forgejo
(`ivof.1`), `agent-run.sh -r/-b` (`ivof.2`), driver RuntimeClass +
outfit (`e6pu.1`), and 2026-10-12 the child as an agent (`e6pu.2`:
`protocol/facethttp`, `actors/pirpc`, `#task`/`#steer`/`#abort` with
the `agent-events` uploader, `spawn -prompt` as the stand-in parent).
Next: ship the child in the `sandbox-agent` image and run the live
acceptance; then the parent actor itself (SQLite+FTS5 store,
list/watch/steer/search UI behind the person gate); `e6pu.3` (git
half in the child). Epic `ivof`. **Detour landed 2026-10-13:** root
rotation (protocol ADR-0010, spike `aja2`) — model + code built, the
desired-state amendments await the owner's ruling before it is wired
into the hub.

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
