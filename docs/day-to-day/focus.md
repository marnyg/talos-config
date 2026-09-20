# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 4 — deletion (`359.11`).** P4.1 (nodes) and
P4.2 (code + hub, 2026-09-21) are done: no nebula package, dependency,
flag, port or document anywhere; the hub runs the identity plane
alone. What remains is paper: P4.3 ADR promotions/revisions
(`359.11.3`), P4.4 desired-state + deployed-state docs (`359.11.4`).
Then Phase 4 — and the Mesh v3 goal — reads "reached".

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
