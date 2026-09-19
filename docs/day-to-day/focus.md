# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 1 — identity plane beside nebula** (`359.8`,
decision `b2t`). Dual plane: nebula untouched, the iroh plane grows
next to it. **The hub is a working set of protocol actors and is
reachable by key**: Issuer (`359.8.1`), relay child (`359.8.2.2`),
Enroll → `Issuer#mint-device` (`359.8.2.3` part 1), the policy compiler
(`359.8.5`) and `Issuer#bundle` (part 2) are built, and since
2026-09-18 (`e8d`) the **hubkey is the hub's iroh `EndpointId`** — one
inbox on two wires, deployed from a nix-built image, answering
envelopes from the outside world through its own relay; since
2026-09-19 `#bundle` is complete (`359.8.2.3` closed) — grants,
blocklist, `speak-as` and a **witnessed name map** (decision `2fc`).
**Now: the first real member.** `359.8.3` (cp1 agent: NodeId on iroh,
enroll, beat, persist Kit + name map, `policy.AcceptTable(KindNode)`)
and `359.8.4` (irohup) consume the Kit and fill the hub's name map
with their beats; `kql` tears the scratch relay down; exit checks are
event-based (`359.8.6`).

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016)
and **Sovereign-actor protocol at the center** — the hub actors are
the protocol's first real inboxes, and `actor.Multi` is the second
protocol change driven by a consumer's lifecycle (after `actor.Hold`).

**Out of scope:**
- Phase 2 consumer moves (talosctl/kubectl bridges in anger, gateway,
  Android app swap, k8s off the mesh) until Phase 1's exit checks pass.
- Relay access gating (`5gz`) before a member cert exists to gate on.
- Parents'-TV deployment (`4te`); storage work until w1 returns.
