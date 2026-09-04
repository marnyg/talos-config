# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v3 Phase 0 — the spike gate (`talos-config-359.1`) —
with the authority model already pinned (ADR-0017, Proposed) so that
Phase 1 and the protocol package (`0bc.1`, epic `0bc`; restructure `k3o`) have a complete spec.
That spec is now model-checked (`verification/quint/{authorize,runway,approval}.qnt`);
five refuted sentences await a ruling (`3cx z1z sqm xwz vzj`) before `0bc.1` ports the laws to Go.
Four checks on scratch infra, spike branch only: self-hosted iroh
relay on fly with n0 infra verifiably absent; iroh inside an Android
`VpnService` holding ≥80 Mbps 4K; a minimal Talos-extension node agent
surviving reboot; an API-churn probe. Any one fails ⇒ `359` is
re-deferred and the deferred nebula backlog reopens. Plan and kill
criteria: `docs/mesh-v3-iroh.md`.

**Toward goal:** **Mesh v3** and **Sovereign-actor protocol at the
center** in `desired-state/goals.md` (ADR-0016, decision `5w1`):
identity-addressed mesh, k8s off the mesh, and the four components
double as the sovereign-actor sketch's gateway, device apps and
membership certs — the trigger that made this worth starting
(decision `talos-config-dlk`).

**Out of scope:**
- Anything in Phase 1+ before the gate decision (`359.1.5`), and any
  repo change beyond a spike branch during Phase 0.
- Closing the deferred nebula-era issues — they close only when the
  gate passes.
- Policy phase 3/4 (`ap2`) on the nebula path — superseded by
  `359.8.5` if v3 proceeds; parked, not abandoned.
- Parents'-TV deployment (`4te`) is LAN-direct and independent of the
  mesh — still valid work, just not the focus.
