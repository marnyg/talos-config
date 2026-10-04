# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (second session): **both field bugs closed and verified on
the phone** (`rnfk`, `bh74`).

- **`bh74`** (`0cf9178`): `irohtransport.NotDialable` (RFC 2544
  `198.18.0.0/15`) joins unspecified in `dialable()`, so `Endpoints()`
  never tags a host's own fake-IP tun address whatever iroh
  enumerated — one chokepoint for Android, `irohup -tun`, and the
  provisioner on the laptop. `mobile.advertiseLocal` skips
  `fakeip.IsFake` too. New vendorHashes for `config-server-bin` and
  `actors-bin` (`750f575`). Not covered: iroh's in-band disco still
  carries the raw interface list (a peer may *try* `198.18.0.1:p` and
  hit its own tun's netstack, which drops non-DNS UDP — harmless).
- **`rnfk`**: code was in (`d23dd6d`); the missing step was the client
  rebuild. AAR + APK built on this box and published to
  `android-latest @ 750f575`; installed on the phone over adb.
  Measured with `adb shell svc wifi disable/enable`: Wi-Fi→cellular
  3 s to `beat ok` (was ~3.5 min), cellular→Wi-Fi 8 s (was 28 s).
  Phone's Debug endpoints: relay + Wi-Fi + cellular + two v6, no
  `198.18.0.1`.
- The published APK also carries the enrollmsg v3 client (`5q33`:
  phone/TV done; gateway image and Mac daemon still stale).

## Loose threads

- **Longhorn is still rebuilding nas1's bulk tier**: two bulk volumes
  (400 GB, 900 GB) `degraded` at session end. Do not reboot nas1 until
  `kubectl get volumes.longhorn.io -n longhorn-system` shows every
  attached volume `healthy` (`k8sd` has the recurring-cost decision).
- **The TV still runs the 09-20 APK** — sideload `android-latest` on
  it when convenient (it picks up `rnfk`/`bh74`/`5q33` in one go).
- The hub still runs `3896837`; `7a3b21e` (`nudgeGap`) and `0cf9178`
  (`Endpoints()` filter — irrelevant to the hub, it has no tun) wait on
  a deploy. The Mac `talos-mesh` daemon and the provisioner binary
  pick up `bh74` on their next build.
- `jlgz` (v6-only hub answer on cellular → tcp4 for the hub fetch?)
  stays open; re-check on the next cellular remote-media session.
- Unchanged: kmsprobe UUID warning on `/status` until the next hub
  restart; no `fly ssh` key on the nixos box; the darwin laptop lacks
  the signing `git config`; `dsuj` waits on the Windows data copy.

## Suggested next steps

- Owner's pick from the board: spikes `dsuj` / `ch74` / `9z4e` /
  `kanr` / `i1il`, or `k8sd`.
- Hygiene-shaped: `4zpf` (the APK build is three hand-run steps —
  `/tmp/apk-build.sh` from this session is the one-shot wrapper, worth
  committing as `android/build.sh`), `359.9.4.3` (DoT `:853` probe
  noise on every network change, seen again today).
