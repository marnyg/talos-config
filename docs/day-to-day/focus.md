# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — last step: P2.5 (`359.9.5`), the
k8s/Talos endpoint off the mesh.** P2.0–P2.4 are live and the
scaffolding they held up is gone (`vftt`, `ri3b`, `xnat`, 2026-09-20):
no `*.cp1` names in k8s, no nebula-side hub HTTP beyond a hello,
ingress-nginx reachable only through the gateway. What is left on
nebula is the apiServer certSAN / kubelet path (`cp1.mesh.internal`,
invariant 4's stated exception) — P2.5 moves it to declared LAN
addresses, then Phase 3 (`359.10`) soaks with nebula idle.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024), per-request device identity at the gateway (ADR-0026).

**Out of scope:**
- Wallet-native app sign-in (`95la` behind spike `i1il`); the TV's
  admin session; the Tailscale-vs-mesh VPN-slot conflict on the TV.
- The daemon's control socket (`fgr`); cp1 hostname pin (`t7b2`);
  relay gating (`5gz`); Parents'-TV (`4te`); `bh74`.
- Phase 4 deletions (`359.11`) — nebula code stays until the soak.
