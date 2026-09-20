# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), each cutting its nebula path as it lands (decision `d3z3`).
**P2.0–P2.3 are live**: the Mac runs `irohup -tun`, admin CLI and the
hub's auto-bootstrap go by name, and since 2026-09-20 the **workload
plane** does too — the in-cluster gateway `gw` terminates
`ingress-http`, every service is `<svc>.gw.mesh.internal` with the
verified device identity in `X-Mesh-*` and SSO on `auth.gw`
(ADR-0026). The Mac has no nebula plane left. Next is **P2.4**
(`359.9.4`): the Android/TV app on iroh + fake-IP, sharing the zone
rule; `jellyfin.cp1` and the nodes' nebula extension are what remain
of the old plane, then P2.5 takes k8s off the mesh.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024), per-request device identity at the gateway — the last of
these landed this session.

**Out of scope:**
- Removing ingress-nginx's hostNetwork / the `1gv` interim gate until
  the TV is off nebula (P2.4).
- Apps consuming `X-Mesh-*` (oauth2-proxy header mapping): possible,
  not planned; SIWE→OIDC stays the app-layer seam.
- The daemon's control socket (`fgr`); mobile `fakeip` (`phz`); cp1
  hostname pin (`t7b2`); relay gating (`5gz`); Parents'-TV (`4te`).
