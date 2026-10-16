package store

import (
	"context"
	"database/sql"
	"errors"
)

// Folder groups templates.
type Folder struct {
	ID        string
	Name      string
	Position  int
	Origin    string
	Templates []Template
}

// Template is a planned workout.
type Template struct {
	ID        string
	FolderID  *string
	Name      string
	Notes     string
	Position  int
	Origin    string
	Exercises []TemplateExercise
	// LastPerformed is the most recent finished workout started from
	// this template (filled by ListFolders), nil if never.
	LastPerformed *string
}

// TemplateExercise is a slot in a template.
type TemplateExercise struct {
	ID         string
	ExerciseID string
	Exercise   Exercise
	Position   int
	Note       string
	RestS      *int
	Superset   *int
	Sets       []Set // Completed/CompletedAt unused
}

// PutFolder inserts or replaces a folder.
func (s *Store) PutFolder(ctx context.Context, f Folder) error {
	if f.Origin == "" {
		f.Origin = "local"
	}
	t := now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO folder (id, name, position, origin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, position = excluded.position, origin = excluded.origin, updated_at = excluded.updated_at`,
		f.ID, f.Name, f.Position, f.Origin, t, t)
	return err
}

// DeleteFolder removes a folder; its templates become unfiled.
func (s *Store) DeleteFolder(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM folder WHERE id = ?`, id)
	return err
}

// PutTemplate inserts or replaces a template tree.
func (s *Store) PutTemplate(ctx context.Context, tpl Template) error {
	if tpl.Origin == "" {
		tpl.Origin = "local"
	}
	t := now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO template (id, folder_id, name, notes, position, origin, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET folder_id = excluded.folder_id, name = excluded.name, notes = excluded.notes,
			  position = excluded.position, origin = excluded.origin, updated_at = excluded.updated_at`,
			tpl.ID, nullStr(tpl.FolderID), tpl.Name, tpl.Notes, tpl.Position, tpl.Origin, t, t); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM template_exercise WHERE template_id = ?`, tpl.ID); err != nil {
			return err
		}
		for i, te := range tpl.Exercises {
			if te.ID == "" {
				te.ID = NewID()
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO template_exercise (id, template_id, exercise_id, position, note, rest_s, superset)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, te.ID, tpl.ID, te.ExerciseID, i, te.Note, te.RestS, te.Superset); err != nil {
				return err
			}
			for j, st := range te.Sets {
				if st.ID == "" {
					st.ID = NewID()
				}
				if st.Kind == "" {
					st.Kind = KindNormal
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO template_set (id, template_exercise_id, position, kind, weight_kg, reps, rpe, duration_s, distance_m)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, st.ID, te.ID, j, st.Kind, st.WeightKg, st.Reps, st.RPE, st.DurationS, st.DistanceM); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// DeleteTemplate removes a template; workouts keep their history.
func (s *Store) DeleteTemplate(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM template WHERE id = ?`, id)
	return err
}

// GetTemplate loads a template with exercises and sets.
func (s *Store) GetTemplate(ctx context.Context, id string) (Template, error) {
	var tpl Template
	var folder sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, folder_id, name, notes, position, origin FROM template WHERE id = ?`, id).
		Scan(&tpl.ID, &folder, &tpl.Name, &tpl.Notes, &tpl.Position, &tpl.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return tpl, ErrNotFound
	}
	if err != nil {
		return tpl, err
	}
	if folder.Valid {
		tpl.FolderID = &folder.String
	}
	tpl.Exercises, err = s.templateExercises(ctx, id)
	return tpl, err
}

func (s *Store) templateExercises(ctx context.Context, tplID string) ([]TemplateExercise, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT te.id, te.exercise_id, te.position, te.note, te.rest_s, te.superset, `+exerciseCols+`
		FROM template_exercise te JOIN exercise e ON e.id = te.exercise_id WHERE te.template_id = ? ORDER BY te.position`, tplID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TemplateExercise
	var ids []string
	for rows.Next() {
		var te TemplateExercise
		var cells, inst string
		if err := rows.Scan(&te.ID, &te.ExerciseID, &te.Position, &te.Note, &te.RestS, &te.Superset,
			&te.Exercise.ID, &te.Exercise.Name, &te.Exercise.BodyPart, &te.Exercise.Category, &cells, &inst, &te.Exercise.Origin, &te.Exercise.Archived); err != nil {
			return nil, err
		}
		te.Exercise.CellTypes = fromJSONList(cells)
		te.Exercise.Instructions = fromJSONList(inst)
		out = append(out, te)
		ids = append(ids, te.ExerciseID)
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
		srows, err := s.db.QueryContext(ctx, `SELECT id, position, kind, weight_kg, reps, rpe, duration_s, distance_m FROM template_set WHERE template_exercise_id = ? ORDER BY position`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var st Set
			if err := srows.Scan(&st.ID, &st.Position, &st.Kind, &st.WeightKg, &st.Reps, &st.RPE, &st.DurationS, &st.DistanceM); err != nil {
				srows.Close()
				return nil, err
			}
			out[i].Sets = append(out[i].Sets, st)
		}
		srows.Close()
	}
	return out, nil
}

// ListFolders returns folders (position order) with their templates
// loaded; templates without a folder come in a synthetic folder with
// ID "".
func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, position, origin FROM folder ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	var folders []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.Position, &f.Origin); err != nil {
			rows.Close()
			return nil, err
		}
		folders = append(folders, f)
	}
	rows.Close()
	trows, err := s.db.QueryContext(ctx, `SELECT t.id, t.folder_id,
		(SELECT MAX(w.started_at) FROM workout w WHERE w.template_id = t.id AND w.ended_at IS NOT NULL)
		FROM template t ORDER BY t.position, t.name`)
	if err != nil {
		return nil, err
	}
	type row struct {
		id     string
		folder sql.NullString
		last   sql.NullString
	}
	var trs []row
	for trows.Next() {
		var r row
		if err := trows.Scan(&r.id, &r.folder, &r.last); err != nil {
			trows.Close()
			return nil, err
		}
		trs = append(trs, r)
	}
	trows.Close()
	unfiled := Folder{ID: "", Name: "Templates"}
	byID := map[string]*Folder{}
	for i := range folders {
		byID[folders[i].ID] = &folders[i]
	}
	for _, r := range trs {
		tpl, err := s.GetTemplate(ctx, r.id)
		if err != nil {
			return nil, err
		}
		if r.last.Valid {
			tpl.LastPerformed = &r.last.String
		}
		if r.folder.Valid {
			if f, ok := byID[r.folder.String]; ok {
				f.Templates = append(f.Templates, tpl)
				continue
			}
		}
		unfiled.Templates = append(unfiled.Templates, tpl)
	}
	if len(unfiled.Templates) > 0 {
		folders = append([]Folder{unfiled}, folders...)
	}
	return folders, nil
}

// SaveWorkoutAsTemplate writes a workout's exercises and sets over a
// template (the "update template?" prompt on finish, or "save as new").
func (s *Store) SaveWorkoutAsTemplate(ctx context.Context, workoutID, templateID string, folderID *string, name string) error {
	w, err := s.GetWorkout(ctx, workoutID)
	if err != nil {
		return err
	}
	tpl := Template{ID: templateID, FolderID: folderID, Name: name, Origin: "local"}
	if templateID == "" {
		tpl.ID = NewID()
	} else if old, err := s.GetTemplate(ctx, templateID); err == nil {
		tpl.Notes, tpl.Position, tpl.FolderID = old.Notes, old.Position, old.FolderID
		if name == "" {
			tpl.Name = old.Name
		}
	}
	for _, we := range w.Exercises {
		te := TemplateExercise{ExerciseID: we.ExerciseID, Note: we.Note, RestS: we.RestS, Superset: we.Superset}
		for _, st := range we.Sets {
			te.Sets = append(te.Sets, Set{Kind: st.Kind, WeightKg: st.WeightKg, Reps: st.Reps, RPE: st.RPE, DurationS: st.DurationS, DistanceM: st.DistanceM})
		}
		tpl.Exercises = append(tpl.Exercises, te)
	}
	return s.PutTemplate(ctx, tpl)
}
