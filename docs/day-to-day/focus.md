# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 is reached** (2026-09-21, Phase 4 closed) and the
fleet is **three nodes** since 2026-09-22: nas1, the storage node,
provisioned end-to-end through the declared path (MAC-selected config,
one wallet approval, key minted on the machine) with no step done by
hand. No nebula anywhere; the identity plane is the only plane. The
epic `talos-config-359` stays open only for field items that are not
the goal: parents' TV (`4te`, ADR-0013's gate), stale `enrollmsg` v2
binaries (`5q33`), relay access gating (`5gz`), `bh74`.

**Next candidates** (owner to pick):
- **Fill nas1's four SATA bays** — live `UserVolumeConfig` per disk;
  the reason the node exists, and it makes the HA sweep affordable.
- **HA sweep** (`9l67`) once networking is settled — w1 off takes
  every `*.gw` service down with the gateway's RWO volume. Three
  nodes make a real replica spread possible for the first time.
- **Sovereign-actor protocol v0** (`0bc`, M1–M5) — M1–M3 built;
  **M4 spawn: designed (ADR-0008/0009 Proposed), M4.1 + M4.2 built
  2026-09-23** (`protocol/spawn`, `protocol/provisioner`); **drivers
  built 2026-09-25** in the new `actors/` module (`0bc.4.3/.4`
  closed), **binaries + image recipe the same day** (`0bc.4.5`);
  next: push the image, then the acceptance run `0bc.4.6`; talos
  wire unchanged throughout. State and history
  live in `protocol/docs/day-to-day/`. The talos Provisioner's split
  along ADR-0009 is thread `kckm`, not v0.
- Small ops: cp1 hostname pin (`t7b2`), w1's provisioning MAC
  (`c4vd`), SA-issuer runbook (`etzl`).

**Out of scope:** wallet-native app sign-in (`95la` behind spike
`i1il`); KMS onto 443 (`os8s`); the daemon's control socket (`fgr`).
