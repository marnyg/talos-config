# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Both big goals are reached and their epics sit at P2 — **Mesh
v3** (2026-09-21; no nebula anywhere, three-node fleet since 09-22) and
**storage tiers** (ADR-0029/0031; library at one replica on nas1's
bulk tier, user files on `longhorn-user` across two nodes, app state
on the fenced `nvme` default class, `/status` watches Longhorn). The board was groomed 2026-10-04; the
open set is field items, follow-ups, and the owner's next-apps list.
ADR-0030 (hub serves `talos/` from the signed git tip) is live since
2026-10-04 and proven end-to-end 2026-10-05 (push→served in 6 s with
a nudge); `talos/` edits no longer need a deploy. Disk encryption's
slot 0 is proven live at boot and the fleet runs p0agent 0.1.6
(2026-10-04): no secret derives from a MAC any more.

**Toward goal:** "Every exposed service authenticates against the
wallet" and "Provisioning plane stays minimal" (`goals.md`) are what
the new spikes push on: `9z4e` (HTTPS over the mesh from protocol
identities, not a parallel CA), `i1il`/`95la` (app sign-in from mesh
identity), `dsuj` (a fourth node — Windows PC with GPU — through the
same one-signature path).

**Next candidates** (owner to pick):
- Apps: the docker host is retired (`4iob` closed); `dsuj` waits only
  on the data copy off the Windows PC.
- Spikes: `dsuj` Windows node + GPU, `ch74` SMB/sync, `9z4e` HTTPS,
  `kanr` agentic workloads. (`r4fw` closed → ADR-0030.)
- ~~Disk-secret hygiene: `spvd`, `9af0`~~ — done 2026-10-04;
  ~~`k8sd`~~ decided 2026-10-05 (ADR-0031).
- Protocol v0 M5 money (`0bc.5`) — state in `protocol/docs/day-to-day/`.
- ~~Field bugs `rnfk`, `bh74`~~ — closed 2026-10-04, verified on the phone.

**Out of scope:** KMS onto 443 (`os8s`); the daemon's control socket
(`fgr`); iroh-ffi read-cancellation (`vh6e`) — `vzbf` is closed.
