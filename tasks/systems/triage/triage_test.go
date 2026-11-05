package triage

import (
	"testing"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/systems/dag"
)

func TestDeck(t *testing.T) {
	w, err := ecs.New(&ecs.MemLog{}, core.System{}, dag.System{}, System{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	mk := func(title string) ecs.EntityID {
		id, err := core.Create(w, title, "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ready, done, asleep, flagged, blocker, blocked := mk("ready"), mk("done"), mk("asleep"), mk("flagged"), mk("blocker"), mk("blocked")
	core.MarkDone(w, done)
	core.Snooze(w, asleep, now.Add(time.Hour))
	core.FlagForGrooming(w, flagged)
	dag.Link(w, blocker, blocked)

	deck := func(at time.Time) map[ecs.EntityID]bool {
		got := map[ecs.EntityID]bool{}
		w.Read(func(r ecs.Reader) {
			for _, task := range Deck(r, at, "") {
				got[task.ID] = true
			}
		})
		return got
	}
	got := deck(now)
	if !got[ready] || !got[blocker] || got[done] || got[asleep] || got[flagged] || got[blocked] {
		t.Errorf("deck = %v", got)
	}
	if !deck(now.Add(2 * time.Hour))[asleep] {
		t.Error("sleep should expire by derivation")
	}

	// Same day → same order; different day may differ but same members.
	order := func(at time.Time) []ecs.EntityID {
		var ids []ecs.EntityID
		w.Read(func(r ecs.Reader) {
			for _, task := range Deck(r, at, "") {
				ids = append(ids, task.ID)
			}
		})
		return ids
	}
	// 30 min later: same members (asleep still sleeps), must be same order.
	a, b := order(now), order(now.Add(30*time.Minute))
	if len(a) != len(b) {
		t.Fatalf("deck size changed: %v vs %v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("deck reshuffled within a day: %v vs %v", a, b)
		}
	}

	// Scope narrows the deck to tagged tasks; "" is everything.
	core.AddTag(w, ready, "home")
	var scoped []ecs.EntityID
	w.Read(func(r ecs.Reader) {
		for _, task := range Deck(r, now, "home") {
			scoped = append(scoped, task.ID)
		}
	})
	if len(scoped) != 1 || scoped[0] != ready {
		t.Errorf("scoped deck = %v, want [%s]", scoped, ready)
	}
}
