# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-21 (nineteenth session) — **Mesh v3 Phase 4 closed: P4.3 +
P4.4, the paper half of the deletion.**

- `178d415` P4.3 — ADR-0002/0005 `Superseded by ADR-0016`; ADR-0016
  supersessions/revisions in force; **ADR-0017 Accepted** (two of its
  Confirmation checks — one-poll-interval propagation, 6-day
  starvation — are stated as not yet observed, not blockers);
  revision notes on 0006 (relay carries over), 0007 (mechanism
  retired, property survives via `authorize()` + `X-Mesh-*`), 0009
  (gateway zone), 0013 (internals swapped; stays Proposed on the TV
  gate), 0014 (v3 recipe only).
- P4.4 — `goals.md`: Mesh v3 **reached**; `invariants.md` #5 reworded
  (HTTPS 443 + KMS 8443, no UDP, dedicated v4 is KMS's);
  `deployed-state.md` **rewritten** against the live system (hub
  `0e67661`/hubkey `a65c301d…`, fleet image `p0agent-0.1.5`, one
  plane, policy rows, ports); punch-test pre-flight moved to
  `gotchas.md`; `mesh-v3-iroh.md` banner + Phase 4 data block +
  last open question closed; exploration-log "Mesh v3 — outcome"
  entry (strategy lessons, tried/ruled-out); `docs/README.md` marks
  mesh-v2 record as history.
- Live check found **w1 off** (owner closed it ~07:20Z) and the
  gateway's replacement pod on cp1 stuck on `Multi-Attach` for its
  RWO Longhorn PVC → every `*.gw.mesh.internal` down. Owner: leave to
  self-heal; filed the structural fix as **`9l67`** (HA sweep, P3).

## Loose threads

- `*.gw` services stay down until w1 returns or Longhorn releases
  `gateway-state`; three media volumes `faulted` meanwhile.
- Clients speak `enrollmsg` v2 until rebuilt (`5q33`, P3).
- Not measured on the new plane: 4K/throughput through the relay;
  the parents' TV (`4te`).
- `-n cp1` by name still fails from the tun (`t7b2`).

## Suggested next steps

- Close `359.11` (Phase 4) — user confirms. Decide whether the epic
  `359` closes now (goal reached) or waits for `4te`.
- Pick the next epic: HA sweep (`9l67`) or protocol v0 (`0bc`).
