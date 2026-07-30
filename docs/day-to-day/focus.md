# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** The SSO arc is **complete** — every exposed service
authenticates against the wallet (ArgoCD native OIDC, five UIs via
oauth2-proxy `auth_request`, Jellyfin via plugin; all through the
in-cluster SIWE→OIDC bridge). No active arc; next candidates are the
deferred hardening items and refilling the media library.

**Toward goal:** "Every exposed service authenticates against the
wallet" in `desired-state/goals.md` — reached 2026-07-31. The last
remaining scope under that goal is HTTPS-over-mesh (task 39, `+later`).

**Out of scope:**
- HTTPS over the mesh — still deferred until the wallet-derived CA
  lands in nebup (task 39); plain HTTP remains the recorded decision.
- TV mesh client / phone onboarding UX — unchanged deferrals.
- Auto-enroll for undeclared devices (thread a7920bda) — needs an
  invariant-1 discussion before any code.
