# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 4 — deletion (`359.11`).** Phase 3's soak
closed 2026-09-20 with all three exit events covered (w1 reboot, hub
re-seal by forced restart — decision `iwrk`, remote-media from stable
cellular). Nebula is installed but carries nothing; every path that
matters is on the identity plane. Phase 4 removes it: P4.1 factory
schematic without the nebula extension + node upgrades, P4.2 code
deletion (`neb*.go`, `nebderive`, `nebstack`, `cmd/nebup`, vendored
nebula pkg, AAR/mobile nebula parts), P4.3 ADR promotions/revisions,
P4.4 desired-state + deployed-state docs.

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
