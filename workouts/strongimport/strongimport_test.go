package strongimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marnyg/talos-config/workouts/store"
)

func TestImport(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	media := t.TempDir()
	r, err := Import(ctx, s, "testdata/export", Options{MediaDir: media})
	if err != nil {
		t.Fatal(err)
	}
	if r.Exercises != 4 || r.MediaCopied != 2 || r.MediaMissing != 1 || r.Folders != 2 || r.Templates != 1 || r.Workouts != 2 {
		t.Fatalf("report %+v", r)
	}
	if len(r.Skipped) != 1 || r.Skipped[0] != "workout Upper 2026-10-14: unknown exercise ghost" {
		t.Fatalf("skipped %v", r.Skipped)
	}
	if _, err := os.Stat(filepath.Join(media, "THUaaa@3x.png")); err != nil {
		t.Fatal("thumb not copied (and %40 not decoded)")
	}

	// exercises
	b, err := s.GetExercise(ctx, "bench")
	if err != nil {
		t.Fatal(err)
	}
	if b.Origin != "strong" || b.BodyPart != "chest" || b.Category != store.CatBarbell || len(b.Instructions) != 2 ||
		len(b.Media) != 2 || b.Thumb() != "THUaaa@3x.png" || b.MediaOf("video") != "VIDaaa.mp4" {
		t.Fatalf("bench %+v", b)
	}
	if got := b.CellTypes; len(got) != 3 || got[0] != store.CellBarbellWeight || got[2] != store.CellRPE {
		t.Fatalf("cells %v", got)
	}
	c, _ := s.GetExercise(ctx, "custom1")
	if c.Origin != "local" || c.Name != "My Curl" || c.Category != store.CatMachineOther {
		t.Fatalf("custom %+v", c)
	}
	run, _ := s.GetExercise(ctx, "run")
	if run.Category != store.CatCardio {
		t.Fatalf("run %+v", run)
	}
	pu, _ := s.GetExercise(ctx, "pullup")
	if pu.Category != store.CatAssistedBodyweight {
		t.Fatalf("pullup %+v", pu)
	}

	// templates
	folders, _ := s.ListFolders(ctx)
	if len(folders) != 2 || folders[0].ID != "u1-my-templates" || folders[1].ID != "example-templates" {
		t.Fatalf("folders %+v", folders)
	}
	tpl := folders[0].Templates[0]
	if tpl.Name != "Upper" || len(tpl.Exercises) != 2 || len(tpl.Exercises[0].Sets) != 2 || *tpl.Exercises[0].RestS != 120 || *tpl.Exercises[1].Sets[0].WeightKg != 25 {
		t.Fatalf("template %+v", tpl)
	}
	if tpl.LastPerformed == nil {
		t.Fatal("last performed not linked")
	}

	// workouts
	w, err := s.GetWorkout(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if w.TemplateID == nil || *w.TemplateID != "t1" || w.Timezone != "Europe/Oslo" || w.InProgress() || w.Name != "Upper" {
		t.Fatalf("workout %+v", w)
	}
	// ghost exercise skipped, note-only group dropped
	if len(w.Exercises) != 2 {
		t.Fatalf("exercises %d", len(w.Exercises))
	}
	bp := w.Exercises[0]
	if bp.Note != "felt heavy" || *bp.RestS != 120 || len(bp.Sets) != 3 { // incomplete s4 dropped
		t.Fatalf("bench slot %+v", bp)
	}
	if bp.Sets[1].RPE == nil || *bp.Sets[1].RPE != 8.5 || bp.Sets[2].Kind != store.KindDrop || !bp.Sets[2].Completed {
		t.Fatalf("sets %+v", bp.Sets)
	}
	if rn := w.Exercises[1]; *rn.Sets[0].DistanceM != 5000 || *rn.Sets[0].DurationS != 1500 {
		t.Fatalf("run %+v", rn.Sets)
	}
	w0, _ := s.GetWorkout(ctx, "w0")
	if w0.TemplateID != nil {
		t.Fatal("dangling template link should be dropped")
	}

	// derived values line up with what Strong showed
	rec, _ := s.ExerciseRecords(ctx, "bench")
	if rec.Est1RM == nil || int(rec.Est1RM.Value+0.5) != 74 || rec.MaxWeight.Value != 60 || rec.RepMax[12] == nil {
		t.Fatalf("records %+v", rec)
	}

	// idempotent
	r2, err := Import(ctx, s, "testdata/export", Options{MediaDir: media})
	if err != nil || r2.Workouts != 2 || r2.Exercises != 4 {
		t.Fatalf("re-import %v %+v", err, r2)
	}
	if n, _ := s.CountWorkouts(ctx); n != 2 {
		t.Fatalf("duplicated on re-import: %d", n)
	}
}
