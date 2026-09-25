# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-25 — **Protocol + a new module: `udof` decided, M4.3/M4.4
drivers built** (`121f8e3`, `e316081`, `2b5c29d`). A provisioner
restart now re-adopts leases from platform labels instead of
persisting (decision `uzgl`, ADR-0009 amended); the k8s and docker
drivers live in **`actors/`**, a new C-free Go module beside
`iroh-transport/` (in CI's `go` matrix; AGENTS.md layout updated).
Both were verified against the live platforms — a throwaway Job in
a `sap-probe` namespace (deleted) and Docker Desktop. Nothing under
`talos/`, `config-server/` or `k8s/` moved; `scripts/test-iroh.sh`
green; vendorHashes unchanged. Detail in
`protocol/docs/day-to-day/handoff.md`.

## Previous sessions

2026-09-23 (two sessions) — **Protocol only: M4.1 `protocol/spawn`
and M4.2 `protocol/provisioner` built** (`25e7dd0`, `2dafea6`); the
protocol half of M4 complete.

2026-09-22 — **nas1 provisioned (node three, the storage node)**
(`72f9038`) and protocol regression `ydq0` fixed (`32ef88c`); hub
`4518c2f` deployed. Details in `technical/deployed-state.md`.

## Loose threads

- **nas1 is `NotReady,SchedulingDisabled` and w1 `NotReady`** as of
  2026-09-25 10:00Z (seen while probing the Jobs API; not
  investigated). nas1 was `Ready` on 2026-09-22 and nothing in git
  cordoned it. Only cp1 is serving. Look before assuming anything
  about the cluster.
- `-tags iroh` tests run only inside the nix build; `scripts/test-
  iroh.sh` is the gate (AGENTS.md). `actors/` is C-free and not
  covered by it — nor yet by the pre-push vendored-tree list.
- nas1's four SATA bays are empty; Longhorn `replicaCount: 2` with
  three nodes — undecided on purpose.
- Carried: w1 off since 2026-09-21 (`9l67`); clients on `enrollmsg`
  v2 (`5q33`); TV not measured (`4te`); `-n cp1` by name fails from
  the tun (`t7b2`); `c4vd`, `etzl`.

## Suggested next steps

- Find out why nas1 is cordoned/NotReady before anything else on the
  cluster — the acceptance run (`0bc.4.6`) needs a schedulable node.
- Protocol: `0bc.4.5` (child + provisioner binaries + image) — see
  the protocol handoff.
- Populate nas1's SATA bays; decide the Longhorn replica count.
