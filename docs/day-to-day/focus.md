# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), and since 2026-09-19 each migration **cuts its nebula path
as it lands** (decision `d3z3`) — Phase 4 is whatever never moved.
**P2.0, P2.1 and P2.2 are live**: the Mac runs `irohup -tun` as a
launchd daemon, talosconfig / kubeconfig / `nix run .#apply` go by
name over it, and the hub's auto-bootstrap dials the control plane's
`apid` facet as an ordinary caller (`359.9.2`, closed 2026-09-20:
42 s from unseal to `etcd-running` after a redeploy, both nodes on
the `z2go` agent). The hub's netstack dial path is gone from
`bootstrap.go`. Next is **P2.3** (`359.9.3`): the in-cluster gateway
pod that terminates identity streams and forwards to Services with a
verified device-identity header — Jackett first, then the rest of
ingress.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024). The admin plane and the hub→node plane are now that
fiction end to end; the workload plane is what P2.3 starts.

**Out of scope:**
- Phase 3/4 as a separate pass: nebula removal now rides each
  consumer's migration; only never-migrated consumers remain for it.
- Linux desktop presentation; mobile's adoption of `fakeip` (`phz`).
- The daemon's local control socket (`fgr`) until a consumer needs
  more than the log and `talos-mesh-enroll`.
- cp1's hostname pin (`t7b2`) until the reinstall `bsj` prepares for.
- Relay access gating (`5gz`); Parents'-TV deployment (`4te`).
