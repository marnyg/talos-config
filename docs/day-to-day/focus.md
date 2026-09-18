# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 1 — identity plane beside nebula** (`359.8`,
decision `b2t`). Dual plane: nebula untouched, the iroh plane grows
next to it. **The hub is now a working set of protocol actors**: the
Issuer (`359.8.1`), the relay child (`359.8.2.2`, deployed 2026-09-17,
ADR-0022 Accepted) and Enroll → `Issuer#mint-device` with the v2
enrollment message (`359.8.2.3` part 1) are built; a device that names
its NodeId gets a member Kit from the same wallet signature that mints
its nebula cert. **The policy compiler exists** (`config-server/policy`,
2026-09-18; protocol ADR-0004 Accepted, `talos/mesh-policy-v3.yaml`
real, `4un` round-trip law green) **and `Issuer#bundle` signs its
output** (`359.8.2.3` part 2, 2026-09-18): a verified member's beat
returns hubkey-signed grants + the v3 blocklist + the speak-as. **Now:
`e8d`** — the hub's own iroh endpoint (a fly build-pipeline change:
cgo + `libiroh_ffi`), so the Issuer's inbox is reachable from a real
member and `#bundle`'s name map has a location cache to read. Then
`359.8.3` (cp1 agent, consuming `policy.AcceptTable`, running the
beat) / `359.8.4` (irohup) consume the kit, and `kql` tears the
scratch relay down. Exit checks are event-based (`359.8.6`).

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016)
and **Sovereign-actor protocol at the center** — the hub actors are
the protocol's first real inboxes, and `actor.Hold` is the first
protocol change driven by a consumer's lifecycle.

**Out of scope:**
- Phase 2 consumer moves (talosctl/kubectl bridges in anger, gateway,
  Android app swap, k8s off the mesh) until Phase 1's exit checks pass.
- Relay access gating (`5gz`) before a member cert exists to gate on.
- Parents'-TV deployment (`4te`); storage work until w1 returns.
