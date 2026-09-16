# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 1 — identity plane beside nebula** (`359.8`,
unblocked by the Phase 0 gate 2026-09-16, decision `b2t`). Dual plane:
nebula untouched, the iroh plane grows next to it until Phase 2 moves
consumers one at a time. First act is grooming, not code: order the six
P1 beads (`359.8.2` says hub inbox message set + owned state **first**,
decision `vl4`; `359.8.1` membership issuance is the ready leaf), fold
the P0.2/P0.3 "findings that shape Phase 1" into beads, and decide how
Phase 1 and protocol M3 (`0bc.3`, `xwu`) share the hub-as-actors work.
Exit checks are event-based (`359.8.6`): node reboot, hub re-seal,
laptop roam.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016,
gate passed) and **Sovereign-actor protocol at the center** — Phase 1's
hub actors (Issuer, Enroll, Relay, Provisioner) are the protocol's
first real inboxes.

**Out of scope:**
- Phase 2 consumer moves (talosctl/kubectl bridges in anger, gateway,
  Android app swap, k8s off the mesh) until Phase 1's exit checks pass.
- Tearing down the scratch relay (`kql`) before Phase 1.2 replaces it.
- Parents'-TV deployment (`4te`); storage work until w1 returns.
