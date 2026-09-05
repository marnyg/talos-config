# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Implementation, orchestrated.** The authority model is
fully ruled (ADR-0017/0018/0019 + decisions `89v dyf fje itb wms sn4
vl4 j0b bjg`) and model-checked
(`verification/quint/{authorize,runway,approval,clock}.qnt`). An
orchestrator session hands bd issues to worker agents (one worktree
per issue, branch `swarm/<id>`, review + gate before fast-forward to
`main`). Batch 1: `k3o` monorepo restructure (lands first), `0bc.1`
cert primitive + `authorize()`, `cmi` CI for the models, `czi`/`jp2`
`speak-as` link in the models, `359.1.4` iroh API-churn probe. Phase 0
probes needing fly scratch infra / an Android device (`359.1.1–.3`)
run when the owner can provide them; Phase 1 (`359.8.*`) waits on the
gate `359.1.5`.

**Toward goal:** **Sovereign-actor protocol at the center** and
**Mesh v3** in `desired-state/goals.md` (ADR-0016, decision `5w1`).

**Out of scope:**
- Nothing in the repo is protected: no production system depends on
  it (owner ruling 2026-09-06) — break the nebula-era code where the
  new shape needs it; the deferred nebula-era issues close when the
  gate passes, not before.
- Phase 2+ decisions (`1gv` gateway header) until Phase 1 exists.
- Parents'-TV deployment (`4te`) — valid, LAN-direct, not the focus.
