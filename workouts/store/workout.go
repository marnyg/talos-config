package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Set kinds.
const (
	KindNormal  = "normal"
	KindWarmup  = "warmup"
	KindDrop    = "drop"
	KindFailure = "failure"
)

// Set is one performed (or planned) row of an exercise.
type Set struct {
	ID          string
	Position    int
	Kind        string
	WeightKg    *float64
	Reps        *int
	RPE         *float64
	DurationS   *float64
	DistanceM   *float64
	Completed   bool
	CompletedAt *time.Time
}

// Volume is weight × reps, 0 when either is missing.
func (s Set) Volume() float64 {
	if s.WeightKg == nil || s.Reps == nil {
		return 0
	}
	return *s.WeightKg * float64(*s.Reps)
}

// Est1RM is the Brzycki estimate, 0 when not applicable.
func (s Set) Est1RM() float64 {
	if s.WeightKg == nil || s.Reps == nil || *s.Reps <= 0 || *s.Reps >= 37 {
		return 0
	}
	return *s.WeightKg * 36 / (37 - float64(*s.Reps))
}

// WorkoutExercise is an exercise's slot in a workout, with its sets.
type WorkoutExercise struct {
	ID         string
	WorkoutID  string
	ExerciseID string
	Exercise   Exercise // Name/CellTypes/Media filled on load
	Position   int
	Note       string
	RestS      *int
	Superset   *int
	Sets       []Set
}

// Workout is a session.
type Workout struct {
	ID         string
	TemplateID *string
	Name       string
	Notes      string
	StartedAt  time.Time
	EndedAt    *time.Time
	Timezone   string
	Origin     string
	Exercises  []WorkoutExercise
}

// InProgress reports whether the workout has not been finished.
func (w Workout) InProgress() bool { return w.EndedAt == nil }

// Duration is ended−started, or time since start while in progress.
func (w Workout) Duration() time.Duration {
	if w.EndedAt == nil {
		return time.Since(w.StartedAt)
	}
	return w.EndedAt.Sub(w.StartedAt)
}

// Volume sums completed non-warmup set volume.
func (w Workout) Volume() float64 {
	var v float64
	for _, we := range w.Exercises {
		for _, s := range we.Sets {
			if s.Completed && s.Kind != KindWarmup {
				v += s.Volume()
			}
		}
	}
	return v
}

// CompletedSets counts completed sets.
func (w Workout) CompletedSets() int {
	n := 0
	for _, we := range w.Exercises {
		for _, s := range we.Sets {
			if s.Completed {
				n++
			}
		}
	}
	return n
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return Time(*t)
}

func nullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// PutWorkout inserts or replaces a whole workout tree (by id). Used by
// the importer and by "finish"; live editing uses the finer-grained
// methods below.
func (s *Store) PutWorkout(ctx context.Context, w Workout) error {
	if w.Origin == "" {
		w.Origin = "local"
	}
	if w.Timezone == "" {
		w.Timezone = "UTC"
	}
	t := now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workout
			(id, template_id, name, notes, started_at, ended_at, timezone, origin, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET template_id = excluded.template_id, name = excluded.name,
			  notes = excluded.notes, started_at = excluded.started_at, ended_at = excluded.ended_at,
			  timezone = excluded.timezone, origin = excluded.origin, updated_at = excluded.updated_at`,
			w.ID, nullStr(w.TemplateID), w.Name, w.Notes, Time(w.StartedAt), nullTime(w.EndedAt), w.Timezone, w.Origin, t, t); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workout_exercise WHERE workout_id = ?`, w.ID); err != nil {
			return err
		}
		for i, we := range w.Exercises {
			if we.ID == "" {
				we.ID = NewID()
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workout_exercise
				(id, workout_id, exercise_id, position, note, rest_s, superset) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				we.ID, w.ID, we.ExerciseID, i, we.Note, we.RestS, we.Superset); err != nil {
				return fmt.Errorf("workout %s exercise %s: %w", w.ID, we.ExerciseID, err)
			}
			for j, st := range we.Sets {
				if st.ID == "" {
					st.ID = NewID()
				}
				if st.Kind == "" {
					st.Kind = KindNormal
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO "set"
					(id, workout_exercise_id, position, kind, weight_kg, reps, rpe, duration_s, distance_m, completed, completed_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					st.ID, we.ID, j, st.Kind, st.WeightKg, st.Reps, st.RPE, st.DurationS, st.DistanceM, st.Completed, nullTime(st.CompletedAt)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// StartWorkout creates an in-progress workout. ErrInProgress if one exists.
func (s *Store) StartWorkout(ctx context.Context, name string, templateID *string, tz string) (Workout, error) {
	w := Workout{ID: NewID(), Name: name, TemplateID: templateID, StartedAt: time.Now().UTC(), Timezone: tz, Origin: "local"}
	if tz == "" {
		w.Timezone = "UTC"
	}
	if templateID != nil {
		tpl, err := s.GetTemplate(ctx, *templateID)
		if err != nil {
			return w, err
		}
		if name == "" {
			w.Name = tpl.Name
		}
		for _, te := range tpl.Exercises {
			we := WorkoutExercise{ID: NewID(), ExerciseID: te.ExerciseID, Note: te.Note, RestS: te.RestS, Superset: te.Superset}
			for _, ts := range te.Sets {
				we.Sets = append(we.Sets, Set{ID: NewID(), Kind: ts.Kind, WeightKg: ts.WeightKg, Reps: ts.Reps, RPE: ts.RPE, DurationS: ts.DurationS, DistanceM: ts.DistanceM})
			}
			w.Exercises = append(w.Exercises, we)
		}
	}
	if w.Name == "" {
		w.Name = defaultWorkoutName(time.Now())
	}
	err := s.PutWorkout(ctx, w)
	if err != nil && isUniqueErr(err) {
		return w, ErrInProgress
	}
	return w, err
}

// ErrInProgress: a workout is already in progress (invariant 3).
var ErrInProgress = errors.New("a workout is already in progress")

func isUniqueErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func defaultWorkoutName(t time.Time) string {
	switch h := t.Hour(); {
	case h < 12:
		return "Morning Workout"
	case h < 17:
		return "Afternoon Workout"
	default:
		return "Evening Workout"
	}
}

// CurrentWorkout returns the in-progress workout, or ErrNotFound.
func (s *Store) CurrentWorkout(ctx context.Context) (Workout, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM workout WHERE ended_at IS NULL`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Workout{}, ErrNotFound
	}
	if err != nil {
		return Workout{}, err
	}
	return s.GetWorkout(ctx, id)
}

// FinishWorkout ends the workout now, dropping incomplete sets and
// exercises left with no sets. Returns the finished workout.
func (s *Store) FinishWorkout(ctx context.Context, id string) (Workout, error) {
	t := time.Now().UTC()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE workout SET ended_at = ?, updated_at = ? WHERE id = ? AND ended_at IS NULL`, Time(t), Time(t), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM "set" WHERE completed = 0 AND workout_exercise_id IN
			(SELECT id FROM workout_exercise WHERE workout_id = ?)`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM workout_exercise WHERE workout_id = ? AND id NOT IN (SELECT workout_exercise_id FROM "set")`, id)
		return err
	})
	if err != nil {
		return Workout{}, err
	}
	return s.GetWorkout(ctx, id)
}

// DeleteWorkout removes a workout and its sets.
func (s *Store) DeleteWorkout(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workout WHERE id = ?`, id)
	return err
}

// UpdateWorkoutMeta renames / re-notes a workout.
func (s *Store) UpdateWorkoutMeta(ctx context.Context, id, name, notes string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE workout SET name = ?, notes = ?, updated_at = ? WHERE id = ?`, name, notes, now(), id)
	return err
}

// AddWorkoutExercise appends an exercise with n empty sets, prefilled
// from the last performance when prefill is true.
func (s *Store) AddWorkoutExercise(ctx context.Context, workoutID, exerciseID string, n int, prefill bool) (WorkoutExercise, error) {
	we := WorkoutExercise{ID: NewID(), WorkoutID: workoutID, ExerciseID: exerciseID}
	var prev []Set
	if prefill {
		p, err := s.PreviousSets(ctx, exerciseID, workoutID)
		if err != nil {
			return we, err
		}
		prev = p
		if n < len(prev) {
			n = len(prev)
		}
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var pos int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position)+1, 0) FROM workout_exercise WHERE workout_id = ?`, workoutID).Scan(&pos); err != nil {
			return err
		}
		we.Position = pos
		if _, err := tx.ExecContext(ctx, `INSERT INTO workout_exercise (id, workout_id, exercise_id, position) VALUES (?, ?, ?, ?)`,
			we.ID, workoutID, exerciseID, pos); err != nil {
			return err
		}
		for j := 0; j < n; j++ {
			st := Set{ID: NewID(), Position: j, Kind: KindNormal}
			if j < len(prev) {
				st.Kind, st.WeightKg, st.Reps, st.DurationS, st.DistanceM = prev[j].Kind, prev[j].WeightKg, prev[j].Reps, prev[j].DurationS, prev[j].DistanceM
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO "set" (id, workout_exercise_id, position, kind, weight_kg, reps, duration_s, distance_m)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, st.ID, we.ID, j, st.Kind, st.WeightKg, st.Reps, st.DurationS, st.DistanceM); err != nil {
				return err
			}
			we.Sets = append(we.Sets, st)
		}
		return nil
	})
	return we, err
}

// RemoveWorkoutExercise deletes a slot and its sets, closing the gap.
func (s *Store) RemoveWorkoutExercise(ctx context.Context, weID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var wid string
		var pos int
		if err := tx.QueryRowContext(ctx, `SELECT workout_id, position FROM workout_exercise WHERE id = ?`, weID).Scan(&wid, &pos); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workout_exercise WHERE id = ?`, weID); err != nil {
			return err
		}
		// Renumber in two passes to dodge the UNIQUE(workout_id, position) index.
		if _, err := tx.ExecContext(ctx, `UPDATE workout_exercise SET position = -position-1 WHERE workout_id = ? AND position > ?`, wid, pos); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE workout_exercise SET position = -position-2 WHERE workout_id = ? AND position < 0`, wid)
		return err
	})
}

// Opt is a patch field: Set=false leaves the column alone; Set=true
// writes V (a nil pointer V clears a nullable column).
type Opt[T any] struct {
	Set bool
	V   T
}

// SlotPatch is a partial update of a WorkoutExercise.
type SlotPatch struct {
	Note     Opt[string]
	RestS    Opt[*int]
	Superset Opt[*int]
}

// UpdateWorkoutExercise writes the set fields of p; nothing to set is a
// no-op.
func (s *Store) UpdateWorkoutExercise(ctx context.Context, weID string, p SlotPatch) error {
	var cols []string
	var args []any
	if p.Note.Set {
		cols, args = append(cols, "note = ?"), append(args, p.Note.V)
	}
	if p.RestS.Set {
		cols, args = append(cols, "rest_s = ?"), append(args, p.RestS.V)
	}
	if p.Superset.Set {
		cols, args = append(cols, "superset = ?"), append(args, p.Superset.V)
	}
	if len(cols) == 0 {
		return nil
	}
	args = append(args, weID)
	_, err := s.db.ExecContext(ctx, `UPDATE workout_exercise SET `+strings.Join(cols, ", ")+` WHERE id = ?`, args...)
	return err
}

// AddSet appends an empty set copying the previous row's values.
func (s *Store) AddSet(ctx context.Context, weID string) (Set, error) {
	st := Set{ID: NewID(), Kind: KindNormal}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var pos int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position)+1, 0) FROM "set" WHERE workout_exercise_id = ?`, weID).Scan(&pos); err != nil {
			return err
		}
		st.Position = pos
		if pos > 0 {
			_ = tx.QueryRowContext(ctx, `SELECT weight_kg, reps, duration_s, distance_m FROM "set" WHERE workout_exercise_id = ? AND position = ?`, weID, pos-1).
				Scan(&st.WeightKg, &st.Reps, &st.DurationS, &st.DistanceM)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO "set" (id, workout_exercise_id, position, kind, weight_kg, reps, duration_s, distance_m)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, st.ID, weID, pos, st.Kind, st.WeightKg, st.Reps, st.DurationS, st.DistanceM)
		return err
	})
	return st, err
}

// InsertWarmups puts `sets` in front of a slot's existing sets, in one
// transaction: existing positions shift up by len(sets), the new rows
// take 0..n-1 with kind warmup, not completed. Returned sets carry their
// ids and positions. AddSet (append) is unchanged.
func (s *Store) InsertWarmups(ctx context.Context, weID string, sets []Set) ([]Set, error) {
	n := len(sets)
	out := make([]Set, 0, n)
	if n == 0 {
		return out, nil
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		// Shift in two passes to dodge UNIQUE(workout_exercise_id, position).
		if _, err := tx.ExecContext(ctx, `UPDATE "set" SET position = -position-1 WHERE workout_exercise_id = ?`, weID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE "set" SET position = -position-1+? WHERE workout_exercise_id = ? AND position < 0`, n, weID); err != nil {
			return err
		}
		for i, st := range sets {
			st.ID, st.Position, st.Kind, st.Completed, st.CompletedAt = NewID(), i, KindWarmup, false, nil
			if _, err := tx.ExecContext(ctx, `INSERT INTO "set" (id, workout_exercise_id, position, kind, weight_kg, reps, duration_s, distance_m)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, st.ID, weID, st.Position, st.Kind, st.WeightKg, st.Reps, st.DurationS, st.DistanceM); err != nil {
				return err
			}
			out = append(out, st)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSet writes values and completion for a set.
func (s *Store) UpdateSet(ctx context.Context, st Set) error {
	if st.Kind == "" {
		st.Kind = KindNormal
	}
	var completedAt any
	if st.Completed {
		if st.CompletedAt == nil {
			t := time.Now().UTC()
			st.CompletedAt = &t
		}
		completedAt = Time(*st.CompletedAt)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE "set" SET kind = ?, weight_kg = ?, reps = ?, rpe = ?, duration_s = ?, distance_m = ?, completed = ?, completed_at = ? WHERE id = ?`,
		st.Kind, st.WeightKg, st.Reps, st.RPE, st.DurationS, st.DistanceM, st.Completed, completedAt, st.ID)
	return err
}

// DeleteSet removes a set, closing the gap.
func (s *Store) DeleteSet(ctx context.Context, setID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var weID string
		var pos int
		if err := tx.QueryRowContext(ctx, `SELECT workout_exercise_id, position FROM "set" WHERE id = ?`, setID).Scan(&weID, &pos); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM "set" WHERE id = ?`, setID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE "set" SET position = -position-1 WHERE workout_exercise_id = ? AND position > ?`, weID, pos); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE "set" SET position = -position-2 WHERE workout_exercise_id = ? AND position < 0`, weID)
		return err
	})
}

// GetSet loads one set.
func (s *Store) GetSet(ctx context.Context, id string) (Set, string, error) {
	var st Set
	var weID string
	var completedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, workout_exercise_id, position, kind, weight_kg, reps, rpe, duration_s, distance_m, completed, completed_at FROM "set" WHERE id = ?`, id).
		Scan(&st.ID, &weID, &st.Position, &st.Kind, &st.WeightKg, &st.Reps, &st.RPE, &st.DurationS, &st.DistanceM, &st.Completed, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return st, "", ErrNotFound
	}
	if completedAt.Valid {
		if t, err := ParseTime(completedAt.String); err == nil {
			st.CompletedAt = &t
		}
	}
	return st, weID, err
}

// GetWorkout loads a workout with exercises (incl. library fields) and sets.
func (s *Store) GetWorkout(ctx context.Context, id string) (Workout, error) {
	var w Workout
	var started string
	var ended, tpl sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, template_id, name, notes, started_at, ended_at, timezone, origin FROM workout WHERE id = ?`, id).
		Scan(&w.ID, &tpl, &w.Name, &w.Notes, &started, &ended, &w.Timezone, &w.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	if w.StartedAt, err = ParseTime(started); err != nil {
		return w, err
	}
	if ended.Valid {
		t, err := ParseTime(ended.String)
		if err != nil {
			return w, err
		}
		w.EndedAt = &t
	}
	if tpl.Valid {
		w.TemplateID = &tpl.String
	}
	w.Exercises, err = s.workoutExercises(ctx, id)
	return w, err
}

func (s *Store) workoutExercises(ctx context.Context, workoutID string) ([]WorkoutExercise, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT we.id, we.workout_id, we.exercise_id, we.position, we.note, we.rest_s, we.superset, `+exerciseCols+`
		FROM workout_exercise we JOIN exercise e ON e.id = we.exercise_id WHERE we.workout_id = ? ORDER BY we.position`, workoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkoutExercise
	var ids []string
	for rows.Next() {
		var we WorkoutExercise
		var cells, inst string
		if err := rows.Scan(&we.ID, &we.WorkoutID, &we.ExerciseID, &we.Position, &we.Note, &we.RestS, &we.Superset,
			&we.Exercise.ID, &we.Exercise.Name, &we.Exercise.BodyPart, &we.Exercise.Category, &cells, &inst, &we.Exercise.Origin, &we.Exercise.Archived); err != nil {
			return nil, err
		}
		we.Exercise.CellTypes = fromJSONList(cells)
		we.Exercise.Instructions = fromJSONList(inst)
		out = append(out, we)
		ids = append(ids, we.ExerciseID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	media, err := s.mediaFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Exercise.Media = media[out[i].ExerciseID]
		srows, err := s.db.QueryContext(ctx, `SELECT id, position, kind, weight_kg, reps, rpe, duration_s, distance_m, completed, completed_at FROM "set" WHERE workout_exercise_id = ? ORDER BY position`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var st Set
			var completedAt sql.NullString
			if err := srows.Scan(&st.ID, &st.Position, &st.Kind, &st.WeightKg, &st.Reps, &st.RPE, &st.DurationS, &st.DistanceM, &st.Completed, &completedAt); err != nil {
				srows.Close()
				return nil, err
			}
			if completedAt.Valid {
				if t, err := ParseTime(completedAt.String); err == nil {
					st.CompletedAt = &t
				}
			}
			out[i].Sets = append(out[i].Sets, st)
		}
		srows.Close()
	}
	return out, nil
}

// WorkoutSummary is a history-list row.
type WorkoutSummary struct {
	Workout
	PRs int
}

// ListWorkouts returns finished workouts newest first, with exercises
// and sets loaded (the history view shows every exercise's best set).
func (s *Store) ListWorkouts(ctx context.Context, limit, offset int) ([]Workout, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM workout WHERE ended_at IS NOT NULL ORDER BY started_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]Workout, 0, len(ids))
	for _, id := range ids {
		w, err := s.GetWorkout(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// CountWorkouts counts finished workouts.
func (s *Store) CountWorkouts(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout WHERE ended_at IS NOT NULL`).Scan(&n)
	return n, err
}
