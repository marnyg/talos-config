# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-16 (evening, at home) — **Mesh v3 Phase 0 gate PASSED**
(decision `talos-config-b2t`; `359.1`, `359.1.2`, `359.1.5`, `5cz`
closed). Branch `spike/mesh-v3-p0.2` fast-forwarded into `main`.

- **P0.2 step 6, LAN-direct throughput: PASS.** Phone on the home Wi-Fi
  → box stand-in: **97.0 Mbps avg over 10.6 min, 154 peak, 127/127
  ticker samples on `*ip:`**, one session, zero redials. Steady minutes
  read 95.0–95.2 = the file's CBR — the player is the limiter. Punched
  through nixos-fw via conntrack; the iptables hole was never needed.
  Step 7 (cp1 extension 0.0.4) **skipped by owner ruling** — it only
  re-proves P0.3's forwarding and cluster Jellyfin has no media.
  Numbers in `docs/mesh-v3-p0.2-android.md §Progress log` and
  `docs/mesh-v3-iroh.md §P0.2`.
- **Gate rulings** (`mesh-v3-iroh.md §Phase 0` header, ADR-0016 status):
  `5cz` = **A**: `talos/hardware/minipc.yaml` now declares cp1's
  imager-built installer, digest-pinned
  (`ghcr.io/marnyg/talos-installer:v1.12.6-p0agent-0.0.3@sha256:6b4337…`);
  `talos/extensions/p0agent/build.sh` is the adopted chain (update tag
  **and** digest after every push). w1 stays on factory `6a9acc…`.
  Scratch relay stays until Phase 1.2 (`kql` retitled). `359.8.3`
  retitled: imager chain, not factory schematic.
- **Box scratch torn down**: `p0agent-standin` stopped (transient unit,
  gone), "P0 Test" library deleted, compose restored from the backup
  and `jellyfin` recreated with only its original mounts, `~/p0-jf`
  (5.4 GB) removed, `:7842` free. Kept: `mar@nixos:~/p0` (x86_64
  builder for `.#p0relay-static`; detached at `fa003f8`, working copy
  = `main` minus one comment), `~/Downloads/p0mesh-debug.apk` + the
  phone install (Phase 2.4's starting point).

## Loose threads

- **cp1's `ext-p0agent` dials the scratch fly relay on every boot**
  until Phase 1.2 embeds the relay in the hub (`kql`). Both ghcr
  packages must stay public (node pulls unauthenticated).
- `talos/talosconfig` endpoints still say `10.99.0.54` (wg0 era); cp1's
  LAN lease was `10.0.0.58` today — every `talosctl` needs `-e/-n`.
- **`~/disks/1TB-old` on the NixOS box is 100 % full** (Jellyfin's
  config/DB live there); jellyfin and plex share `./config/plex`;
  compose images unpinned `:latest`. Broken windows, ruling pending.
- The box's Jellyfin has a `/data/movies` mount but no Movies library
  (pre-existing, noticed during teardown; not ours).
- w1 still down since 2026-08-10: media volumes faulted, `0q0`, `kso`.

## Suggested next steps

- **Phase 1 (`359.8`) is unblocked.** `359.8.2`'s own text says define
  the hub's inbox message set + owned state **first** (decision `vl4`);
  `359.8.1` (membership issuance per ADR-0018) is the ready leaf. Groom
  the order before building — the protocol module's M3 (`0bc.3`) and
  `xwu` (verb = root consent's verb) are the same work seen from the
  protocol side.
- Phase-1 items carried from P0.2/P0.3 are in the "findings that shape
  Phase 1" lists under `mesh-v3-iroh.md §P0.2/§P0.3` — record as beads
  under `359.8` when grooming, don't lose them in prose.
