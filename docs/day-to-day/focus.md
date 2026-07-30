# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 2 step 3** (task 1afafb50): move the hub's
control-channel duties (config-over-tunnel serving, source-IP auth,
auto-bootstrap dials) from `wg.tnet` to the nebula netstack, then strip
wg0 from compose and delete the hub's wg* code. Steps 1–2 landed
2026-07-31: mesh certSANs live, cluster endpoint is cp1's mesh address,
talosconfig/kubeconfig re-pointed.

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — one overlay,
one derivation tree, LAN-direct peer paths. Step 3 closes invariant 5's
bounded dual-overlay exception; the DHCP-lease-drift failure class
(invariant 7's live example) is already retired by step 2.

**Out of scope:**
- Laptop `.mesh.internal` split-DNS (task 04126746, +nice).
- Phone onboarding UX (+later; recurs at 90-day cert renewal).
- TV APK (2e1bef85), direct remote paths (ADR-0006, IPv6 trigger),
  auto-enroll for undeclared names (thread a7920bda).
