# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Implementation, orchestrated.** The authority model is
fully ruled (ADR-0017/0018/0019 + decisions `89v dyf fje itb wms sn4
vl4 j0b bjg 4oz h90 how w5s`) and model-checked
(`verification/quint/{authorize,runway,approval,clock}.qnt`); the Go
`protocol/cert` + `protocol/clock` match the models 1:1. An
orchestrator session hands bd issues to worker agents (one worktree
per issue, branch `swarm/<id>`, review + gate before fast-forward to
`main`). **Batches 1 and 2 landed** (`k3o 0bc.1 cmi czi/jp2 359.1.4`;
`6z9a zev 44r ow7`). The iroh Go binding is in-house (`iroh-go/`,
bindgen, no sidecar). Next: rule `2g6` (rooted = signature-only vs
live-at-`now`) then `2qp`; `/skill:grill-design` on `0bc.2`; Phase 0
probes `359.1.1–.3` need fly scratch infra / an Android device, then
the gate `359.1.5`; Phase 1 (`359.8.*`) waits on it.

**Toward goal:** **Sovereign-actor protocol at the center** and
**Mesh v3** in `desired-state/goals.md` (ADR-0016, decision `5w1`).

**Out of scope:**
- Nothing in the repo is protected: no production system depends on
  it (owner ruling 2026-09-06) — break the nebula-era code where the
  new shape needs it; the deferred nebula-era issues close when the
  gate passes, not before.
- Phase 2+ decisions (`1gv` gateway header) until Phase 1 exists.
- Parents'-TV deployment (`4te`) — valid, LAN-direct, not the focus.
