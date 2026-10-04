# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (third session): TV updated, two hygiene items closed, hub
redeployed.

- **TV on the current APK** (`750f575`) over network adb from this box
  — membership kept, `beat ok`, no `198.18.0.1` advertised (`bh74`
  visible on the TV). Phone and TV now both carry `rnfk`/`bh74`/`5q33`.
- **`4zpf` closed** (`f0c0433`): `android/build.sh` is the one-shot
  cross-lib → AAR → APK build with `--publish` and repeatable
  `--install <serial>`; it leaves `app-debug.apk.sha` beside the APK
  and `publish.sh` puts *that* commit in the release notes (not the
  publisher's HEAD). Build stage not exercised end-to-end yet — the
  next real APK build is the test.
- **`359.9.4.3` closed** (`b410783`, `925dc9d`): the netstack RSTs a
  TCP SYN at `198.18.0.2` (resolver) or `198.18.0.1` (tun) before any
  flow exists — Android's DoT `:853` probe is refused at once, no log
  line, no `FlowErrors`. `fakeip.Stack.Stats` (`TCPRefused`,
  `UDPDropped`) is exported through `meshtun.Tun.Stack` and shows on
  the app's Debug JSON as `tcpRefused`/`udpDropped`. Gate green.
- **Hub redeployed** `925dc9d` (was `3896837`), unsealed by the owner;
  TV renewed against the new hubkey `ed:60669a3c…` within a minute.
  Carries `7a3b21e` (nudgeGap floor) and the fakeip changes (no effect
  on the hub itself — it has no tun).

## Loose threads

- **Longhorn bulk tier on nas1**: still check
  `kubectl get volumes.longhorn.io -n longhorn-system` shows every
  attached volume `healthy` before any nas1 reboot (`k8sd`).
- **Clients still to rebuild** for the RST/stats change: phone + TV
  APK (`android/build.sh --publish --install …`), Mac `talos-mesh`
  daemon, gateway image. Cosmetic — ride the next natural release.
- Undecided broken windows from this session (fix / file / ignore):
  `publish.sh` fallback note shape (sha + prose), `build-aar.sh`
  header still names the by-hand path, `notes.md` TV-adb entry vs the
  README's replace-while-running quirk, `scripts/test-iroh.sh` runs
  `go test` without `-timeout` (a hung test holds the gate 10 min).
- `jlgz` (v6-only hub answer on cellular) stays open; re-check on the
  next cellular remote-media session.
- Unchanged: no `fly ssh` key on the nixos box; the darwin laptop
  lacks the signing `git config`; `dsuj` waits on the Windows data copy.

## Suggested next steps

- Owner's pick from the board: spikes `dsuj` / `ch74` / `9z4e` /
  `kanr` / `i1il`, or `k8sd`.
- Small: decide the four broken windows above; `pymm`/`d4p8` test
  flakes.
