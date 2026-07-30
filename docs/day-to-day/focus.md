# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Wallet-anchored access to cluster services — the ingress
substrate is live (ADR-0009: scoped `<service>.<member>` names,
ingress-nginx over the mesh, tailscale gone); next is the stateless
SIWE→OIDC bridge and per-service SSO (tasks 38–41).

**Toward goal:** "Every exposed service authenticates against the
wallet" in `desired-state/goals.md` (added this session) — the last
hosted account left the access path, and authentication reduces to
the wallet.

**Out of scope:**
- HTTPS over the mesh — deferred until the wallet-derived CA lands in
  nebup (task 42, `+later`); plain HTTP is the recorded decision.
- Per-service mesh identities / nebula firewall authz — rejected in
  ADR-0009; authorization lives in the SSO layer.
- Phone onboarding UX / TV client — unchanged deferrals (5183f6ea,
  2e1bef85).
