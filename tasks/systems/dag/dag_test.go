package dag

import (
	"errors"
	"testing"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
)

func setup(t *testing.T) (*ecs.World, *ecs.MemLog, [3]ecs.EntityID) {
	t.Helper()
	lg := &ecs.MemLog{}
	w, err := ecs.New(lg, core.System{}, System{})
	if err != nil {
		t.Fatal(err)
	}
	var ids [3]ecs.EntityID
	for i, title := range []string{"a", "b", "c"} {
		ids[i], err = core.Create(w, title, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	return w, lg, ids
}

func graph(w *ecs.World, fn func(ecs.Reader, Graph)) {
	w.Read(func(r ecs.Reader) { fn(r, Load(r)) })
}

func TestLinkRules(t *testing.T) {
	w, lg, ids := setup(t)
	a, b, c := ids[0], ids[1], ids[2]

	if _, err := Link(w, a, b); err != nil { // a blocks b
		t.Fatal(err)
	}
	if _, err := Link(w, b, c); err != nil { // b blocks c
		t.Fatal(err)
	}
	n := len(lg.Events)
	cases := []struct {
		name     string
		from, to ecs.EntityID
		want     error
	}{
		{"self", a, a, ErrSelfEdge},
		{"duplicate", a, b, ErrDuplicate},
		{"direct cycle", b, a, ErrCycle},
		{"transitive cycle", c, a, ErrCycle},
		{"unknown task", a, "nope", core.ErrNoSuchTask},
	}
	for _, tc := range cases {
		if _, err := Link(w, tc.from, tc.to); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	if len(lg.Events) != n {
		t.Errorf("rejected links reached the log: %d → %d", n, len(lg.Events))
	}
	// a→c alongside a→b→c is fine (diamond, not cycle).
	if _, err := Link(w, a, c); err != nil {
		t.Errorf("a→c: %v", err)
	}
}

func TestDerivations(t *testing.T) {
	w, _, ids := setup(t)
	a, b, c := ids[0], ids[1], ids[2]
	ab, _ := Link(w, a, b)
	Link(w, b, c)

	graph(w, func(r ecs.Reader, g Graph) {
		if g.Blocked(r, a) || !g.Blocked(r, b) || !g.Blocked(r, c) {
			t.Error("blocked: want only b, c")
		}
		layers := g.Layers(r)
		if len(layers) != 3 || layers[0][0].ID != a || layers[1][0].ID != b || layers[2][0].ID != c {
			t.Errorf("layers = %v", titles(layers))
		}
		if rels := g.Blocks(a); len(rels) != 1 || rels[0].Other != b || rels[0].Edge != ab {
			t.Errorf("Blocks(a) = %+v", rels)
		}
	})

	// Done blockers stop blocking — by derivation, no edge event.
	if err := core.MarkDone(w, a); err != nil {
		t.Fatal(err)
	}
	graph(w, func(r ecs.Reader, g Graph) {
		if g.Blocked(r, b) {
			t.Error("b still blocked by done a")
		}
		if len(g.BlockedBy(b)) != 1 {
			t.Error("edge a→b should still exist")
		}
		if layers := g.Layers(r); len(layers) != 2 || layers[0][0].ID != b {
			t.Errorf("layers after a done = %v", titles(layers))
		}
	})

	// Unlink frees c; the edge entity loses its component.
	var bc ecs.EntityID
	graph(w, func(r ecs.Reader, g Graph) { bc = g.Find(b, c) })
	if err := Unlink(w, bc); err != nil {
		t.Fatal(err)
	}
	if err := Unlink(w, bc); !errors.Is(err, ErrNoSuchEdge) {
		t.Errorf("double unlink: %v", err)
	}
	graph(w, func(r ecs.Reader, g Graph) {
		if g.Blocked(r, c) || r.Has(bc, Edge) {
			t.Error("c still blocked after unlink")
		}
	})
}

func titles(layers [][]core.Task) [][]string {
	var out [][]string
	for _, l := range layers {
		var row []string
		for _, t := range l {
			row = append(row, t.Title)
		}
		out = append(out, row)
	}
	return out
}
