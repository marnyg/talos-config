# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — the consumers are migrated; collect the
debt.** **P2.0–P2.4 are live** (Mac on `irohup -tun`; workload plane
on `<svc>.gw.mesh.internal` with verified device identity, ADR-0026;
phone and TV both members on the same APK). The TV was the last thing
holding nebula's scaffolding up, so the next unit of work is
demolition, not new capability: `vftt` (`jellyfin.cp1`), `ri3b` (the
hub's `/hosts` + `/policy`), `xnat` (the `1gv` gate and
ingress-nginx's hostNetwork). Then **P2.5** (`359.9.5`) takes the
k8s/Talos endpoint off the mesh — the only step that touches cluster
availability.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024), per-request device identity at the gateway. Every device
the household actually uses now reaches Jellyfin that way.

**Out of scope:**
- Making app sign-in wallet-native (`95la` behind spike `i1il`): both
  devices are in on Quick Connect / the local account instead, and
  the TV holds an *admin* session because of it.
- The Tailscale-vs-mesh one-VPN-slot conflict on the TV, and its
  foreign `jellyfin.pytt.io` server — owner chose to leave both.
- The daemon's control socket (`fgr`); cp1 hostname pin (`t7b2`);
  relay gating (`5gz`); Parents'-TV (`4te`); `bh74`'s fake-range
  address filter.
