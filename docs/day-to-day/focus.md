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
its nebula cert. **Next is the policy compiler `359.8.5`** (design pins
first — see its note), which unblocks `Issuer#bundle` and `4un`; in
parallel `e8d` gives the hub its own iroh endpoint (a fly build-pipeline
change). Then `359.8.3` (cp1 agent) / `359.8.4` (irohup) consume the
kit, and `kql` tears the scratch relay down. Exit checks are
event-based (`359.8.6`).

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016)
and **Sovereign-actor protocol at the center** — the hub actors are
the protocol's first real inboxes, and `actor.Hold` is the first
protocol change driven by a consumer's lifecycle.

**Out of scope:**
- Phase 2 consumer moves (talosctl/kubectl bridges in anger, gateway,
  Android app swap, k8s off the mesh) until Phase 1's exit checks pass.
- Relay access gating (`5gz`) before a member cert exists to gate on.
- Parents'-TV deployment (`4te`); storage work until w1 returns.
