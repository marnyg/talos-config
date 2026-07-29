# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 1** — dual overlay (uuid fca5be68). Everything is
built and cp1 is on the mesh; what remains is getting a **second member**
on it. `fly deploy` → unseal → `nebup` on the laptop, and then the ≥1
week dogfood can actually start — kill criteria 2–4 measure paths
*between* members, so a one-member mesh proves nothing yet.

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — direct peer
paths, phones/TV on the network, one derivation tree. ADR-0002 Accepted.

**Out of scope:**
- Phase 2 cutover (certSANs, endpoint move, wg0 removal) until phase 1
  dogfooding passes the kill criteria. Invariant 5's two-overlay
  exception ends there, so a stalled phase 2 is a real cost.
- App SSO / any OIDC issuer.
- Cert revocation/expiry mechanics: 5-year machine leaves stand until a
  config-refresh mechanism exists (thread dc04e3e8).
- TV onboarding (task 38): designed, deliberately not built until the
  laptop path has been dogfooded.
- ENS commitment layer (+idea task; strictly additive, never auth-path).
