# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 1** — dual overlay (uuid 1afafb50): factory
schematic + nebula extension (`talosctl upgrade`, no wipe), wallet→HKDF
cert derivation + compose-time injection, `nebup` enrollment, wgdns
ported to the nebula netstack; then ≥1 week dogfood measuring
direct-vs-relay rate and throughput (kill criteria 2–4 still armed).

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — the spike gate
passed 2026-07-29; ADR-0002 is Accepted.

**Out of scope:**
- Phase 2 cutover (certSANs, endpoint move, wg0 removal) until phase 1
  dogfooding passes the kill criteria.
- App SSO / any OIDC issuer.
- ENS commitment layer (+idea task; strictly additive, never auth-path).
