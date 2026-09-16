# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 1 — identity plane beside nebula** (`359.8`,
unblocked by the Phase 0 gate 2026-09-16, decision `b2t`). Dual plane:
nebula untouched, the iroh plane grows next to it until Phase 2 moves
consumers one at a time. **Groomed 2026-09-16** into a DAG
(`mesh-v3-iroh.md §Phase 1`); the **hub actor cut is designed**
(ADR-0024 + protocol ADR-0003, domain-model §2 "Hub actors") and **the
protocol pre-work is done** (`xwu`, `kau`, `7ei` landed 2026-09-17/18;
protocol ADR-0002/0003 Accepted). Next: membership issuance `359.8.1`
against ADR-0018/0024 — the hub as the protocol's first real consumer —
and, in parallel, `359.8.2.2` (relay embedded in the hub). Exit checks are event-based
(`359.8.6`): node reboot, hub re-seal, laptop roam.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016,
gate passed) and **Sovereign-actor protocol at the center** — Phase 1's
hub actors (Issuer, Enroll, Relay, Provisioner) are the protocol's
first real inboxes.

**Out of scope:**
- Phase 2 consumer moves (talosctl/kubectl bridges in anger, gateway,
  Android app swap, k8s off the mesh) until Phase 1's exit checks pass.
- Tearing down the scratch relay (`kql`) before Phase 1.2 replaces it.
- Parents'-TV deployment (`4te`); storage work until w1 returns.
