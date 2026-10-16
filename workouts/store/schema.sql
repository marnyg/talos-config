-- Workouts schema (workouts/docs/desired-state/domain-model.md).
-- Applied once by store.Open; versioned by user_version.
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS setting (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS exercise (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  body_part    TEXT NOT NULL,            -- arms|back|cardio|chest|core|full-body|legs|olympic|other|shoulders
  category     TEXT NOT NULL,            -- barbell|dumbbell|machine_other|bodyweight|assisted_bodyweight|reps_only|duration|cardio
  cell_types   TEXT NOT NULL,            -- JSON array of cell type names, in display order
  instructions TEXT NOT NULL DEFAULT '[]', -- JSON array of steps
  origin       TEXT NOT NULL DEFAULT 'local', -- strong|local
  archived     INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS exercise_name ON exercise(name COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS exercise_media (
  exercise_id  TEXT NOT NULL REFERENCES exercise(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,            -- video|image|thumb
  file         TEXT NOT NULL,            -- basename under <data>/media
  content_type TEXT NOT NULL,
  PRIMARY KEY (exercise_id, kind)
);

CREATE TABLE IF NOT EXISTS folder (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  position   INTEGER NOT NULL DEFAULT 0,
  origin     TEXT NOT NULL DEFAULT 'local',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS template (
  id         TEXT PRIMARY KEY,
  folder_id  TEXT REFERENCES folder(id) ON DELETE SET NULL,
  name       TEXT NOT NULL,
  notes      TEXT NOT NULL DEFAULT '',
  position   INTEGER NOT NULL DEFAULT 0,
  origin     TEXT NOT NULL DEFAULT 'local',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS template_exercise (
  id          TEXT PRIMARY KEY,
  template_id TEXT NOT NULL REFERENCES template(id) ON DELETE CASCADE,
  exercise_id TEXT NOT NULL REFERENCES exercise(id) ON DELETE RESTRICT,
  position    INTEGER NOT NULL,
  note        TEXT NOT NULL DEFAULT '',
  rest_s      INTEGER,                   -- NULL = default rest timer
  superset    INTEGER,                   -- NULL = none; equal numbers are one superset
  UNIQUE (template_id, position)
);

CREATE TABLE IF NOT EXISTS template_set (
  id                   TEXT PRIMARY KEY,
  template_exercise_id TEXT NOT NULL REFERENCES template_exercise(id) ON DELETE CASCADE,
  position             INTEGER NOT NULL,
  kind                 TEXT NOT NULL DEFAULT 'normal', -- normal|warmup|drop|failure
  weight_kg            REAL,
  reps                 INTEGER,
  rpe                  REAL,
  duration_s           REAL,
  distance_m           REAL,
  UNIQUE (template_exercise_id, position)
);

CREATE TABLE IF NOT EXISTS workout (
  id          TEXT PRIMARY KEY,
  template_id TEXT REFERENCES template(id) ON DELETE SET NULL,
  name        TEXT NOT NULL,
  notes       TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL,             -- RFC 3339 UTC
  ended_at    TEXT,                      -- NULL while in progress
  timezone    TEXT NOT NULL DEFAULT 'UTC',
  origin      TEXT NOT NULL DEFAULT 'local',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS workout_started ON workout(started_at DESC);
-- invariant 3: at most one workout in progress
-- (NULLs are distinct to a unique index, so index the predicate itself)
CREATE UNIQUE INDEX IF NOT EXISTS workout_in_progress ON workout((ended_at IS NULL)) WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS workout_exercise (
  id          TEXT PRIMARY KEY,
  workout_id  TEXT NOT NULL REFERENCES workout(id) ON DELETE CASCADE,
  exercise_id TEXT NOT NULL REFERENCES exercise(id) ON DELETE RESTRICT,
  position    INTEGER NOT NULL,
  note        TEXT NOT NULL DEFAULT '',
  rest_s      INTEGER,
  superset    INTEGER,
  UNIQUE (workout_id, position)
);
CREATE INDEX IF NOT EXISTS workout_exercise_by_exercise ON workout_exercise(exercise_id);

CREATE TABLE IF NOT EXISTS "set" (
  id                  TEXT PRIMARY KEY,
  workout_exercise_id TEXT NOT NULL REFERENCES workout_exercise(id) ON DELETE CASCADE,
  position            INTEGER NOT NULL,
  kind                TEXT NOT NULL DEFAULT 'normal',
  weight_kg           REAL,
  reps                INTEGER,
  rpe                 REAL,
  duration_s          REAL,
  distance_m          REAL,
  completed           INTEGER NOT NULL DEFAULT 0,
  completed_at        TEXT,
  UNIQUE (workout_exercise_id, position)
);

CREATE TABLE IF NOT EXISTS metric_entry (
  id     TEXT PRIMARY KEY,
  metric TEXT NOT NULL,                  -- weight_kg|body_fat_pct|calories|neck|...
  at     TEXT NOT NULL,
  value  REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS metric_entry_series ON metric_entry(metric, at DESC);

-- Read model for records/history: one row per completed set with its
-- exercise and workout time. Views cost nothing to keep and spare the
-- Go side a join it would otherwise repeat in every query.
CREATE VIEW IF NOT EXISTS set_done AS
  SELECT s.id, s.kind, s.weight_kg, s.reps, s.rpe, s.duration_s, s.distance_m,
         s.position, we.id AS workout_exercise_id, we.exercise_id,
         w.id AS workout_id, w.name AS workout_name, w.started_at,
         CASE WHEN s.weight_kg IS NOT NULL AND s.reps IS NOT NULL AND s.reps > 0 AND s.reps < 37
              THEN s.weight_kg * 36.0 / (37.0 - s.reps) END AS est_1rm,
         CASE WHEN s.weight_kg IS NOT NULL AND s.reps IS NOT NULL
              THEN s.weight_kg * s.reps END AS volume
  FROM "set" s
  JOIN workout_exercise we ON we.id = s.workout_exercise_id
  JOIN workout w ON w.id = we.workout_id
  WHERE s.completed = 1 AND w.ended_at IS NOT NULL AND s.kind <> 'warmup';
