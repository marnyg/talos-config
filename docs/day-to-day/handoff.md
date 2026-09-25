# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-25 (second) — **Protocol only: M4.5 built** (`b93bfcc`) —
`actors/cmd/{child,provisioner}` on iroh (behind build tag `iroh`,
config-server's pattern), the child's beat as C-free `actors/child`,
and the image recipe (`actors-image` → `ghcr.io/marnyg/sap-actors`,
`actors/build.sh`). `actors/` joined the pre-push vendored list and
CI's `vendor-hash` matrix; AGENTS.md layout + quality-gate notes
updated. Nothing under `talos/`, `config-server/`, `protocol/` or
`k8s/` moved; `nix build .#actors-bin` green (the tagged suite);
other vendorHashes unchanged. Detail in
`protocol/docs/day-to-day/handoff.md`.

## Previous sessions

2026-09-25 — **`udof` decided, M4.3/M4.4 drivers built** (`121f8e3`,
`e316081`, `2b5c29d`) in the new `actors/` module; restart re-adopts
leases from platform labels (decision `uzgl`).

2026-09-23 (two sessions) — **Protocol only: M4.1 `protocol/spawn`
and M4.2 `protocol/provisioner` built** (`25e7dd0`, `2dafea6`); the
protocol half of M4 complete.

2026-09-22 — **nas1 provisioned (node three, the storage node)**
(`72f9038`) and protocol regression `ydq0` fixed (`32ef88c`); hub
`4518c2f` deployed. Details in `technical/deployed-state.md`.

## Loose threads

- **nas1 and w1 were powered off** (owner, 2026-09-25) — that is the
  `NotReady` / `SchedulingDisabled`; nas1 was being booted back up
  during the session. The acceptance run (`0bc.4.6`) needs a
  schedulable worker.
- **The actors image is not pushed yet** (`HUB_BUILDER=mar@nixos
  actors/build.sh`; needs the box + GHCR token).
- `-tags iroh` tests run only inside the nix build; `scripts/test-
  iroh.sh` is the gate for the hub, `nix build .#actors-bin` for
  `actors/cmd/` (AGENTS.md).
- nas1's four SATA bays are empty; Longhorn `replicaCount: 2` with
  three nodes — undecided on purpose.
- Carried: w1 off since 2026-09-21 (`9l67`); clients on `enrollmsg`
  v2 (`5q33`); TV not measured (`4te`); `-n cp1` by name fails from
  the tun (`t7b2`); `c4vd`, `etzl`.

## Suggested next steps

- Push the actors image; then `0bc.4.6` (parent CLI, provisioner
  Deployment on the cluster + a docker host) — see the protocol
  handoff. Confirm nas1 is `Ready` and uncordoned first.
- Populate nas1's SATA bays; decide the Longhorn replica count.
