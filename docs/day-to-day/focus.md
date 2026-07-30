# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 1 dogfood** (uuid fca5be68). The verdict is in:
criterion 2 resolved 2026-07-30 as *amended, not fired* (ADR-0006) —
remote paths are relay-by-default, which is wg0 parity, and LAN-direct
at 1.785ms is the actual win. Criteria 1–3 settled; criterion 4
(mobile/TV UX) remains open behind revocation policy. What's left of
phase 1 is ordinary use for the ≥1wk window.

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — now honestly
scoped: driver 1 is **LAN-direct peer paths**, remote P2P is a non-goal
because the networks, not the config, forbid it.

**Out of scope:**
- Phase 2 cutover until the dogfood completes and certSANs land.
  Invariant 5's dual-overlay exception is the running cost, no longer
  blocked on a punch verdict.
- Chasing direct remote paths — needs native IPv6 (task 43) or spending
  invariant 5. Both deliberately declined for now.
- App SSO / OIDC; ENS commitment layer (+idea).
