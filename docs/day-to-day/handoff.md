# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (fifteenth session, late evening) — **soak 2/3: hub
re-seal covered; `rnfk` fixed and on the phone; hub redeployed.**

- **Hub redeployed** `585524e → 1173f0d` (`HUB_BUILDER=mar@nixos
  fly/deploy.sh`), so the served w1 config carries the dongle MAC.
  Unsealed 19:23:18Z; cp1/w1 `beat ok` on the new hub id within 5 s,
  `etcd-running` seen 10 s later. The phone (tunnel up at the time)
  did `lost → renewed → beat ok` inside one second.
- **Hub re-seal event** (forced: `fly machine restart`, counted as
  soak 2/3 — decision `iwrk`): sealed 19:34:20Z, both nodes noticed in
  ~30 s (h2 pings), unsealed 19:35:19Z, `beat ok` at +29 s (w1) /
  +38 s (cp1), hub back to idle 19:36:24Z. No hands, no nebula.
- **`rnfk` fixed** (`d23dd6d`, `2c2f607`): `Agent.NetworkChanged()` =
  `CloseIdleConnections` + `Kick`, called from
  `mobile.Tunnel.NetworkChanged`; `hubTransport`/`hubClient` moved to
  untagged `nodeagent/hubtransport.go` with a test pinning that
  `CloseIdleConnections` reaches x/net's h2 transport (net/http
  #22891); `EnrollDevice`'s fallback client now shares it. APK built
  on `mar@nixos` (~30 s, everything cached) and `adb install -r`'d
  on the phone (`XQ-BQ52`, 21:32 local) — **tunnel not reconnected
  yet** (VpnService is not exported; must be tapped in the app).
- Longhorn: `win2k25`'s data volume `healthy`; `win2k25-system`
  (28 GB) rebuild was at 92 % at wrap-up, moving ~1 %/45 s.

## Loose threads

- **Phone tunnel down** since the APK install; tap Connect. Then the
  next Wi-Fi→cellular switch should `beat ok` in seconds, not 3.5 min.
- `talos-config-jlgz` (thread): hub fetch over v6-capable cellular hit
  a v6-only answer through the tun's `::/0` — re-check on the cellular
  session; `tcp4` dial is the cheap fix if it recurs.
- Unchanged from last session: `hyjv` (`-n w1` via cp1 hangs; use
  `-n 10.0.0.71`), TV admin session, Mac daemon on the pre-`d4960c1`
  binary (`darwin-rebuild switch` pending).

## Suggested next steps

- **Soak 3/3**: one full remote-media session from real, stable
  cellular with the new APK (watch `cache/mesh.log` via
  `adb shell run-as dev.marnyg.mesh tail cache/mesh.log`). Then
  Phase 4 (`359.11`).
- Confirm `win2k25-system` reached `healthy`
  (`kubectl get volumes.longhorn.io -n longhorn-system`).
