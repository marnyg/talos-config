package kanban

import (
	"errors"
	"testing"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/systems/dag"
)

func setup(t *testing.T) (*ecs.World, time.Time) {
	t.Helper()
	w, err := ecs.New(&ecs.MemLog{}, core.System{}, dag.System{}, System{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	clock := now
	w.Now = func() time.Time { clock = clock.Add(time.Second); return clock } // distinct CreatedAt
	return w, now.Add(time.Hour)
}

func mk(t *testing.T, w *ecs.World, title string) ecs.EntityID {
	t.Helper()
	id, err := core.Create(w, title, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func titles(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Title)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestColumns(t *testing.T) {
	w, now := setup(t)
	ready, groom, blocked, asleep, done := mk(t, w, "ready"), mk(t, w, "groom"), mk(t, w, "blocked"), mk(t, w, "asleep"), mk(t, w, "done")
	both := mk(t, w, "sleeping+blocked")
	core.FlagForGrooming(w, groom)
	dag.Link(w, ready, blocked)
	dag.Link(w, ready, both)
	core.Snooze(w, asleep, now.Add(time.Hour))
	core.Snooze(w, both, now.Add(time.Hour))
	core.FlagForGrooming(w, blocked) // blocked wins over groom
	core.MarkDone(w, done)

	want := map[Column][]string{
		ColGroom:    {"groom"},
		ColReady:    {"ready"},
		ColBlocked:  {"blocked"},
		ColSleeping: {"asleep", "sleeping+blocked"},
		ColDone:     {"done"},
	}
	w.Read(func(r ecs.Reader) {
		b := Board(r, now)
		for c, names := range want {
			if got := titles(b[c]); !eq(got, names) {
				t.Errorf("%s = %v, want %v", c, got, names)
			}
		}
	})
}

func TestMove(t *testing.T) {
	w, now := setup(t)
	a, b, c := mk(t, w, "a"), mk(t, w, "b"), mk(t, w, "c")
	ready := func() []string {
		var got []string
		w.Read(func(r ecs.Reader) { got = titles(Board(r, now)[ColReady]) })
		return got
	}
	if got := ready(); !eq(got, []string{"a", "b", "c"}) {
		t.Fatalf("initial = %v", got)
	}
	if err := Move(w, c, -1); err != nil { // c above b
		t.Fatal(err)
	}
	if got := ready(); !eq(got, []string{"a", "c", "b"}) {
		t.Errorf("after c up = %v", got)
	}
	if err := Move(w, c, -1); err != nil { // c to top
		t.Fatal(err)
	}
	if got := ready(); !eq(got, []string{"c", "a", "b"}) {
		t.Errorf("after c up again = %v", got)
	}
	if err := Move(w, c, -1); !errors.Is(err, ErrNoMove) {
		t.Errorf("top: %v", err)
	}
	if err := Move(w, a, 1); err != nil { // a below b (b unpositioned)
		t.Fatal(err)
	}
	if got := ready(); !eq(got, []string{"c", "b", "a"}) {
		t.Errorf("after a down = %v", got)
	}
	// Done drops Position via cross-system reduction; reopen lands at the end.
	core.MarkDone(w, c)
	w.Read(func(r ecs.Reader) {
		if r.Has(c, Position) {
			t.Error("done task kept its Position")
		}
	})
	core.Reopen(w, c)
	if got := ready(); !eq(got, []string{"b", "a", "c"}) {
		t.Errorf("after reopen = %v", got)
	}
	_ = b
}
