# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-16 — **Android app DNS debugged end-to-end on the owner's
phone; mesh access policy extracted to data** (sketch `6462fed4`
phase 1).

- App debug surface (`5104858`…`e60e9a3`): shim counters + event ring
  (`Tunnel.DebugJSON`), nebula log to file, in-app debug screen with
  arbitrary-name lookup, tunnel-start errors surfaced on-screen.
- The debug screen immediately caught a real regression: the split-DNS
  shim commits (`692b42f`/`323abf5`) were never device-tested and
  lacked `ACCESS_NETWORK_STATE` — tunnel start died with
  SecurityException on every device. Fixed (`d79fa93`); DNS (mesh,
  scoped and underlay names) now verified working on the phone.
- The remaining "Jellyfin timeout" was by-design firewall: media-group
  devices reach nodes on tcp/30096 only. Working URL for app/TV
  clients: `http://jellyfin.cp1.mesh.internal:30096` (local login —
  SSO fronts only the admin-only :80 ingress). This settles the
  Jellyfin-addressing question for the parents' TV.
- Policy design session (groups → ACL discussion) → sketch `6462fed4`:
  git-resident `mesh-policy.yaml` + ephemeral hub overlay + epoch'd
  live sync + unseal reconciliation. **Phase 1 landed** (`019ce97`):
  the who×what table now lives in `talos/mesh-policy.yaml`, all three
  render sites derive from it, tests guard the real file. ADR-0014
  drafted (Proposed).

## Loose threads

- **Hub on fly still runs the pre-policy-refactor image.** Renders are
  identical by construction; rides the next deploy (+ unseal).
- **Worker 6443 question** (task filed): node policy admits no
  machines-group traffic, yet worker kubelets point at cp1's mesh
  address — how do workers actually join? Untestable while w1 is down;
  check when it's power-cycled, *before* the storage arc leans on it.
- **Parents' TV still not deployed** — the original build-gate. All
  known blockers now cleared (APK debuggable, DNS works, Jellyfin URL
  proven on the owner's phone).
- Phone is enrolled as group `media`; the enroll screen hardcodes it.
  If the owner wants the :80 SSO ingress from the phone, re-enroll as
  `admins` (or add a group picker to the app).
- 90-day device renewal automation (`49443c38`) owed before
  ~2026-11-12.

## Suggested next steps

- Deploy the hub (picks up the policy refactor; needs a wallet unseal)
  and re-verify enrollment + `/hosts` after.
- Ship the APK to the parents' TV with the proven
  `jellyfin.cp1.mesh.internal:30096` recipe; watch relay throughput.
- Phase 2 of `6462fed4`: hub overlay + wallet-gated policy UI
  (effective-vs-git diff, export-to-commit).
- Power-cycle w1 → answers the worker-6443 question and unblocks the
  storage arc.
