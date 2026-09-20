# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), each cutting its nebula path as it lands (decision `d3z3`).
**P2.0–P2.3 are live** (Mac on `irohup -tun`; workload plane on
`<svc>.gw.mesh.internal` through the in-cluster gateway with verified
device identity, ADR-0026). **P2.4 is all but done** (`359.9.4`): the
Android app is a member, and the owner's phone is enrolled and
verified on both path types — `*ip` on wifi, `*relay` on 5G, Jellyfin
served on the raw facet. Two things remain: **play actual media**
(`359.9.4.4` — Direct Play and sustained bitrate are the only
unproven acceptance items) and **migrate the TV** (`359.9.4.5`).
The TV is the gate: it unblocks cutting `jellyfin.cp1` (`vftt`), the
hub's dead `/hosts` + `/policy` (`ri3b`), and the `1gv` gate with
ingress-nginx's `hostNetwork` (`xnat`). Then P2.5 takes k8s off the
mesh.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024), per-request device identity at the gateway.

**Out of scope:**
- The three post-TV cleanups above: blocked on `359.9.4.5` by
  construction, not by effort. Don't start them early.
- Apps consuming `X-Mesh-*` (oauth2-proxy header mapping): possible,
  not planned; SIWE→OIDC stays the app-layer seam. The deeper fork is
  spike `i1il`, with `95la` deferred behind it.
- cp1 hostname pin (`t7b2`); relay gating (`5gz`); Parents'-TV
  (`4te`); HTTPS-on-mesh-names (`90a`, rescoped to the gateway).
