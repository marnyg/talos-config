// Package dag owns dependency edges. An edge is its own entity carrying
// dag.Edge{from, to, kind}; "from blocks to" is the only kind. The
// reducer keeps the graph acyclic and duplicate-free; everything else —
// blocked, ready, topological layers — is derived at read time.
package dag

import (
	"errors"
	"fmt"
	"sort"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
)

const Name = "dag"

// Edge is the one component dag owns, attached to edge entities.
const Edge ecs.Kind = "dag.Edge"

// KindBlocks is the only edge kind: From must be done before To is ready.
const KindBlocks = "blocks"

// EdgeData is both the component payload and the Linked payload.
type EdgeData struct {
	From ecs.EntityID `json:"from"`
	To   ecs.EntityID `json:"to"`
	Kind string       `json:"kind"`
}

// Event types (namespace dag.*). Both carry the edge entity id and an
// EdgeData payload, so an Unlinked is readable after the edge is gone.
const (
	EvLinked   = "Linked"
	EvUnlinked = "Unlinked"
)

var (
	ErrSelfEdge   = errors.New("dag: a task cannot block itself")
	ErrCycle      = errors.New("dag: edge would create a cycle")
	ErrDuplicate  = errors.New("dag: edge already exists")
	ErrNoSuchEdge = errors.New("dag: no such edge")
)

// System implements ecs.System.
type System struct{}

func (System) Name() string      { return Name }
func (System) Kinds() []ecs.Kind { return []ecs.Kind{Edge} }

func (System) Reduce(ev ecs.Event, r ecs.Reader, w ecs.Writer) error {
	if ev.System != Name {
		return nil
	}
	switch ev.Type {
	case EvLinked:
		var p EdgeData
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if ev.Entity == "" {
			return errors.New("dag: Linked without edge entity")
		}
		if p.Kind != KindBlocks {
			return fmt.Errorf("dag: unknown edge kind %q", p.Kind)
		}
		if p.From == p.To {
			return ErrSelfEdge
		}
		for _, id := range []ecs.EntityID{p.From, p.To} {
			if !r.Has(id, core.Title) {
				return fmt.Errorf("%w: %s", core.ErrNoSuchTask, id)
			}
		}
		g := Load(r)
		if g.Find(p.From, p.To) != "" {
			return ErrDuplicate
		}
		if g.Reaches(p.To, p.From) {
			return ErrCycle
		}
		return w.Set(ev.Entity, Edge, p)
	case EvUnlinked:
		cur, ok := ecs.Get[EdgeData](r, ev.Entity, Edge)
		if !ok {
			return ErrNoSuchEdge
		}
		var p EdgeData
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if p != cur {
			return fmt.Errorf("dag: Unlinked payload %+v does not match edge %+v", p, cur)
		}
		return w.Remove(ev.Entity, Edge)
	default:
		return fmt.Errorf("unknown event %s", ev.Name())
	}
}

// --- raise helpers -----------------------------------------------------

// Link raises dag.Linked: from blocks to. Returns the edge entity id.
func Link(w *ecs.World, from, to ecs.EntityID) (ecs.EntityID, error) {
	id := ecs.NewEntityID()
	_, err := w.Raise(Name, EvLinked, id, EdgeData{From: from, To: to, Kind: KindBlocks})
	return id, err
}

// Unlink raises dag.Unlinked for an edge entity, echoing its data.
func Unlink(w *ecs.World, edge ecs.EntityID) error {
	var (
		d  EdgeData
		ok bool
	)
	w.Read(func(r ecs.Reader) { d, ok = ecs.Get[EdgeData](r, edge, Edge) })
	if !ok {
		return ErrNoSuchEdge
	}
	_, err := w.Raise(Name, EvUnlinked, edge, d)
	return err
}

// --- derivations -------------------------------------------------------

// Graph is an index over every edge, built per read.
type Graph struct {
	edges map[ecs.EntityID]EdgeData       // edge id → data
	out   map[ecs.EntityID][]ecs.EntityID // task → edge ids it is From of
	in    map[ecs.EntityID][]ecs.EntityID // task → edge ids it is To of
}

// Load indexes all edges.
func Load(r ecs.Reader) Graph {
	g := Graph{
		edges: map[ecs.EntityID]EdgeData{},
		out:   map[ecs.EntityID][]ecs.EntityID{},
		in:    map[ecs.EntityID][]ecs.EntityID{},
	}
	for _, id := range r.Entities(Edge) {
		d, ok := ecs.Get[EdgeData](r, id, Edge)
		if !ok {
			continue
		}
		g.edges[id] = d
		g.out[d.From] = append(g.out[d.From], id)
		g.in[d.To] = append(g.in[d.To], id)
	}
	return g
}

// Find returns the edge id for from→to, or "".
func (g Graph) Find(from, to ecs.EntityID) ecs.EntityID {
	for _, e := range g.out[from] {
		if g.edges[e].To == to {
			return e
		}
	}
	return ""
}

// Reaches reports whether a path of blocks-edges leads from a to b.
func (g Graph) Reaches(a, b ecs.EntityID) bool {
	seen := map[ecs.EntityID]bool{}
	var walk func(ecs.EntityID) bool
	walk = func(n ecs.EntityID) bool {
		if n == b {
			return true
		}
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, e := range g.out[n] {
			if walk(g.edges[e].To) {
				return true
			}
		}
		return false
	}
	return walk(a)
}

// Rel is one edge as seen from a task: the other end and the edge id.
type Rel struct {
	Edge  ecs.EntityID
	Other ecs.EntityID
}

// BlockedBy lists every incoming edge (done blockers included — the
// caller decides what to show). Sorted by other id.
func (g Graph) BlockedBy(id ecs.EntityID) []Rel {
	return g.rels(g.in[id], func(d EdgeData) ecs.EntityID { return d.From })
}

// Blocks lists every outgoing edge.
func (g Graph) Blocks(id ecs.EntityID) []Rel {
	return g.rels(g.out[id], func(d EdgeData) ecs.EntityID { return d.To })
}

func (g Graph) rels(edges []ecs.EntityID, other func(EdgeData) ecs.EntityID) []Rel {
	out := make([]Rel, 0, len(edges))
	for _, e := range edges {
		out = append(out, Rel{Edge: e, Other: other(g.edges[e])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Other < out[j].Other })
	return out
}

// Blockers returns the ids of open (not Done) tasks blocking id.
func (g Graph) Blockers(r ecs.Reader, id ecs.EntityID) []ecs.EntityID {
	var out []ecs.EntityID
	for _, rel := range g.BlockedBy(id) {
		if !r.Has(rel.Other, core.Done) {
			out = append(out, rel.Other)
		}
	}
	return out
}

// Blocked is the derivation every view shares: at least one open blocker.
func (g Graph) Blocked(r ecs.Reader, id ecs.EntityID) bool {
	return len(g.Blockers(r, id)) > 0
}

// Layers groups the open tasks topologically: layer 0 has no open
// blockers, layer n+1 is unblocked once layer ≤ n is done. Tasks are
// ordered as core.All within a layer.
func (g Graph) Layers(r ecs.Reader) [][]core.Task {
	depth := map[ecs.EntityID]int{}
	var walk func(ecs.EntityID) int
	walk = func(id ecs.EntityID) int {
		if d, ok := depth[id]; ok {
			return d
		}
		depth[id] = 0 // acyclic by construction; guards against bad data
		d := 0
		for _, b := range g.Blockers(r, id) {
			if bd := walk(b) + 1; bd > d {
				d = bd
			}
		}
		depth[id] = d
		return d
	}
	var layers [][]core.Task
	for _, t := range core.All(r) {
		if t.IsDone() {
			continue
		}
		d := walk(t.ID)
		for len(layers) <= d {
			layers = append(layers, nil)
		}
		layers[d] = append(layers[d], t)
	}
	return layers
}
