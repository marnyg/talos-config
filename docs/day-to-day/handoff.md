# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (fourth session): Longhorn checked, every client but the
Mac daemon rebuilt.

- **Longhorn**: 15/17 volumes healthy; `media/movies` and `media/tv`
  `degraded` *by progress*, not failure — their second replica is
  being built onto `longhorn-2` (the bulk-2 bay). The gate is a
  420 GB **snapshot purge** on `tv`'s healthy replica (14 % at 16:00Z,
  ~55 MB/s read+write on `sda`, 89 % busy), which starves the `movies`
  local sync (~0.5 MB/s, reported as 0 %). nas1 had rebooted at
  13:55Z mid-rebuild and lost 101 GB of copied replica. Noted on
  `k8sd`. **Do not reboot nas1 until both are `healthy`.**
- **APK `3f47eff`** built with `android/build.sh --publish` — first
  end-to-end run of the build stage (the `4zpf` open question) — and
  installed on the phone (`QV7802S09E`, USB) and the TV (`10.0.0.2`).
  Both kept membership and `beat ok` against hubkey `ed:60669a3c…`;
  the phone's `:853 not a name we minted` lines are gone (`359.9.4.3`
  visible in the field).
- **Gateway image `3f47eff`** pushed and pinned (`458ad40`); ArgoCD
  rolled it (needed a `refresh=hard` nudge to pick the commit up
  inside its poll interval), new pod on cp1 `beat ok`, admitting
  `ingress-http`. `5q33` now has only the Mac `talos-mesh` daemon
  left.

## Loose threads

- **Longhorn bulk tier**: re-check
  `kubectl get volumes.longhorn.io -n longhorn-system` later today;
  expect `tv` purge → `tv` rebuild (~690 GB) → `movies` rebuild to
  take several hours in total. `k8sd` has the design question.
- **Mac daemon** (`5q33`): `darwin-rebuild switch` on the laptop,
  which also still lacks the signing `git config`.
- Undecided broken windows carried over: `publish.sh` fallback note
  shape, `build-aar.sh` header still describing the by-hand path,
  `scripts/test-iroh.sh` runs `go test` without `-timeout`.
- `jlgz` (v6-only hub answer on cellular) stays open; re-check on the
  next cellular remote-media session.
- Unchanged: no `fly ssh` key on the nixos box; `dsuj` waits on the
  Windows data copy.

## Suggested next steps

- Owner's pick from the board: spikes `dsuj` / `ch74` / `9z4e` /
  `kanr` / `i1il`, or decide `k8sd` (recurring snapshot on the bulk
  class vs. single replica vs. accept).
- Small: the three broken windows above; `pymm`/`d4p8` test flakes.
