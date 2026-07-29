# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 1 verdict** (uuid fca5be68). Everything is
built and deployed; three of four kill criteria cleared. Criterion 2's
remote case measured RELAYED on the CGNAT-hotspot pair — one
discriminating test remains (punch from office Wi-Fi, `+next` task)
before deciding: fire the criterion (keep wg0, LAN shortcut) or amend
it (LAN-direct + wg0-parity remote + drivers 2/3 justify phase 2).
That decision wants an ADR either way.

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — but driver 1
(direct peer paths) is now known to be LAN-only-at-best, so the goal's
calculus is exactly what's being decided.

**Out of scope:**
- Phase 2 cutover until the criterion 2 verdict and the ≥1wk dogfood.
  Invariant 5's two-overlay exception is the running cost.
- TV onboarding (task 36) until revocation policy (dc04e3e8) settles.
- App SSO / OIDC; ENS commitment layer (+idea).
