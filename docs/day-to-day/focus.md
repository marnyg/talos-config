# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), each step reversible, nebula still installed. **P2.0 is
live** (2026-09-19): the Mac runs `irohup -tun` as a launchd daemon —
`*.mesh.internal` names the name map knows resolve to `198.18/15`
fake IPs in a utun, one iroh stream per TCP flow. Next is `359.9.1`,
the admin CLI paths *on that* — talosconfig/kubeconfig endpoints by
name, the `/etc/hosts` workaround gone, `nix run .#apply` off the
nebula address (blocked on `359.8.2.4`, hub-http).

**Phase 1 closed 2026-09-19** (`359.8`): the identity plane exists
beside nebula and carries real traffic. Its remainder,
Provisioner-as-actor (`359.8.2`, ADR-0024), moved into Phase 2.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction. P2.0 is that
fiction on the desktop; P2.1 is the first consumer living in it.

**Out of scope:**
- Phase 3/4 (nebula removal) until every Phase 2 consumer has moved.
- Linux desktop presentation (fakeip is portable; the privileged setup
  and a systemd unit are not written) and mobile's adoption of
  `fakeip` (`phz`).
- The daemon's local control socket (`fgr` constraint) until a
  consumer needs more than the log and `talos-mesh-enroll`.
- Relay access gating (`5gz`); Parents'-TV deployment (`4te`); storage
  work until w1 returns.
