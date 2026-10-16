package store

import (
	"context"
	"database/sql"
	"time"
)

// Everything in this file is derived from set_done (schema.sql) at
// read time — domain-model.md invariant 4.

// PreviousSets returns the completed, non-warmup sets of the most
// recent finished workout containing the exercise, excluding the
// workout given (the one being logged).
func (s *Store) PreviousSets(ctx context.Context, exerciseID, excludeWorkoutID string) ([]Set, error) {
	var weID string
	err := s.db.QueryRowContext(ctx, `SELECT we.id FROM workout_exercise we JOIN workout w ON w.id = we.workout_id
		WHERE we.exercise_id = ? AND w.ended_at IS NOT NULL AND w.id <> ?
		ORDER BY w.started_at DESC LIMIT 1`, exerciseID, excludeWorkoutID).Scan(&weID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, position, kind, weight_kg, reps, rpe, duration_s, distance_m FROM "set"
		WHERE workout_exercise_id = ? AND completed = 1 ORDER BY position`, weID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Set
	for rows.Next() {
		var st Set
		if err := rows.Scan(&st.ID, &st.Position, &st.Kind, &st.WeightKg, &st.Reps, &st.RPE, &st.DurationS, &st.DistanceM); err != nil {
			return nil, err
		}
		st.Completed = true
		out = append(out, st)
	}
	return out, rows.Err()
}

// BestSet is a record holder: the set and when it happened.
type BestSet struct {
	Value     float64
	WeightKg  float64
	Reps      int
	WorkoutID string
	At        time.Time
}

// Records is the per-exercise record sheet.
type Records struct {
	Est1RM    *BestSet
	MaxWeight *BestSet
	MaxVolume *BestSet
	// RepMax[n] for n = 1..12: best actual set at exactly n reps.
	RepMax [13]*BestSet
}

// EstimateAtReps projects the best est. 1RM back to n reps (Brzycki).
func (r Records) EstimateAtReps(n int) float64 {
	if r.Est1RM == nil || n <= 0 || n >= 37 {
		return 0
	}
	return r.Est1RM.Value * (37 - float64(n)) / 36
}

func (s *Store) bestBy(ctx context.Context, exerciseID, expr string) (*BestSet, error) {
	var b BestSet
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT `+expr+`, weight_kg, reps, workout_id, started_at FROM set_done
		WHERE exercise_id = ? AND `+expr+` IS NOT NULL ORDER BY `+expr+` DESC, started_at ASC LIMIT 1`, exerciseID).
		Scan(&b.Value, &b.WeightKg, &b.Reps, &b.WorkoutID, &at)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.At, _ = ParseTime(at)
	return &b, nil
}

// ExerciseRecords computes the record sheet for an exercise.
func (s *Store) ExerciseRecords(ctx context.Context, exerciseID string) (Records, error) {
	var r Records
	var err error
	if r.Est1RM, err = s.bestBy(ctx, exerciseID, "est_1rm"); err != nil {
		return r, err
	}
	if r.MaxWeight, err = s.bestBy(ctx, exerciseID, "weight_kg"); err != nil {
		return r, err
	}
	if r.MaxVolume, err = s.bestBy(ctx, exerciseID, "volume"); err != nil {
		return r, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT reps, MAX(weight_kg), workout_id, MIN(started_at) FROM set_done
		WHERE exercise_id = ? AND reps BETWEEN 1 AND 12 AND weight_kg IS NOT NULL GROUP BY reps`, exerciseID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var b BestSet
		var at string
		if err := rows.Scan(&b.Reps, &b.WeightKg, &b.WorkoutID, &at); err != nil {
			return r, err
		}
		b.Value = b.WeightKg
		b.At, _ = ParseTime(at)
		r.RepMax[b.Reps] = &b
	}
	return r, rows.Err()
}

// ExerciseHistoryEntry is one workout's sets of an exercise.
type ExerciseHistoryEntry struct {
	WorkoutID   string
	WorkoutName string
	StartedAt   time.Time
	Sets        []Set
}

// ExerciseHistory lists completed sets per workout, newest first.
func (s *Store) ExerciseHistory(ctx context.Context, exerciseID string, limit int) ([]ExerciseHistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT we.id, w.id, w.name, w.started_at FROM workout_exercise we
		JOIN workout w ON w.id = we.workout_id WHERE we.exercise_id = ? AND w.ended_at IS NOT NULL
		ORDER BY w.started_at DESC LIMIT ?`, exerciseID, limit)
	if err != nil {
		return nil, err
	}
	type hdr struct {
		weID string
		e    ExerciseHistoryEntry
	}
	var hs []hdr
	for rows.Next() {
		var h hdr
		var at string
		if err := rows.Scan(&h.weID, &h.e.WorkoutID, &h.e.WorkoutName, &at); err != nil {
			rows.Close()
			return nil, err
		}
		h.e.StartedAt, _ = ParseTime(at)
		hs = append(hs, h)
	}
	rows.Close()
	out := make([]ExerciseHistoryEntry, 0, len(hs))
	for _, h := range hs {
		srows, err := s.db.QueryContext(ctx, `SELECT id, position, kind, weight_kg, reps, rpe, duration_s, distance_m FROM "set"
			WHERE workout_exercise_id = ? AND completed = 1 ORDER BY position`, h.weID)
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var st Set
			if err := srows.Scan(&st.ID, &st.Position, &st.Kind, &st.WeightKg, &st.Reps, &st.RPE, &st.DurationS, &st.DistanceM); err != nil {
				srows.Close()
				return nil, err
			}
			st.Completed = true
			h.e.Sets = append(h.e.Sets, st)
		}
		srows.Close()
		out = append(out, h.e)
	}
	return out, nil
}

// Point is a chart sample.
type Point struct {
	At    time.Time
	Value float64
}

// ExerciseSeries returns one value per workout for charts:
// metric ∈ est_1rm | weight_kg | volume_total | reps.
func (s *Store) ExerciseSeries(ctx context.Context, exerciseID, metric string) ([]Point, error) {
	var expr string
	switch metric {
	case "est_1rm":
		expr = "MAX(est_1rm)"
	case "weight_kg":
		expr = "MAX(weight_kg)"
	case "volume_total":
		expr = "SUM(volume)"
	case "reps":
		expr = "MAX(reps)"
	default:
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT started_at, `+expr+` FROM set_done WHERE exercise_id = ? GROUP BY workout_id HAVING `+expr+` IS NOT NULL ORDER BY started_at`, exerciseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var at string
		var p Point
		if err := rows.Scan(&at, &p.Value); err != nil {
			return nil, err
		}
		p.At, _ = ParseTime(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// WorkoutPRs counts sets in the workout that beat every earlier set of
// the same exercise on est. 1RM, weight or volume.
func (s *Store) WorkoutPRs(ctx context.Context, workoutID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM set_done d WHERE d.workout_id = ? AND (
		  (d.est_1rm IS NOT NULL AND d.est_1rm > COALESCE((SELECT MAX(p.est_1rm) FROM set_done p WHERE p.exercise_id = d.exercise_id AND p.started_at < d.started_at), 0))
		OR (d.weight_kg IS NOT NULL AND d.weight_kg > COALESCE((SELECT MAX(p.weight_kg) FROM set_done p WHERE p.exercise_id = d.exercise_id AND p.started_at < d.started_at), 0))
		OR (d.volume IS NOT NULL AND d.volume > COALESCE((SELECT MAX(p.volume) FROM set_done p WHERE p.exercise_id = d.exercise_id AND p.started_at < d.started_at), 0)))`,
		workoutID).Scan(&n)
	return n, err
}

// WorkoutsPerWeek returns counts for the last n ISO weeks, oldest first.
func (s *Store) WorkoutsPerWeek(ctx context.Context, n int) ([]Point, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT strftime('%Y-%W', started_at) AS wk, MIN(started_at), COUNT(*) FROM workout
		WHERE ended_at IS NOT NULL GROUP BY wk ORDER BY wk DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var wk, at string
		var p Point
		if err := rows.Scan(&wk, &at, &p.Value); err != nil {
			return nil, err
		}
		p.At, _ = ParseTime(at)
		out = append([]Point{p}, out...)
	}
	return out, rows.Err()
}
