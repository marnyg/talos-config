# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-21, autonomous-leaves swarm, round 2)

Five small beads closed, four of them by a herdr swarm (reports under
`/tmp/swarm/<name>.md`, cited from the bead notes), all merged `--no-ff`:

- `s5ok` (inline): `config-server/relay.go` comments name the third relay
  mode, `observing (…)`.
- `b35t` → `a-outfit-pins`: `k8s/apps/sandbox/build.sh` no longer `exec`s
  ghcr-push; it prints the **three** pin lines to paste (agent.yaml,
  sap-provisioner `-outfit`, sap-parent `-image`). The k8s driver gained
  `Config.Log` and **warns** on a `#spawn` whose image has no outfit while
  outfits are configured (the silent-keyless case). `nix build
  .#actors-bin` green.
- `ne6e` → `w-supersets`: the live screen (`/log`) does supersets (slot
  menu "Superset with…", SS<n> tag + colour band, rest timer only after
  the group's last slot) and a **warm-up ramp generator** (bar×10, 50 %×5,
  70 %×3, 90 %×1 of the first working weight, rounded to 2×smallest plate,
  bar 20 kg; `Workouts.warmupRamp` pure). New `store.InsertWarmups`
  (insert at front, positions shifted in one tx) and
  `POST /api/exercise-slots/{id}/warmups`. `slotSave` now echoes
  `superset` — it used to NULL template-copied supersets on every
  note/rest save. sw.js `v3`.
- `63fi` → `w-plates-unit`: `plates` is entered/shown in the display unit,
  converted to kg at `/settings` (lb → 3 dp), per-unit default list when
  unset. Storage stays kg; `data-plates`/app.js untouched.
- `xrfm` → `q-pin-draw`: `gen` no longer draws `PIN_DEAD` or a
  caller-carried pin (genNear's faults keep them; mutants p3/p4 still
  die). Depth-2 verify 337 → 322 s solver on an M4 Pro — **not enough**,
  so `check.sh`'s Depths paragraph now states a **6-minute** authorize
  budget honestly.

**Wave 3 + solo follow-ups, same day:**

- `bhui` → `k-kubevirt-infra`: `kubevirt-cr.yaml` gets `spec.infra.nodePlacement`
  (match-all required term + preferred control-plane) so virt-api/
  controller/exportproxy can land on workers during a cp1 drain.
  **Not confirmed live** — recipe in `/tmp/swarm/k-kubevirt-infra.md`.
- `6t21` → `m-prowlarr`: Prowlarr seeds thepiratebay/knaben/iptorrents
  from git (schema-template awk; IPT cookie+UA from the moved
  `iptorrents` SealedSecret), sonarr/radarr lose the Jackett Torznab
  loops and env, `k8s/apps/jackett` + Ingress + gatus + app-of-apps
  entry deleted. **Gate is yours**: IPT results through Prowlarr —
  recipe in `/tmp/swarm/m-prowlarr.md` §Confirm-live (sync prowlarr
  first; then delete the orphaned `Jackett (…)` indexers in the *arr UIs).
- Solo: `tho0` (ramp replaces instead of stacking, bar from the plate
  dialog remembered in localStorage, superset picker is a dialog),
  `bwhy` (hidden `plates_unit` field), blank-row `<template>` per slot
  (no more `location.reload()`), `TestPlatesUnit` split out, check.sh
  Depths history as a table. Workouts image `sha256:92c83f…` pinned.

## 2026-10-22 confirm-live pass

- `6t21` **closed**: the seed loop had skipped `knaben`/`iptorrents` —
  Prowlarr's native C# indexers carry their class name in
  `definitionName` (`IPTorrents`, `Knaben`), Cardigann ids are lowercase.
  Case-insensitive match (226ddfd); after a restart all three added, IPT
  search returned 50 results via Prowlarr, pushed into sonarr+radarr, and
  the six orphaned `Jackett (…)` entries deleted via API.
- `bhui` **closed**: operator rolled out nodePlacement — each infra
  deployment has one replica on cp1 + one on a worker, PDBs at 1.
  Drain itself not exercised. Stale Completed/Error pods cleared.
- `m0vb` **closed**: radarr NZBgeek block is PUT-or-POST like sonarr
  (5289111); live refresh of id 2 confirmed. Seen once on that restart:
  `WARN: Prowlarr Radarr refresh failed` — Prowlarr tests the app by
  calling Radarr back before Radarr is fully up; a manual PUT 202'd.
  Transient, not tracked.
- Workouts `sha256:92c83f…` confirmed running.

## Loose threads

- Workouts image `sha256:92c83f…` is live; first open on the phone
  should pull sw.js v4.
- Broken windows fixed the same session (63b4ad4): agent.yaml pins the
  bare `@digest`; k8s driver refuses non-digest outfit keys at `New`;
  `settingKeys` hoisted; `POST /api/exercise-slots/{id}` is a partial
  update (`store.SlotPatch`).
- `hp79` (terminal client of the parent's stream) is unblocked but needs
  the CLI-auth design first (OIDC cookie vs a member-cert facet) —
  grill-design, not a worker. `jlgz` left alone per its own note.
- Carried: `d0o0` (durable `talos/pins` copy), `tmm5`, `88uq` phone
  subscription, ntfy first page.

## Suggested next steps

- Try supersets + a warm-up ramp on the phone (`tmm5` offline check too).
- `bsj` (Longhorn backup target) — unchanged from last handoff.
- `d0o0` remainder solo (hub trust root; not swarm material).
