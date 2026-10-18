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

## Loose threads

- **Workouts is not redeployed:** `workouts/build.sh` + pin bump in
  `k8s/apps/workouts/deployment.yaml` are still to do; until then the
  PWA runs the old shell (sw.js v2).
- Plates form ambiguity: the profile form posts `weight_unit` + `plates`
  together and `plates` is read in the *submitted* unit, so flipping the
  dropdown without retyping re-labels the numbers (25 kg → 25 lb). A
  hidden "unit the values were rendered in" field would remove it.
- Warm-up generator rough edges: runs twice ⇒ two ramps (no dedupe);
  bar hard-coded 20 kg; picker is a `prompt()`.
- `ivof.4`–`ivof.8` are still `in_progress` in beads although the parent
  is live (2026-10-18 handoff); `9g6m` likewise done-but-open. Close them.
- Carried: `d0o0` (durable `talos/pins` copy), `tmm5`, `88uq` phone
  subscription, ntfy first page.

## Suggested next steps

- Redeploy workouts and try supersets + a warm-up ramp on the phone.
- `bsj` (Longhorn backup target) — unchanged from last handoff.
- `d0o0` remainder solo (hub trust root; not swarm material).
