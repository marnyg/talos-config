# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 1** — dual overlay (uuid fca5be68). Done:
baked-in decisions settled (CIDR `10.42.0.0/16`, zone `mesh.internal`,
cert V2) and `nebderive` landed — wallet→HKDF CA/machine/device/hub
identities, golden-tested against stock `nebula-cert` 1.10.3.
Next: hub embedding (`nebula/service` variant with UDP Listen,
lighthouse+relay from derived state, wgdns ported to the nebula
netstack), then the node side (factory schematic + extension,
compose-time cert injection), then `nebup` + ≥1 week dogfood measuring
direct-vs-relay rate and throughput (kill criteria 2–4 still armed).

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — the spike gate
passed 2026-07-29; ADR-0002 is Accepted.

**Out of scope:**
- Phase 2 cutover (certSANs, endpoint move, wg0 removal) until phase 1
  dogfooding passes the kill criteria.
- App SSO / any OIDC issuer.
- ENS commitment layer (+idea task; strictly additive, never auth-path).
