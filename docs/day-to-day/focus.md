# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 2 execution** (task 1afafb50). Phase 1 is
fully closed 2026-07-30 — three exit checks plus criterion 4 (TV
dropped 3dfef644, phone measured-and-passed c4f07507). Order: certSANs
(nebula name + IP) → cluster endpoint to nebula IP + re-point
talosconfig/kubeconfig → strip wg0 from compose and delete hub wg*
code.

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — one overlay,
one derivation tree, LAN-direct peer paths. Completing phase 2 closes
invariant 5's bounded dual-overlay exception and retires the
DHCP-lease-drift failure class (invariant 7's live example).

**Out of scope:**
- Phone onboarding UX improvements (+later task; revisit at first
  90-day cert renewal).
- TV APK (task 2e1bef85) and direct remote paths (ADR-0006; IPv6
  trigger, task d06201ce).
- Auto-enroll for undeclared device names (thread a7920bda — needs an
  invariant-1 conversation first).
