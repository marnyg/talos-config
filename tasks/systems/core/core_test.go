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

func TestTags(t *testing.T) {
	w, lg := newWorld(t)

	// Created carries tags: normalized, deduped, sorted.
	id, err := Create(w, "paint fence", "", "Home", "garden", "home")
	if err != nil {
		t.Fatal(err)
	}
	if got := load(t, w, id).Tags; len(got) != 2 || got[0] != "garden" || got[1] != "home" {
		t.Errorf("tags = %v", got)
	}
	other, _ := Create(w, "untagged", "")
	if load(t, w, other).Tags != nil {
		t.Error("untagged task carries Tags")
	}

	if err := AddTag(w, id, " Work "); err != nil {
		t.Fatal(err)
	}
	if got := load(t, w, id); !got.Has("work") || !got.InScope("work") || got.InScope("nope") || !got.InScope("") {
		t.Errorf("after tag = %v", got.Tags)
	}
	if err := RemoveTag(w, id, "garden"); err != nil {
		t.Fatal(err)
	}

	// No-ops and bad tags are rejected and leave no trace.
	n := len(lg.Events)
	for _, bad := range []error{
		AddTag(w, id, "work"),      // already
		RemoveTag(w, id, "garden"), // not any more
		AddTag(w, id, ""),          // empty
		AddTag(w, id, "two words"), // separator
		AddTag(w, "nope", "x"),     // no such task
	} {
		if bad == nil {
			t.Error("rejection expected")
		}
	}
	if _, err := Create(w, "bad", "", "a,b"); err == nil {
		t.Error("comma in a Created tag accepted")
	}
	if len(lg.Events) != n {
		t.Errorf("log grew on rejected events: %d → %d", n, len(lg.Events))
	}

	// Vocabulary and Scoped are derived.
	w.Read(func(r ecs.Reader) {
		if v := Vocabulary(r); len(v) != 2 || v[0] != "home" || v[1] != "work" {
			t.Errorf("vocabulary = %v", v)
		}
		if s := Scoped(r, "home"); len(s) != 1 || s[0].ID != id {
			t.Errorf("scoped(home) = %v", s)
		}
		if s := Scoped(r, ""); len(s) != 2 {
			t.Errorf("scoped('') = %d tasks", len(s))
		}
	})

	// Removing the last tag drops the component; replay agrees.
	if err := RemoveTag(w, id, "home"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTag(w, id, "work"); err != nil {
		t.Fatal(err)
	}
	w.Read(func(r ecs.Reader) {
		if r.Has(id, Tags) {
			t.Error("empty Tags component kept")
		}
	})
	w2, err := ecs.New(lg, System{})
	if err != nil {
		t.Fatal(err)
	}
	if got := load(t, w2, id); got.Tags != nil {
		t.Errorf("after replay tags = %v", got.Tags)
	}
}

func TestParseTags(t *testing.T) {
	got, err := ParseTags(" Home, work  garden,home\n")
	if err != nil || len(got) != 3 || got[0] != "garden" || got[1] != "home" || got[2] != "work" {
		t.Errorf("ParseTags = %v, %v", got, err)
	}
	if got, err := ParseTags(""); err != nil || got != nil {
		t.Errorf("ParseTags('') = %v, %v", got, err)
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
