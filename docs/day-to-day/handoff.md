# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04: **disk-secret hygiene, both items done** (`spvd`, `9af0`).

- **Slot 0 (KMS) is live at boot** — ADR-0004's "dormant" was the
  first attempt only; the boot logs of all three nodes showed EPHEMERAL
  opening on KMS and Talos re-syncing slots, and the v1.12.6 source
  confirmed retry-on-all-fail (30 s ticker) + post-open `syncKeys`.
  Proven by re-keying: on every node STATE went `slot 1 rejected` →
  ~28 s → `opened … slot 0 *keys.KMSKeyHandler` → `updated encryption
  key slot 1`. All three `installMAC` fields are gone; every header now
  holds the UUID passphrase (ADR-0028's exit). ADR-0004 amended
  (Accepted), ADR-0028 consequences, deployed-state, domain-model,
  reinstall guide updated.
- **p0agent 0.1.6 on the fleet**: nodeagent at `7a3b21e` (carries
  `7fb6473`, expired speak-as → re-enroll). Installer
  `v1.12.6-p0agent-0.1.6@sha256:d9193308…` pinned in the three
  hardware files and `talosctl upgrade`d on nas1, w1, cp1 (the upgrade
  reboot doubled as re-key boot B).
- `flake.nix` apply: `APPLY_REKEY=1` opt-in for a deliberate
  `systemDiskEncryption` diff (guard otherwise unchanged).

## Loose threads

- **Longhorn is still rebuilding nas1's bulk tier** (three volumes,
  ~600 GB at ~12 MB/s — hours; `k8sd` filed). Do not reboot nas1 until
  `kubectl get volumes.longhorn.io -n longhorn-system` shows every
  attached volume `healthy`. Small nvme-tier rebuilds *onto* nas1 time
  out while that runs; deleting the stuck replica CR unsticks them.
- The `kmsprobe` runs left `00000000-dead-beef-…` and the node UUIDs in
  the hub's session-seal grace set — the probe UUID shows as a
  warning on `/status` until the next hub restart (documented
  behaviour).
- w1 and cp1 took the pending Longhorn label/annotation diff
  (`create-default-disk: config`, nvme default-disks-config) with boot
  A; Longhorn only reads those at first registration, so no effect.
- The hub still runs `3896837`; the `nudgeGap` change (`7a3b21e`) and
  nothing else waits on a deploy.
- Unchanged from last time: no `fly ssh` key on the nixos box; the
  darwin laptop lacks the signing `git config`; `dsuj` waits on the
  Windows data copy.

## Suggested next steps

- Owner's pick from the board: spikes `dsuj` / `ch74` / `9z4e` /
  `kanr`, or `k8sd` (bulk-tier rebuild cost: recurring snapshot vs
  one replica vs accept).
- Field bugs `rnfk` (phone handover) and `bh74` (fake-range addr) are
  the next hygiene-shaped items.
