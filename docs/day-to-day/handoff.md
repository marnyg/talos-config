# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-14 (evening, one sitting) — **ADR-0012 first slice landed.**
Status flipped to Accepted (v1 signed-message prefix pinned in the ADR,
thread `72d38fd0` closed). Implementation in one push:

- `mesh/nebseal.go`: `Device` type, `ParseDevices`, `Manager.devices`
  and `Manager.Device()` deleted; `NewManager` signature shrank.
- `mesh/nebdevice.go`: rewritten as `Manager.EnrollDevice(params)` —
  device-supplied pubkey, git-zone collision check, `pki.key` empty on
  the wire.
- `mesh/nebdns.go`: `buildMeshZone` no longer takes devices; live
  devices resolve via `liveDeviceZone` layered over the static zone
  (peer cert name + derived address match).
- `mesh/nebhttp.go`: `/config` gate is per-request, cert-based —
  `admins` group AND `cert addr == DeviceIP(master, cert.Name)`.
- `mesh/nebseal.go` `Members()`: hub + machines from git; devices only
  while live.
- `nebenroll.go` rewritten as the verify+mint core. `nebtv.go` deleted
  (media-only asymmetry gone). New endpoints:
  `POST /mesh/enroll/challenge`, `POST /mesh/enroll` (nebup direct),
  `POST /mesh/enroll/device` (RFC 8628 start),
  `POST /mesh/enroll/approve` (/status form target),
  `GET /mesh/enroll/config` (bearer token → minted config).
- `deviceflow`: `KindTV` → `KindMeshEnroll`, added `Payload` on `Auth`
  and `grant` plus `ApproveWithPayload` / `MeshEnrollPayload` /
  `UpdateIdentity`.
- `status.go`: pending-approval loop renders a separate mesh-enroll
  card with editable name, group radio, admins-name re-type, live-
  rebuilt v1 message; admin-retype **also** checked server-side.
- `walletsign`: `MeshEnroll(...)` replaces `Enroll(...)`; challenge
  carries `Fingerprint` now.
- `cmd/nebup`: two-file cache (`<name>.key` + `<name>.yml`), `-group`
  (default admins), `-rekey`, `-reenroll` refreshes the yml only.
- `fly.toml`: `MESH_DEVICES` / `MESH_MEDIA_DEVICES` removed.
- `CLAUDE.md` device-groups + nebup sections updated.

All package tests green (`go test ./...` in `config-server/`).

## Loose threads

- **Not deployed yet.** Nothing has run against the real hub. First
  smoke steps: unseal on staging → `nebup -rekey` from the laptop →
  enroll → tunnel up → `curl` /config over the mesh → confirm the
  admins gate accepts the fresh cert. If those pass, deploy prod +
  re-enroll the real laptop.
- **90-day renewal automation** (`49443c38`) — device key persists
  across enrollments, so a "re-sign same pubkey" cron/CLI is doable.
  Not started; still `+later`.
- **w1 still down** (since 2026-08-04) — 3 `longhorn-bulk` volumes
  `faulted`; VM system/ISO volumes run `degraded` 1-of-2. Unblocks the
  emptyDir→Longhorn storage arc when it returns.
- Phone enrollment accepted-broken until the APK exists (unchanged).

## Suggested next steps

- Deploy the config-server bundle to fly and smoke-test the whole
  enrollment path end-to-end with the real wallet.
- Once verified, re-enroll `laptop` from nebup (`-rekey`, since the
  device key is now device-born; old wg0-era derived cert is gone).
- Power-cycle w1; verify `longhorn-bulk` volumes leave `faulted` and
  the storage arc unblocks.
