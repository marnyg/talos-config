# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), and since 2026-09-19 each migration **cuts its nebula path
as it lands** (decision `d3z3`) — Phase 4 is whatever never moved.
**P2.0 and P2.1 are live**: the Mac runs `irohup -tun` as a launchd
daemon, and talosconfig / kubeconfig / `nix run .#apply` all go by
name over it — hub included (`hub.mesh.internal`, hub-http facet).
The overlay `/config` route is gone. **The fleet prerequisite is
done** (`qb5q`): both nodes run the same p0agent installer, so
`w1.mesh.internal` and `cp1.mesh.internal` both dial directly. Next
is **P2.2** (`359.9.2`): hub→node dials (`/status`, bootstrap probes)
onto identity streams — now able to cover both nodes.

**In the way:** `ipt7` — a hub redeploy strands running daemons for
up to 6 h (stale hubkey). P2.2 puts the hub on the dialing side, so
this gets worse before it gets better; worth fixing first.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction, hub as actors
(ADR-0024). The admin plane is now that fiction end to end.

**Out of scope:**
- Phase 3/4 as a separate pass: nebula removal now rides each
  consumer's migration; only never-migrated consumers remain for it.
- Linux desktop presentation; mobile's adoption of `fakeip` (`phz`).
- The daemon's local control socket (`fgr`) until a consumer needs
  more than the log and `talos-mesh-enroll`.
- cp1's hostname pin (`t7b2`) until the reinstall `bsj` prepares for.
- Relay access gating (`5gz`); Parents'-TV deployment (`4te`).
