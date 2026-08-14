# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** ADR-0012 wallet-signed enrollment is deployed and
owner-verified (2026-08-14); the mesh-v2 enrollment arc is at its
deliberate stopping point. Pivot to the storage arc: blocked on
physically reviving w1, so the interim code work is the etcd
advertised-subnets bug (`6c456522`).

**Toward goal:** the finished work closes out "Mesh v2 — phones/TV
join the network" in `desired-state/goals.md` (phones: verified via
stock Mobile Nebula, no client code). The storage pivot serves
Longhorn adoption — currently only a task-tracker goal (`25d30c3b`),
not yet in `goals.md`.

**Out of scope:**
- TV/phone client code — stock Mobile Nebula verified working; revisit
  only if the TV needs a leanback UI (`2e1bef85`).
- 90-day renewal automation (`49443c38`) — owed before ~2026-11-12,
  not now.
- Any storage migration while `longhorn-bulk` is faulted.
