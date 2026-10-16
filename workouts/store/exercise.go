package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Cell types: the fields a set of an exercise records (domain-model.md).
const (
	CellBarbellWeight      = "BARBELL_WEIGHT"
	CellDumbbellWeight     = "DUMBBELL_WEIGHT"
	CellOtherWeight        = "OTHER_WEIGHT"
	CellWeightedBodyweight = "WEIGHTED_BODYWEIGHT"
	CellAssistedBodyweight = "ASSISTED_BODYWEIGHT"
	CellReps               = "REPS"
	CellRPE                = "RPE"
	CellDuration           = "DURATION"
	CellDistance           = "DISTANCE"
)

// Categories: the equipment class the picker filters on.
const (
	CatBarbell            = "barbell"
	CatDumbbell           = "dumbbell"
	CatMachineOther       = "machine_other"
	CatBodyweight         = "bodyweight"
	CatAssistedBodyweight = "assisted_bodyweight"
	CatRepsOnly           = "reps_only"
	CatDuration           = "duration"
	CatCardio             = "cardio"
)

// BodyParts is the closed set of body-part tags.
var BodyParts = []string{"arms", "back", "cardio", "chest", "core", "full-body", "legs", "olympic", "other", "shoulders"}

// Exercise is a library entry.
type Exercise struct {
	ID           string
	Name         string
	BodyPart     string
	Category     string
	CellTypes    []string
	Instructions []string
	Origin       string
	Archived     bool
	Media        []Media
	// PerformedCount is filled by ListExercises: completed workouts
	// containing the exercise.
	PerformedCount int
}

// Media is one file of an exercise: kind video|image|thumb.
type Media struct {
	Kind        string
	File        string
	ContentType string
}

// Thumb returns the thumbnail file, falling back to the image, or "".
func (e Exercise) Thumb() string {
	for _, k := range []string{"thumb", "image"} {
		for _, m := range e.Media {
			if m.Kind == k {
				return m.File
			}
		}
	}
	return ""
}

// MediaOf returns the file of the given kind, or "".
func (e Exercise) MediaOf(kind string) string {
	for _, m := range e.Media {
		if m.Kind == kind {
			return m.File
		}
	}
	return ""
}

// HasCell reports whether the exercise records the cell type.
func (e Exercise) HasCell(ct string) bool {
	for _, c := range e.CellTypes {
		if c == ct {
			return true
		}
	}
	return false
}

// HasWeight reports whether any weight cell is present.
func (e Exercise) HasWeight() bool {
	return e.HasCell(CellBarbellWeight) || e.HasCell(CellDumbbellWeight) || e.HasCell(CellOtherWeight) ||
		e.HasCell(CellWeightedBodyweight) || e.HasCell(CellAssistedBodyweight)
}

// CategoryFor derives the picker category from an exercise's cell
// types (the rule the import applies; a custom exercise may override).
func CategoryFor(cells []string) string {
	has := func(c string) bool {
		for _, x := range cells {
			if x == c {
				return true
			}
		}
		return false
	}
	switch {
	case has(CellBarbellWeight):
		return CatBarbell
	case has(CellDumbbellWeight):
		return CatDumbbell
	case has(CellAssistedBodyweight):
		return CatAssistedBodyweight
	case has(CellWeightedBodyweight):
		return CatBodyweight
	case has(CellOtherWeight):
		return CatMachineOther
	case has(CellDistance):
		return CatCardio
	case has(CellDuration):
		return CatDuration
	case has(CellReps):
		return CatRepsOnly
	}
	return CatRepsOnly
}

// PutExercise inserts or replaces an exercise and its media (by id).
func (s *Store) PutExercise(ctx context.Context, e Exercise) error {
	if e.Origin == "" {
		e.Origin = "local"
	}
	if e.Category == "" {
		e.Category = CategoryFor(e.CellTypes)
	}
	t := now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO exercise
			(id, name, body_part, category, cell_types, instructions, origin, archived, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name, body_part = excluded.body_part,
			  category = excluded.category, cell_types = excluded.cell_types,
			  instructions = excluded.instructions, origin = excluded.origin,
			  archived = excluded.archived, updated_at = excluded.updated_at`,
			e.ID, strings.TrimSpace(e.Name), e.BodyPart, e.Category, jsonList(e.CellTypes), jsonList(e.Instructions),
			e.Origin, e.Archived, t, t)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM exercise_media WHERE exercise_id = ?`, e.ID); err != nil {
			return err
		}
		for _, m := range e.Media {
			if _, err := tx.ExecContext(ctx, `INSERT INTO exercise_media (exercise_id, kind, file, content_type)
				VALUES (?, ?, ?, ?) ON CONFLICT DO UPDATE SET file = excluded.file, content_type = excluded.content_type`,
				e.ID, m.Kind, m.File, m.ContentType); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetExerciseArchived flips the archived flag.
func (s *Store) SetExerciseArchived(ctx context.Context, id string, archived bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE exercise SET archived = ?, updated_at = ? WHERE id = ?`, archived, now(), id)
	return err
}

const exerciseCols = `e.id, e.name, e.body_part, e.category, e.cell_types, e.instructions, e.origin, e.archived`

func scanExercise(sc interface{ Scan(...any) error }, e *Exercise) error {
	var cells, inst string
	if err := sc.Scan(&e.ID, &e.Name, &e.BodyPart, &e.Category, &cells, &inst, &e.Origin, &e.Archived); err != nil {
		return err
	}
	e.CellTypes = fromJSONList(cells)
	e.Instructions = fromJSONList(inst)
	return nil
}

// GetExercise loads one exercise with media.
func (s *Store) GetExercise(ctx context.Context, id string) (Exercise, error) {
	var e Exercise
	row := s.db.QueryRowContext(ctx, `SELECT `+exerciseCols+` FROM exercise e WHERE e.id = ?`, id)
	if err := scanExercise(row, &e); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e, ErrNotFound
		}
		return e, err
	}
	media, err := s.mediaFor(ctx, []string{id})
	if err != nil {
		return e, err
	}
	e.Media = media[id]
	return e, nil
}

func (s *Store) mediaFor(ctx context.Context, ids []string) (map[string][]Media, error) {
	out := map[string][]Media{}
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT exercise_id, kind, file, content_type FROM exercise_media WHERE exercise_id IN (?` + strings.Repeat(",?", len(ids)-1) + `) ORDER BY kind`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var m Media
		if err := rows.Scan(&id, &m.Kind, &m.File, &m.ContentType); err != nil {
			return nil, err
		}
		out[id] = append(out[id], m)
	}
	return out, rows.Err()
}

// ExerciseFilter narrows ListExercises. Empty fields match everything.
type ExerciseFilter struct {
	Query           string
	BodyPart        string
	Category        string
	IncludeArchived bool
}

// ListExercises returns exercises sorted by name, with media and
// PerformedCount.
func (s *Store) ListExercises(ctx context.Context, f ExerciseFilter) ([]Exercise, error) {
	q := `SELECT ` + exerciseCols + `,
		(SELECT COUNT(DISTINCT we.workout_id) FROM workout_exercise we
		   JOIN workout w ON w.id = we.workout_id
		  WHERE we.exercise_id = e.id AND w.ended_at IS NOT NULL) AS performed
		FROM exercise e WHERE 1=1`
	var args []any
	if !f.IncludeArchived {
		q += ` AND e.archived = 0`
	}
	if f.Query != "" {
		q += ` AND e.name LIKE ? COLLATE NOCASE`
		args = append(args, "%"+f.Query+"%")
	}
	if f.BodyPart != "" {
		q += ` AND e.body_part = ?`
		args = append(args, f.BodyPart)
	}
	if f.Category != "" {
		q += ` AND e.category = ?`
		args = append(args, f.Category)
	}
	q += ` ORDER BY e.name COLLATE NOCASE`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Exercise
	var ids []string
	for rows.Next() {
		var e Exercise
		var cells, inst string
		if err := rows.Scan(&e.ID, &e.Name, &e.BodyPart, &e.Category, &cells, &inst, &e.Origin, &e.Archived, &e.PerformedCount); err != nil {
			return nil, err
		}
		e.CellTypes = fromJSONList(cells)
		e.Instructions = fromJSONList(inst)
		out = append(out, e)
		ids = append(ids, e.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	media, err := s.mediaFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Media = media[out[i].ID]
	}
	return out, nil
}
