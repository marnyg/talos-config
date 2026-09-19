# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), and since 2026-09-19 each migration **cuts its nebula path
as it lands** (decision `d3z3`) — Phase 4 is whatever never moved.
**P2.0 and P2.1 are live**: the Mac runs `irohup -tun` as a launchd
daemon, and talosconfig / kubeconfig / `nix run .#apply` all go by
name over it — hub included. **P2.2 is built, awaiting deploy**
(`359.9.2`, commits `4814be3` + `49a7bdb`): auto-bootstrap dials the
control plane's `apid` facet as an ordinary caller; the hub's
netstack dial path is gone from `bootstrap.go`. Its prerequisite —
how the hub re-learns members after its own restart — is settled by
decision `z2go`: members re-beat on transport evidence (their pooled
connection to the hub dying; a caller carrying a newer `speak-as`),
and poll a *sealed* hub flat, so the hub's name map is complete one
`MinRebeat` after the unseal. Deploy order: hub, then `p0agent` 0.1.3
on both nodes, then the Mac. Then **P2.3** (`359.9.3`), the gateway.

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
