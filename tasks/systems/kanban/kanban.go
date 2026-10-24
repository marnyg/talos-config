// Package kanban owns manual ordering within a column. Columns
// themselves are derived from core + dag state; only the rank is stored.
package kanban

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/systems/dag"
)

const Name = "kanban"

// Position is the one component kanban owns.
const Position ecs.Kind = "kanban.Position"

// Pos is Position's payload and Moved's payload: a float rank, lower
// first; new ranks are midpoints so no renumbering is ever needed.
type Pos struct {
	Rank float64 `json:"rank"`
}

const EvMoved = "Moved"

// System implements ecs.System.
type System struct{}

func (System) Name() string      { return Name }
func (System) Kinds() []ecs.Kind { return []ecs.Kind{Position} }

func (System) Reduce(ev ecs.Event, r ecs.Reader, w ecs.Writer) error {
	switch {
	case ev.Is(Name, EvMoved):
		var p Pos
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if !r.Has(ev.Entity, core.Title) {
			return core.ErrNoSuchTask
		}
		return w.Set(ev.Entity, Position, p)
	case ev.Is(core.Name, core.EvDone):
		// A done task leaves its column; its rank means nothing now.
		if r.Has(ev.Entity, Position) {
			return w.Remove(ev.Entity, Position)
		}
	case ev.System == Name:
		return fmt.Errorf("unknown event %s", ev.Name())
	}
	return nil
}

// --- columns (derived) -------------------------------------------------

// Column is derived per task; precedence done > sleeping > blocked >
// groom > ready.
type Column string

const (
	ColGroom    Column = "groom"
	ColReady    Column = "ready"
	ColBlocked  Column = "blocked"
	ColSleeping Column = "sleeping"
	ColDone     Column = "done"
)

// Columns is the display order.
var Columns = []Column{ColGroom, ColReady, ColBlocked, ColSleeping, ColDone}

// ColumnOf classifies one task.
func ColumnOf(r ecs.Reader, g dag.Graph, t core.Task, now time.Time) Column {
	switch {
	case t.IsDone():
		return ColDone
	case t.Sleeping(now):
		return ColSleeping
	case g.Blocked(r, t.ID):
		return ColBlocked
	case t.NeedsGrooming:
		return ColGroom
	default:
		return ColReady
	}
}

// Entry is a task with its effective rank in its column.
type Entry struct {
	core.Task
	Rank       float64
	Positioned bool
}

// Board groups every task by column, ordered. Done is newest-done
// first; the rest are by effective rank (see Ranked).
func Board(r ecs.Reader, now time.Time) map[Column][]Entry {
	g := dag.Load(r)
	by := map[Column][]core.Task{}
	for _, t := range core.All(r) {
		c := ColumnOf(r, g, t, now)
		by[c] = append(by[c], t)
	}
	out := map[Column][]Entry{}
	for c, tasks := range by {
		if c == ColDone {
			sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Done.After(*tasks[j].Done) })
			for _, t := range tasks {
				out[c] = append(out[c], Entry{Task: t})
			}
			continue
		}
		out[c] = Ranked(r, tasks)
	}
	return out
}

// Ranked orders tasks by effective rank: the stored Position, or the
// creation time (unix seconds, fractional) for tasks never moved. One number line
// for both, so a midpoint between any two neighbours is a valid rank
// and an unmoved task keeps its place when a neighbour is moved.
func Ranked(r ecs.Reader, tasks []core.Task) []Entry {
	out := make([]Entry, 0, len(tasks))
	for _, t := range tasks {
		e := Entry{Task: t, Rank: float64(t.CreatedAt.UnixNano()) / 1e9}
		if p, ok := ecs.Get[Pos](r, t.ID, Position); ok {
			e.Rank, e.Positioned = p.Rank, true
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

// --- raise helpers -----------------------------------------------------

var ErrNoMove = errors.New("kanban: already at the edge")

// Move raises kanban.Moved to shift id one step up (-1) or down (+1)
// within its current column.
func Move(w *ecs.World, id ecs.EntityID, dir int) error {
	var (
		rank float64
		err  error
	)
	w.Read(func(r ecs.Reader) {
		t, ok := core.Load(r, id)
		if !ok {
			err = core.ErrNoSuchTask
			return
		}
		now := w.Now()
		g := dag.Load(r)
		col := ColumnOf(r, g, t, now)
		if col == ColDone {
			err = errors.New("kanban: done tasks are not ordered")
			return
		}
		var peers []core.Task
		for _, x := range core.All(r) {
			if ColumnOf(r, g, x, now) == col {
				peers = append(peers, x)
			}
		}
		entries := Ranked(r, peers)
		i := -1
		for k, e := range entries {
			if e.ID == id {
				i = k
			}
		}
		j := i + dir
		if i < 0 || j < 0 || j >= len(entries) {
			err = ErrNoMove
			return
		}
		rank, err = between(entries, i, j)
	})
	if err != nil {
		return err
	}
	_, err = w.Raise(Name, EvMoved, id, Pos{Rank: rank})
	return err
}

// between returns a rank that places entries[i] on the far side of
// entries[j]: above it when moving up, below when moving down.
func between(entries []Entry, i, j int) (float64, error) {
	switch {
	case j < i: // moving up: between j-1 and j
		if j == 0 {
			return entries[j].Rank - 1, nil
		}
		return (entries[j-1].Rank + entries[j].Rank) / 2, nil
	default: // moving down: between j and j+1
		if j == len(entries)-1 {
			return entries[j].Rank + 1, nil
		}
		return (entries[j].Rank + entries[j+1].Rank) / 2, nil
	}
}
