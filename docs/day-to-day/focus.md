# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), each cutting its nebula path as it lands (decision `d3z3`).
**P2.0–P2.3 are live** (Mac on `irohup -tun`; workload plane on
`<svc>.gw.mesh.internal` through the in-cluster gateway with verified
device identity, ADR-0026). **P2.4 is half-landed** (`359.9.4`): the
Android app is a member — `nodeagent` + the shared presentation
(`meshtun`) on a `VpnService`, nebula deleted from it, APK v2 built —
but **no device has been enrolled on it yet**, so the TV still reaches
Jellyfin over nebula and `jellyfin.cp1` stays in k8s until it does.
Sideload → enroll → verify LAN-direct and relay is the whole remaining
step; then P2.5 takes k8s off the mesh.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024), per-request device identity at the gateway. This session
made the fiction one implementation across desktop and phone rather
than two copies of a dialect.

**Out of scope:**
- Removing ingress-nginx's hostNetwork / the `1gv` interim gate, and
  the hub's `/hosts` + `/policy` (the app was their last consumer):
  all wait on the TV actually running the new APK.
- Apps consuming `X-Mesh-*` (oauth2-proxy header mapping): possible,
  not planned; SIWE→OIDC stays the app-layer seam.
- The daemon's control socket (`fgr`); mobile `fakeip` (`phz`); cp1
  hostname pin (`t7b2`); relay gating (`5gz`); Parents'-TV (`4te`).
