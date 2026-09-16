# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 1 — identity plane beside nebula** (`359.8`,
unblocked by the Phase 0 gate 2026-09-16, decision `b2t`). Dual plane:
nebula untouched, the iroh plane grows next to it until Phase 2 moves
consumers one at a time. **Groomed 2026-09-16** into a DAG
(`mesh-v3-iroh.md §Phase 1`). Next up: **`359.8.2.1` hub actor cut** —
a grill-design session defining each hub actor's inbox message set and
owned state (Issuer, Enroll, Relay, Provisioner, hub-http; lighthouse
`0bc.3` as one more actor) before any handler is written (decision
`vl4`). Output: domain-model section + ADR. In parallel, protocol
`xwu` + `7ei` are hard prerequisites of membership issuance `359.8.1`;
`359.8.2.2` (relay in hub) is independent and ready. Exit checks are
event-based (`359.8.6`): node reboot, hub re-seal, laptop roam.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016,
gate passed) and **Sovereign-actor protocol at the center** — Phase 1's
hub actors (Issuer, Enroll, Relay, Provisioner) are the protocol's
first real inboxes.

**Out of scope:**
- Phase 2 consumer moves (talosctl/kubectl bridges in anger, gateway,
  Android app swap, k8s off the mesh) until Phase 1's exit checks pass.
- Tearing down the scratch relay (`kql`) before Phase 1.2 replaces it.
- Parents'-TV deployment (`4te`); storage work until w1 returns.
