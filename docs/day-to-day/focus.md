# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 4 — deletion (`359.11`).** P4.1 done
2026-09-20: the nebula extension is off both nodes (fleet image
`p0agent-0.1.5`), the apiserver cert carries no overlay address, and
the served config's `<name>.mesh.internal` SAN comes from the
identity-plane render. What remains: P4.2 code deletion (`neb*.go`,
`nebderive`, `nebstack`, `nebenroll.go`, `cmd/nebup`, vendored nebula
pkg, AAR/mobile nebula parts, hub `--mesh-*` flags), P4.3 ADR
promotions/revisions, P4.4 desired-state + deployed-state docs.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024), per-request device identity at the gateway (ADR-0026).
Phase 4 is what lets the goal read "reached".

**Out of scope:**
- Wallet-native app sign-in (`95la` behind spike `i1il`); the TV's
  admin session; the Tailscale-vs-mesh VPN-slot conflict on the TV.
- The daemon's control socket (`fgr`); cp1 hostname pin (`t7b2`);
  relay gating (`5gz`); Parents'-TV (`4te`); `bh74`; `jlgz` unless it
  recurs.
