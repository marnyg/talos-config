# Workouts — domain model

<!-- Sub-scope of the root docs (docs/desired-state/). Scope: the
     self-hosted workout tracker (epic talos-config-yz75), a data-plane
     service under invariant 2's exception: its state is payload on a
     Longhorn volume, not control-plane state. -->

A single-user training log: **what was done** (workouts of sets),
**what is planned** (templates), **what can be done** (the exercise
library), and **what the body measures** (metrics). Everything the
app shows beyond that — records, rep-max tables, charts, "previous
set" prefill — is a **derivation** over the log and is never stored.

```
Folder ─┬─< Template ─< TemplateExercise ─< TemplateSet
        │                   │
        │                   ▼ (origin, nullable)
        └─ ─ ─ ─ ─ ─ ─   Workout ─< WorkoutExercise ─< Set
                                        │
                        Exercise ◄──────┘        Metric ─< MetricEntry
                          └─< ExerciseMedia
```

## Glossary

- **Exercise** — a named movement with a fixed *shape of a set* (its
  `cell types`), a body part, an equipment category, ordered
  instruction steps and media. 253 come from Strong's global library
  (`origin = strong`, keep their ids), the rest are the user's. An
  exercise is never deleted while a Set references it; it is
  **archived** instead (hidden from pickers, still in history).
- **Cell type** — one field a set of this exercise records. Closed set,
  verbatim from Strong so the import is lossless:
  `BARBELL_WEIGHT | DUMBBELL_WEIGHT | OTHER_WEIGHT |
  WEIGHTED_BODYWEIGHT | ASSISTED_BODYWEIGHT | REPS | RPE | DURATION |
  DISTANCE`. The weight flavours differ only in how a plate calculator
  and "(-KG)" labelling treat them; storage is one `weight_kg` column.
  `ASSISTED_BODYWEIGHT` stores the assistance as a **positive** kg and
  the UI shows the sign.
- **Category** — the equipment class the exercise picker filters on
  (`barbell | dumbbell | machine_other | bodyweight |
  assisted_bodyweight | reps_only | duration | cardio`). Derived from
  the cell types once at import, stored because the user may override
  it on custom exercises.
- **Body part** — one of `arms back cardio chest core full-body legs
  olympic other shoulders` (Strong's tag ids). One per exercise.
- **Workout** — a performed session: name, `started_at`, `ended_at`
  (null while in progress), notes, optional **origin template**. At
  most **one workout is in progress** at a time.
- **WorkoutExercise** — an exercise's slot in a workout, ordered by
  `position`; carries the exercise note, the rest timer (seconds, null
  = app default) and an optional `superset` group number. Strong's
  `cellSetGroup`.
- **Set** — one row under a WorkoutExercise: `position`, `kind`
  (`normal | warmup | drop | failure`), the cell values
  (`weight_kg reps rpe duration_s distance_m`, each nullable), and
  `completed`. A set that is not completed when the workout ends is
  **dropped**, not stored (Strong's "handle incomplete sets" default).
- **Template** — a plan: name, folder, ordered **TemplateExercise**
  (exercise, note, rest timer, superset) each with ordered
  **TemplateSet** (kind + target cell values). Starting a template
  copies it into a Workout whose sets are prefilled from the template
  values, with "previous" shown from the last Workout of the same
  exercise. Finishing a workout whose origin is a template may offer
  to write the performed sets back into it.
- **Folder** — a named, ordered group of templates. `My Templates` is
  the default; "Example Templates" from Strong is imported too.
- **Metric** — a body measurement series: `weight_kg body_fat_pct
  calories` plus circumferences (`neck shoulders chest left_bicep
  right_bicep left_forearm right_forearm upper_abs waist lower_abs
  hips left_thigh right_thigh left_calf right_calf`). Closed set in v1.
- **MetricEntry** — `(metric, at, value)`.
- **Record** (derived) — per exercise: best estimated 1RM, max weight,
  max volume (weight × reps for one set), and the 1–12 rep-max table
  (best actual set at exactly n reps, plus the estimate from the best
  1RM). Estimated 1RM uses Brzycki (`w × 36 / (37 − reps)`), which
  reproduces Strong's displayed numbers (60×8 → 74).
- **PR** (derived) — a set whose est. 1RM / weight / volume beats
  every earlier set of that exercise; counted per workout in history.
- **Setting** — key/value: units (`kg`/`lb`, `km`/`mi`, `cm`/`in`),
  first weekday, default rest timer, available bars/plates, xRM
  formula. Single user, so no owner column anywhere. `weight_unit`
  is applied at render time only (every stored weight is kg, including
  the `plates` list, which is entered in kg whatever the display unit
  — `63fi`); the plate calculator reads `plates` and the bar off the
  live set row.

## Identity and origin

- Ids are UUIDs (text). Imported rows keep Strong's ids so a re-import
  is idempotent; locally created rows mint v4.
- `origin` on exercises, templates, folders and workouts is
  `strong | local`; nothing else keys off it.

## Invariants (this scope)

1. A Set belongs to exactly one WorkoutExercise, which belongs to
   exactly one Workout; positions are unique within their parent.
2. Exercises referenced by any Set or TemplateSet are archived, never
   deleted (FK `RESTRICT`).
3. At most one Workout has `ended_at IS NULL`.
4. Records, PRs, "previous set" and charts are computed from Sets at
   read time; there is no table for them (caches may appear later but
   are safe-to-lose).
5. Weight is stored in kilograms, distance in metres, duration in
   seconds; units are a display setting.
6. Media files live beside the database on the volume
   (`<data>/media/<file>`), referenced by filename; the database never
   holds bytes. Strong's media and instruction text are licensed
   content: they are seeded onto the volume by the operator and never
   committed to this (public) repo.
