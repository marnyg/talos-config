# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 0 — run the spike gate to a verdict.** Owner
picked Phase 0 over protocol M3 on 2026-09-13 (riskiest-first; the
probes gate `0bc.2.7`, actors on real Talos nodes, which is the stated
end goal). P0.4 (API churn), P0.1 (self-hosted relay on fly) and
P0.3 (Talos extension: `ext-p0agent` live on cp1 since 2026-09-15)
have passed. **P0.2 Android (`359.1.2`) is two-thirds done**: the APK
(iroh in a `VpnService` + gvisor fake-IP DNS) Direct-Plays 4K on the
owner's phone end to end and the tunnel costs ≈ 3 % of drain (battery
pass); **the ≥ 80 Mbps throughput check needs the owner at home**
(LAN-direct on the Shield — from outside every path is the fly relay,
~50 Mbps). Plan + progress log in `docs/mesh-v3-p0.2-android.md`,
branch `spike/mesh-v3-p0.2`; then cp1 extension 0.0.4 (step 7), the
§P0.2 writeup, and the gate decision `359.1.5`. Any failed check re-defers Mesh v3
(`359`) — that is a valid outcome, not a setback.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016,
gated on this spike) and **Sovereign-actor protocol at the center**
(the relay/lighthouse on the hub is shared by M3 and Mesh v3).

**Out of scope:**
- Protocol M3 (`0bc.3`) until the gate; only the `xwu` ruling is
  pre-work for it.
- Phase 1 shapes (relay embedded in config-server `5gz`, QAD/cert
  `0pq`, membership gate on the relay) — record, don't build.
- Nothing in the repo is protected (owner ruling 2026-09-06); the
  scratch relay app and cp1's imager installer are spike infra, torn
  down or adopted at the gate (`kql`, `5cz`).
- Parents'-TV deployment (`4te`); storage work until w1 returns.
