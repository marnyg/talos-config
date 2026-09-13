# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 0 — run the spike gate to a verdict.** Owner
picked Phase 0 over protocol M3 on 2026-09-13 (riskiest-first; the
probes gate `0bc.2.7`, actors on real Talos nodes, which is the stated
end goal). P0.4 (API churn) and P0.1 (self-hosted relay on fly) have
passed; the musl link (`cs3`) is proven. Next is **P0.3, the Talos
system-extension proof** (`359.1.3`: static agent boots as an
extension, dials the scratch relay outbound, forwards one ALPN-gated
stream to apid, survives `talosctl reboot`), then P0.2 Android
(`359.1.2`), then the gate decision `359.1.5`. Any failed check
re-defers Mesh v3 (`359`) — that is a valid outcome, not a setback.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016,
gated on this spike) and **Sovereign-actor protocol at the center**
(the relay/lighthouse on the hub is shared by M3 and Mesh v3).

**Out of scope:**
- Protocol M3 (`0bc.3`) until the gate; only the `xwu` ruling is
  pre-work for it.
- Phase 1 shapes (relay embedded in config-server `5gz`, QAD/cert
  `0pq`, membership gate on the relay) — record, don't build.
- Nothing in the repo is protected (owner ruling 2026-09-06); the
  scratch relay app is spike infra, torn down at the gate (`kql`).
- Parents'-TV deployment (`4te`); storage work until w1 returns.
