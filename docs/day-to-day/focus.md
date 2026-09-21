# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 is reached** (2026-09-21, Phase 4 closed). No
nebula anywhere; the identity plane is the only plane; ADRs and the
desired-state/deployed-state docs describe what runs. The epic
`talos-config-359` stays open only for field items that are not the
goal: parents' TV (`4te`, ADR-0013's gate), stale `enrollmsg` v2
binaries (`5q33`), relay access gating (`5gz`), `bh74`.

**Next candidates** (owner to pick):
- **HA sweep** (`9l67`) once networking is settled — w1 off takes
  every `*.gw` service down with the gateway's RWO volume.
- **Sovereign-actor protocol v0** (`0bc`, M1–M5) — picked up
  2026-09-22: **M3 built** (`0bc.3`, protocol ADR-0007: lighthouse as
  a plain actor, PoW postage at the frontdoor; talos wire unchanged).
  M4 spawn (`0bc.4`) next. See `protocol/docs/day-to-day/`.
- Small ops: cp1 hostname pin (`t7b2`), w1's provisioning MAC
  (`c4vd`), SA-issuer runbook (`etzl`).

**Out of scope:** wallet-native app sign-in (`95la` behind spike
`i1il`); KMS onto 443 (`os8s`); the daemon's control socket (`fgr`).
