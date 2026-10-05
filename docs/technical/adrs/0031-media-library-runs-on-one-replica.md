# ADR-0031: The media library runs on one replica

- Status: Accepted (2026-10-05)
- Date: 2026-10-05
- Amends: ADR-0029 (replica count only; the bulk-tier placement and
  the share-manager pin stand). Restores ADR-0011's replica count on
  the tier ADR-0029 chose.
- Related: invariant 2's corollary (no disk is exempt from a wipe),
  task `k8sd`, bug `cnb5`, backup target `bsj`.

## Context and Problem Statement

ADR-0029 (yesterday) mirrored the library across nas1's two SATA bays:
two replicas, hard disk anti-affinity, "RAID-1 without mdraid". Its
driver was that losing one Toshiba should not mean a re-download.

The first day showed the cost. Both replicas of every `longhorn-bulk`
volume live on one node, so a nas1 reboot fails both at once; Longhorn
salvages one and copies the live head to rebuild the other — ~600 GB at
~12 MB/s, hours, during which the volume is `degraded`. A reboot
mid-rebuild on 2026-10-04 threw away 101 GB of progress. The `tv`
snapshot purge (420 GB of coalesce on the same spindle) starved the
`movies` sync to ~0.5 MB/s. `fast-replica-rebuild` cannot help: there
are no checksummed snapshots to skip, and adding a recurring snapshot
means paying the purge cost on a schedule instead (`k8sd`).

The redundancy bought with that is against one bay failing. The
library is re-downloadable (ADR-0011; invariant 2's corollary); a
bay failure means a re-download of what that bay held, not a loss.

## Considered Options

- **A — keep two replicas, add a recurring checksummed snapshot** so
  rebuilds copy only the delta. Periodic purge/coalesce I/O on the
  bulk spindles; the complexity stays.
- **B — one replica on the bulk tier.** No rebuild on reboot (a single
  replica just comes back), 8 TB usable. One bay dying = a
  re-download and a `faulted` volume until a new replica is
  scheduled on the other bay.
- **C — accept** hours of degraded tier per reboot as the tier price.

## Decision Outcome

Chosen: **B** for the library (`media/tv`, `media/movies`,
`media/downloads`). `longhorn-bulk` goes to `numberOfReplicas: "1"`;
the three live Volume CRs are patched to match, which drops one
replica each without a rebuild.

**Exception: `files/transfer` stays at 2**, hand-patched on its
Volume CR. It is the only copy of the Windows PC's data once that PC
becomes a node (`dsuj`) — the irreplaceable tenant on a disposable
tier that ADR-0029's confirmation clause already named. Two replicas
is the stopgap; the right answer is a backup target (`bsj`).

### Consequences

- The class's anti-affinity parameters only matter for a volume
  patched above 1 replica; they are kept for `transfer` and for any
  future opt-in.
- A nas1 reboot brings the library back as soon as the node is up; no
  `degraded` window, no "do not reboot mid-rebuild" rule. `k8sd`
  closes as decided.
- ADR-0029's confirmation ("one Toshiba failing leaves the library
  online") no longer holds for the library; it holds for `transfer`.
- Class parameters are immutable: the class is deleted once the
  commit is ArgoCD's target revision, and recreated by sync.

### Confirmation

Right if: a nas1 reboot returns all four bulk volumes to `healthy`
without a rebuild. Invalidated if the owner stops treating the library
as re-downloadable (then `bsj` first, then replicas), or a second bulk
node arrives (ADR-0029's Option C).
