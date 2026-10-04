# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Both big goals are reached and their epics sit at P2 — **Mesh
v3** (2026-09-21; no nebula anywhere, three-node fleet since 09-22) and
**storage tiers** (ADR-0029 Accepted 2026-10-04; library mirrored on
nas1's bulk tier, app state on the fenced `nvme` default class,
`/status` watches Longhorn). The board was groomed 2026-10-04; the
open set is field items, follow-ups, and the owner's next-apps list.

**Toward goal:** "Every exposed service authenticates against the
wallet" and "Provisioning plane stays minimal" (`goals.md`) are what
the new spikes push on: `9z4e` (HTTPS over the mesh from protocol
identities, not a parallel CA), `i1il`/`95la` (app sign-in from mesh
identity), `dsuj` (a fourth node — Windows PC with GPU — through the
same one-signature path).

**Next candidates** (owner to pick):
- Apps on the storage tiers: `hwtp` seerr, then `4iob` retire the
  docker host. `dsuj` waits only on the data copy off the Windows PC.
- Spikes: `dsuj` Windows node + GPU, `ch74` SMB/sync, `9z4e` HTTPS,
  `kanr` agentic workloads.
- Disk-secret hygiene: `spvd` (slot-0 KMS unlock proof), `9af0` (ship
  the nodeagent fix).
- Protocol v0 M5 money (`0bc.5`) — state in `protocol/docs/day-to-day/`.

**Out of scope:** KMS onto 443 (`os8s`); the daemon's control socket
(`fgr`); iroh-ffi read-cancellation (`vh6e`) — `vzbf` is closed.
