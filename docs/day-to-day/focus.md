# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 1** — dual overlay (uuid fca5be68). The hub
side is complete and verified running: derivation (`nebderive`) →
netstack (`nebstack`) → config (`nebconf`) → overlay DNS (`nebdns`) →
lifecycle (`nebseal`, one unseal for both overlays). Next is the **node
side**: factory schematic + nebula extension via `talosctl upgrade`, then
compose-time cert injection — the gap that currently makes mesh names
resolve to addresses nobody answers on. Then `nebup` enrollment, then
≥1 week dogfood measuring direct-vs-relay rate and throughput (kill
criteria 2–4 still armed).

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — direct peer
paths, phones/TV on the network, one derivation tree. ADR-0002 Accepted.

**Out of scope:**
- Phase 2 cutover (certSANs, endpoint move, wg0 removal) until phase 1
  dogfooding passes the kill criteria. Invariant 5's two-overlay
  exception ends there, so a stalled phase 2 is a real cost.
- App SSO / any OIDC issuer.
- ENS commitment layer (+idea task; strictly additive, never auth-path).
