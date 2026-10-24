package core

import (
	"testing"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
)

func newWorld(t *testing.T) (*ecs.World, *ecs.MemLog) {
	t.Helper()
	lg := &ecs.MemLog{}
	w, err := ecs.New(lg, System{})
	if err != nil {
		t.Fatal(err)
	}
	return w, lg
}

func load(t *testing.T, w *ecs.World, id ecs.EntityID) Task {
	t.Helper()
	var (
		task Task
		ok   bool
	)
	w.Read(func(r ecs.Reader) { task, ok = Load(r, id) })
	if !ok {
		t.Fatalf("task %s not found", id)
	}
	return task
}

func TestLifecycle(t *testing.T) {
	w, lg := newWorld(t)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	id, err := Create(w, "write tests", "for core")
	if err != nil {
		t.Fatal(err)
	}
	task := load(t, w, id)
	if task.Title != "write tests" || task.Body != "for core" || !task.CreatedAt.Equal(now) {
		t.Errorf("created = %+v", task)
	}

	if err := Snooze(w, id, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !load(t, w, id).Sleeping(now) {
		t.Error("should be sleeping")
	}
	if load(t, w, id).Sleeping(now.Add(48 * time.Hour)) {
		t.Error("sleep must expire by derivation, not by event")
	}
	if err := Wake(w, id); err != nil {
		t.Fatal(err)
	}
	if load(t, w, id).SleepUntil != nil {
		t.Error("woken task still has SleepUntil")
	}

	if err := FlagForGrooming(w, id); err != nil {
		t.Fatal(err)
	}
	if !load(t, w, id).NeedsGrooming {
		t.Error("not flagged")
	}
	if err := Edit(w, id, "title", "write good tests"); err != nil {
		t.Fatal(err)
	}
	if err := Groom(w, id); err != nil {
		t.Fatal(err)
	}
	if err := MarkDone(w, id); err != nil {
		t.Fatal(err)
	}
	task = load(t, w, id)
	if task.Title != "write good tests" || task.NeedsGrooming || !task.IsDone() {
		t.Errorf("final = %+v", task)
	}

	// Rejections leave no trace in the log.
	n := len(lg.Events)
	if err := Edit(w, id, "title", ""); err == nil {
		t.Error("empty title accepted")
	}
	if err := Edit(w, id, "colour", "red"); err == nil {
		t.Error("unknown field accepted")
	}
	if err := MarkDone(w, "nope"); err != ErrNoSuchTask {
		t.Errorf("missing task: %v", err)
	}
	if len(lg.Events) != n {
		t.Errorf("log grew on rejected events: %d → %d", n, len(lg.Events))
	}

	// Replay reproduces the projection.
	w2, err := ecs.New(lg, System{})
	if err != nil {
		t.Fatal(err)
	}
	if got := load(t, w2, id); got.Title != task.Title || got.IsDone() != task.IsDone() {
		t.Errorf("after replay = %+v, want %+v", got, task)
	}
}

func TestParseUntil(t *testing.T) {
	now := time.Date(2026, 3, 1, 15, 30, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"tomorrow":   time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
		"week":       time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC),
		"2026-04-01": time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, err := ParseUntil(now, in)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseUntil(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "01/04/2026"} {
		if _, err := ParseUntil(now, bad); err == nil {
			t.Errorf("ParseUntil(%q) accepted", bad)
		}
	}
}
