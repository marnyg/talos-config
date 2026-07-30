# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-30 (afternoon) — **All three phase-1 exit checks passed**, the
TV question got decided, and the dashboard grew mesh eyes.

- **Exit checks 1–3 ✓** (details in `mesh-v2-nebula.md`): node reboot
  (same mesh address through a reboot *and* a LAN lease drift), hub
  re-seal (twice — reconverges unaided ~60s after unseal), roaming
  (fixed-line → cellular, no manual action, relay parity ~71–82ms).
- **Criterion 4's TV case dropped** (decision 3dfef644): official
  Mobile Nebula is unusable on Android TV (mobile_nebula#148 — Flutter
  ignores d-pad), so the home TV goes LAN-direct; a thin gomobile TV
  APK is filed as task 2e1bef85 for if a remote-TV need ever appears.
  The phone half of criterion 4 remains the *only* phase-2 gate.
- **TV device flow deployed** (`f2cc4b7` shipped with the first
  re-seal); `/mesh/tv` live.
- **/status upgrades** (`5d85161`): soft refresh (10s poller swaps a
  `#live` region, backs off while typing — replaces meta-refresh) and a
  Mesh table: full derived membership joined with the live hostmap
  (tunnel state, WAN endpoint, relaying-via-hub). Watched the roaming
  check happen on it in real time.

## Loose threads

- **Criterion 4, phone half**: measure Mobile Nebula phone UX (app
  imports externally-derived keys — verified in source) or formally
  drop driver 2. This is the last gate before phase 2.
- **Office MacBook holds the `laptop` identity** (same key + overlay
  address as the home laptop). Decide: leave / blocklist / re-enroll
  under a distinct name. Undecided since morning.
- **Thread 35 (TV onboarding, dba0c63d)** has nothing left in it after
  the deploy + TV decision — close it, or keep until the phone half
  resolves.
- cp1's LAN lease now drifts freely (.20→.30→.31 in one day); every
  LAN-side `talosctl` needs a scan first. Phase 2 step 2 (endpoint →
  mesh IP) retires this — thread 24 has the full context.

## Suggested next steps

- Settle criterion 4's phone half (enroll a phone once, or drop driver
  2 by decision) — then phase 2 is unblocked.
- Phase 2 step 1 (certSANs: nebula name + IP) is additive and safe to
  start regardless.
- Decide the office-mac credential question.
