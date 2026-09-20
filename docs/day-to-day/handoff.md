# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (sixteenth session, night) — **soak 3/3 covered; Phase 3
closed (`359.10`). No code changed.**

- **Remote-media event** from real, stable cellular (Telenor LTE,
  netId 155, bearer up since 19:06Z — none of the churn that voided the
  first attempt). Driven from the Mac over USB adb: `svc wifi disable`
  at 19:48:15Z → `advertising 10.202.121.221` and `beat ok` at
  19:48:16Z — **1 s** (was ~3.5 min; `rnfk` verified, the APK from
  `2c2f607` is on the phone and the tunnel is connected). Gateway
  `admitted "phone" [media] → jellyfin` in 321 ms; main stream
  52.7 MB over 5m54s, seek responsive, ~69 MB through `tun0`,
  playback smooth per Marius, zero reconnects. `svc wifi enable` at
  19:56:39Z → `beat ok` at :49 — **8 s** (was 28 s).
- `jlgz` (v6-only hub answer on cellular) did **not** reproduce
  during the session; noted on the issue, still a thread.
- Longhorn: every attached volume `healthy` (`win2k25-system` rebuild
  finished).
- Phase 4 (`359.11`) deliberately **not started** — fresh session.

## Loose threads

- `hyjv` (`-n w1` via cp1 hangs; use `-n 10.0.0.71`) — resolves
  itself when P4.1 drops `nebula0` from advertised addresses.
- `c4vd` (w1's `talos/machines/<mac>/` dir is the dock's MAC, not the
  dongle's) — must be handled before any w1 wipe, and P4.1 upgrades
  nodes; check whether `talosctl upgrade` re-fetches by MAC.
- `etzl` (endpoint change rotates SA issuer) — not on Phase 4's path
  unless the endpoint moves again.
- TV admin session; Mac daemon on the pre-`d4960c1` binary
  (`darwin-rebuild switch` pending).

## Suggested next steps

- **Start Phase 4** (`bd show talos-config-359.11`). Order sketched
  last session: P4.2 code deletion first (reversible, no node
  touches) — but `359.11.2` depends on `359.11.1` in beads and its
  notes carry two must-carry items (`certSANs <name>.<zone>` injection
  moves out of `nebmachine.go`; `cluster.yaml` loses the `10.42.218.125`
  SAN and `nodeport-addresses`). Read those notes before choosing.
- P4.1 (`359.11.1`): factory schematic without the nebula extension,
  `talosctl upgrade` all nodes; do it with Marius at the keyboard.
