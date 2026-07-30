# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 phase 2 is **complete** (2026-07-30): wg0 deleted,
one overlay, one derivation tree, invariant 5 unqualified. No active
build focus — next is either quality-of-life on the mesh (laptop
split-DNS, task 04126746) or leaving the mesh to dogfood and picking
up deferred debt (sealed-secrets upgrade 4d6d9e26, EPHEMERAL media
disk be79fbb1).

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — achieved in
its committed scope (LAN-direct peer paths, phone on the mesh, single
overlay). TV client and remote-direct paths remain explicitly deferred
(ADR-0006, task 2e1bef85).

**Out of scope:**
- Phone onboarding UX (+later; recurs at 90-day cert renewal).
- Auto-enroll for undeclared device names (thread a7920bda — needs an
  invariant-1 decision first).
- KMS-only disk encryption (ADR-0004 posture stands).
