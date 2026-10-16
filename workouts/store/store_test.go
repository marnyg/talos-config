package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }
func i(v int) *int         { return &v }

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func bench(t *testing.T, s *Store) Exercise {
	t.Helper()
	e := Exercise{ID: "bench", Name: "Bench Press (Barbell)", BodyPart: "chest",
		CellTypes: []string{CellBarbellWeight, CellReps, CellRPE}, Instructions: []string{"Lie flat", "Press"},
		Origin: "strong", Media: []Media{{Kind: "video", File: "VID1.mp4", ContentType: "video/mp4"}, {Kind: "thumb", File: "THU1.png", ContentType: "image/png"}}}
	if err := s.PutExercise(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExerciseRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	bench(t, s)
	e, err := s.GetExercise(ctx, "bench")
	if err != nil {
		t.Fatal(err)
	}
	if e.Category != CatBarbell || len(e.Media) != 2 || e.Thumb() != "THU1.png" || e.MediaOf("video") != "VID1.mp4" || len(e.Instructions) != 2 {
		t.Fatalf("bad exercise: %+v", e)
	}
	list, err := s.ListExercises(ctx, ExerciseFilter{Query: "bench", BodyPart: "chest"})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if list, _ := s.ListExercises(ctx, ExerciseFilter{Category: CatDumbbell}); len(list) != 0 {
		t.Fatal("filter leak")
	}
	if _, err := s.GetExercise(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound")
	}
}

func TestCategoryFor(t *testing.T) {
	cases := map[string][]string{
		CatBarbell:            {CellBarbellWeight, CellReps},
		CatDumbbell:           {CellDumbbellWeight, CellReps},
		CatAssistedBodyweight: {CellAssistedBodyweight, CellReps},
		CatBodyweight:         {CellWeightedBodyweight, CellReps},
		CatMachineOther:       {CellOtherWeight, CellReps},
		CatRepsOnly:           {CellReps},
		CatDuration:           {CellDuration},
		CatCardio:             {CellDistance, CellDuration},
	}
	for want, cells := range cases {
		if got := CategoryFor(cells); got != want {
			t.Errorf("%v: got %s want %s", cells, got, want)
		}
	}
}

func TestWorkoutLifecycle(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	bench(t, s)

	// A finished earlier workout provides "previous".
	ended := time.Date(2026, 10, 7, 17, 0, 0, 0, time.UTC)
	prev := Workout{ID: "w0", Name: "Push", StartedAt: ended.Add(-time.Hour), EndedAt: &ended, Exercises: []WorkoutExercise{{
		ExerciseID: "bench", Sets: []Set{
			{Kind: KindWarmup, WeightKg: f(40), Reps: i(10), Completed: true},
			{WeightKg: f(60), Reps: i(7), Completed: true},
			{WeightKg: f(60), Reps: i(6), Completed: true},
		}}}}
	if err := s.PutWorkout(ctx, prev); err != nil {
		t.Fatal(err)
	}

	w, err := s.StartWorkout(ctx, "", nil, "Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartWorkout(ctx, "", nil, ""); !errors.Is(err, ErrInProgress) {
		t.Fatalf("second in-progress workout allowed: %v", err)
	}
	cur, err := s.CurrentWorkout(ctx)
	if err != nil || cur.ID != w.ID || !cur.InProgress() {
		t.Fatalf("current: %v %+v", err, cur)
	}

	we, err := s.AddWorkoutExercise(ctx, w.ID, "bench", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	// prefilled from the previous workout's completed sets (warmup included as a row)
	if len(we.Sets) != 3 || we.Sets[1].WeightKg == nil || *we.Sets[1].WeightKg != 60 || *we.Sets[1].Reps != 7 {
		t.Fatalf("prefill: %+v", we.Sets)
	}
	// complete two sets, leave one incomplete, add+delete a set
	st := we.Sets[1]
	st.WeightKg, st.Reps, st.Completed = f(60), i(8), true
	if err := s.UpdateSet(ctx, st); err != nil {
		t.Fatal(err)
	}
	st2 := we.Sets[2]
	st2.Completed = true
	if err := s.UpdateSet(ctx, st2); err != nil {
		t.Fatal(err)
	}
	extra, err := s.AddSet(ctx, we.ID)
	if err != nil || extra.Position != 3 || extra.WeightKg == nil || *extra.WeightKg != 60 {
		t.Fatalf("AddSet: %v %+v", err, extra)
	}
	if err := s.DeleteSet(ctx, we.Sets[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetWorkout(ctx, w.ID)
	if len(got.Exercises[0].Sets) != 3 || got.Exercises[0].Sets[0].Position != 0 || got.Exercises[0].Sets[2].Position != 2 {
		t.Fatalf("renumber after delete: %+v", got.Exercises[0].Sets)
	}

	fin, err := s.FinishWorkout(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fin.InProgress() || len(fin.Exercises[0].Sets) != 2 || fin.CompletedSets() != 2 {
		t.Fatalf("finish dropped wrong sets: %+v", fin.Exercises[0].Sets)
	}
	if v := fin.Volume(); v != 60*8+60*6 {
		t.Fatalf("volume %v", v)
	}
	if _, err := s.CurrentWorkout(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("still in progress")
	}

	// derived
	r, err := s.ExerciseRecords(ctx, "bench")
	if err != nil {
		t.Fatal(err)
	}
	if r.Est1RM == nil || math.Round(r.Est1RM.Value) != 74 { // Strong shows 74 for 60×8
		t.Fatalf("est 1RM %+v", r.Est1RM)
	}
	if r.MaxWeight == nil || r.MaxWeight.Value != 60 || r.MaxVolume == nil || r.MaxVolume.Value != 480 {
		t.Fatalf("records %+v %+v", r.MaxWeight, r.MaxVolume)
	}
	if r.RepMax[8] == nil || r.RepMax[8].WeightKg != 60 || r.RepMax[1].Reps != 8 || r.RepMax[9] != nil { // rows 1–8 from 60×8; warmup 40×10 excluded
		t.Fatalf("rep max %+v %+v", r.RepMax[8], r.RepMax[10])
	}
	if n, _ := s.WorkoutPRs(ctx, w.ID); n != 1 { // 60×8 beats 60×7 on est 1RM and volume: one set
		t.Fatalf("PRs %d", n)
	}
	if n, _ := s.WorkoutPRs(ctx, "w0"); n != 2 { // first ever: both working sets are PRs
		t.Fatalf("first-workout PRs %d", n)
	}
	series, _ := s.ExerciseSeries(ctx, "bench", "est_1rm")
	if len(series) != 2 || series[0].At.After(series[1].At) {
		t.Fatalf("series %+v", series)
	}
	hist, _ := s.ExerciseHistory(ctx, "bench", 10)
	if len(hist) != 2 || hist[0].WorkoutID != w.ID {
		t.Fatalf("history %+v", hist)
	}
	list, _ := s.ListExercises(ctx, ExerciseFilter{})
	if list[0].PerformedCount != 2 {
		t.Fatalf("performed %d", list[0].PerformedCount)
	}
	wk, _ := s.WorkoutsPerWeek(ctx, 8)
	if len(wk) == 0 {
		t.Fatal("no weeks")
	}
}

func TestTemplates(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	bench(t, s)
	if err := s.PutFolder(ctx, Folder{ID: "f1", Name: "My Templates"}); err != nil {
		t.Fatal(err)
	}
	fid := "f1"
	tpl := Template{ID: "t1", FolderID: &fid, Name: "Upper", Exercises: []TemplateExercise{{ExerciseID: "bench", RestS: i(120),
		Sets: []Set{{WeightKg: f(60), Reps: i(8)}, {WeightKg: f(60), Reps: i(7)}}}}}
	if err := s.PutTemplate(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	w, err := s.StartWorkout(ctx, "", &tpl.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "Upper" || len(w.Exercises) != 1 || len(w.Exercises[0].Sets) != 2 || *w.Exercises[0].RestS != 120 {
		t.Fatalf("from template: %+v", w)
	}
	got, _ := s.GetWorkout(ctx, w.ID)
	st := got.Exercises[0].Sets[0]
	st.Completed = true
	st.Reps = i(9)
	if err := s.UpdateSet(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishWorkout(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorkoutAsTemplate(ctx, w.ID, "t1", nil, ""); err != nil {
		t.Fatal(err)
	}
	folders, err := s.ListFolders(ctx)
	if err != nil || len(folders) != 1 || len(folders[0].Templates) != 1 {
		t.Fatalf("folders %v %+v", err, folders)
	}
	tp := folders[0].Templates[0]
	if tp.LastPerformed == nil || len(tp.Exercises[0].Sets) != 1 || *tp.Exercises[0].Sets[0].Reps != 9 {
		t.Fatalf("template after save: %+v", tp)
	}
	// exercise in use cannot be deleted, only archived
	if _, err := s.db.Exec(`DELETE FROM exercise WHERE id = 'bench'`); err == nil {
		t.Fatal("FK RESTRICT not enforced")
	}
	if err := s.SetExerciseArchived(ctx, "bench", true); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListExercises(ctx, ExerciseFilter{}); len(l) != 0 {
		t.Fatal("archived still listed")
	}
}

func TestMetrics(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Now()
	for d, v := range []float64{80, 79.5, 79} {
		if err := s.PutMetricEntry(ctx, MetricEntry{Metric: "weight_kg", At: now.AddDate(0, 0, d), Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	ser, _ := s.MetricSeries(ctx, "weight_kg", 10)
	if len(ser) != 3 || ser[0].Value != 79 {
		t.Fatalf("series %+v", ser)
	}
	latest, _ := s.LatestMetrics(ctx)
	if latest["weight_kg"].Value != 79 {
		t.Fatalf("latest %+v", latest)
	}
	if err := s.SetSetting(ctx, "weight_unit", "kg"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Setting(ctx, "weight_unit"); v != "kg" {
		t.Fatal("setting")
	}
}
