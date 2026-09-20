# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 3 — soak (`359.10`), 2/3 events.** Phase 2
is complete: P2.5 (2026-09-20) put the k8s endpoint on cp1's declared
LAN address, so nothing in steady-state cluster membership touches any
overlay. Nebula is installed but carries nothing that matters. Exit is
on event coverage, not calendar: ~~one node reboot~~ (w1, 2026-09-20 —
two P2.5 defects found and fixed), ~~one hub re-seal~~ (forced restart
2026-09-20, counted by decision `iwrk`), one full remote-media session
(first attempt void: cellular bearer churn; `rnfk` fixed and on the
phone since) — then Phase 4 (`359.11`) deletes nebula.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024), per-request device identity at the gateway (ADR-0026).

**Out of scope:**
- Wallet-native app sign-in (`95la` behind spike `i1il`); the TV's
  admin session; the Tailscale-vs-mesh VPN-slot conflict on the TV.
- The daemon's control socket (`fgr`); cp1 hostname pin (`t7b2`);
  relay gating (`5gz`); Parents'-TV (`4te`); `bh74`.
- Phase 4 deletions (`359.11`) — nebula code stays until the soak.
