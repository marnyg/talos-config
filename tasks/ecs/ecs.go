// Package ecs is the entity/component/event core of the tasks app
// (tasks/docs/desired-state/domain-model.md).
//
//   - An Entity is a bare id.
//   - A Component is a JSON payload keyed (entity, Kind); Kind is
//     namespaced by the System that owns it ("core.Done").
//   - An Event is an intentful, append-only fact in the Log — the only
//     source of truth. Components are a cache rebuilt by replay.
//   - A System owns Kinds and reduces Events into them through a Writer
//     scoped to those Kinds. It may read every Kind. Reducers never
//     emit events and see the pre-event state (mutations from all
//     systems are applied after every reducer ran), so reducer order
//     is irrelevant and replay is deterministic.
package ecs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// EntityID is an opaque id. Empty means "no entity" on an Event.
type EntityID string

// NewEntityID returns a fresh random id.
func NewEntityID() EntityID {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return EntityID(hex.EncodeToString(b[:]))
}

// Kind names a component type, "<system>.<Name>".
type Kind string

// System returns the owning namespace of k ("" if malformed).
func (k Kind) System() string {
	s, _, ok := strings.Cut(string(k), ".")
	if !ok {
		return ""
	}
	return s
}

// Event is one appended fact. System+Type name it ("core.Snoozed");
// System is the namespace whose reducer is canonical, not whichever
// UI raised it.
type Event struct {
	Seq     int64           `json:"seq"`
	At      time.Time       `json:"at"`
	System  string          `json:"system"`
	Type    string          `json:"type"`
	Entity  EntityID        `json:"entity,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Name is "<system>.<type>".
func (e Event) Name() string { return e.System + "." + e.Type }

// Is reports whether the event is system.typ.
func (e Event) Is(system, typ string) bool { return e.System == system && e.Type == typ }

// Decode unmarshals the payload into v.
func (e Event) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

// Log is the append-only event store.
type Log interface {
	// Append stores ev (Seq ignored on input) and returns its Seq.
	Append(ev Event) (int64, error)
	// Replay calls fn for every event in Seq order.
	Replay(fn func(Event) error) error
}

// Reader is read access to every component of every system.
type Reader interface {
	Get(e EntityID, k Kind) (json.RawMessage, bool)
	Has(e EntityID, k Kind) bool
	// Entities returns the ids carrying k, sorted.
	Entities(k Kind) []EntityID
}

// Get decodes component k of e into T.
func Get[T any](r Reader, e EntityID, k Kind) (T, bool) {
	var v T
	raw, ok := r.Get(e, k)
	if !ok {
		return v, false
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, false
	}
	return v, true
}

// Writer is write access scoped to one system's Kinds.
type Writer interface {
	Set(e EntityID, k Kind, v any) error
	Remove(e EntityID, k Kind) error
}

// System is a module: it owns Kinds and reduces events into them.
type System interface {
	Name() string
	Kinds() []Kind
	Reduce(ev Event, r Reader, w Writer) error
}

// ErrNotOwner is returned by a Writer for a Kind the system does not own.
var ErrNotOwner = errors.New("ecs: kind not owned by system")

// ErrUnknownSystem is returned by Raise for an unregistered namespace.
var ErrUnknownSystem = errors.New("ecs: unknown system")

// World holds the registered systems, the log and the rebuilt
// component tables.
type World struct {
	mu      sync.RWMutex
	log     Log
	systems []System
	owner   map[Kind]string
	comps   map[Kind]map[EntityID]json.RawMessage
	Now     func() time.Time
}

// New registers systems, checks Kind ownership and replays the log.
func New(log Log, systems ...System) (*World, error) {
	w := &World{
		log:     log,
		systems: systems,
		owner:   map[Kind]string{},
		comps:   map[Kind]map[EntityID]json.RawMessage{},
		Now:     time.Now,
	}
	names := map[string]bool{}
	for _, s := range systems {
		if names[s.Name()] {
			return nil, fmt.Errorf("ecs: duplicate system %q", s.Name())
		}
		names[s.Name()] = true
		for _, k := range s.Kinds() {
			if k.System() != s.Name() {
				return nil, fmt.Errorf("ecs: system %q declares kind %q outside its namespace", s.Name(), k)
			}
			if o, dup := w.owner[k]; dup {
				return nil, fmt.Errorf("ecs: kind %q owned by both %q and %q", k, o, s.Name())
			}
			w.owner[k] = s.Name()
		}
	}
	err := log.Replay(func(ev Event) error {
		ops, err := w.reduce(ev)
		if err != nil {
			return fmt.Errorf("replay seq %d %s: %w", ev.Seq, ev.Name(), err)
		}
		w.apply(ops)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

// Raise appends one event and reduces it. payload may be nil.
// Reducers run before the append; if any rejects the event nothing is
// stored.
func (w *World) Raise(system, typ string, entity EntityID, payload any) (Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.known(system) {
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownSystem, system)
	}
	ev := Event{At: w.Now().UTC(), System: system, Type: typ, Entity: entity}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Event{}, err
		}
		ev.Payload = raw
	}
	ops, err := w.reduce(ev)
	if err != nil {
		return Event{}, fmt.Errorf("%s: %w", ev.Name(), err)
	}
	seq, err := w.log.Append(ev)
	if err != nil {
		return Event{}, fmt.Errorf("append %s: %w", ev.Name(), err)
	}
	ev.Seq = seq
	w.apply(ops)
	return ev, nil
}

// Read runs fn with a consistent read view.
func (w *World) Read(fn func(Reader)) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	fn(view{w.comps})
}

// History replays the log, calling fn for events touching entity
// (or every event when entity is "").
func (w *World) History(entity EntityID, fn func(Event)) error {
	return w.log.Replay(func(ev Event) error {
		if entity == "" || ev.Entity == entity {
			fn(ev)
		}
		return nil
	})
}

func (w *World) known(system string) bool {
	for _, s := range w.systems {
		if s.Name() == system {
			return true
		}
	}
	return false
}

type op struct {
	e   EntityID
	k   Kind
	v   json.RawMessage
	del bool
}

// reduce runs every system's reducer against the pre-event state and
// collects their mutations. Caller holds the lock.
func (w *World) reduce(ev Event) ([]op, error) {
	var ops []op
	r := view{w.comps}
	for _, s := range w.systems {
		sw := &scopedWriter{sys: s.Name(), owner: w.owner}
		if err := s.Reduce(ev, r, sw); err != nil {
			return nil, fmt.Errorf("system %s: %w", s.Name(), err)
		}
		ops = append(ops, sw.ops...)
	}
	return ops, nil
}

func (w *World) apply(ops []op) {
	for _, o := range ops {
		m := w.comps[o.k]
		if o.del {
			delete(m, o.e)
			continue
		}
		if m == nil {
			m = map[EntityID]json.RawMessage{}
			w.comps[o.k] = m
		}
		m[o.e] = o.v
	}
}

type scopedWriter struct {
	sys   string
	owner map[Kind]string
	ops   []op
}

func (s *scopedWriter) check(k Kind) error {
	if s.owner[k] != s.sys {
		return fmt.Errorf("%w: %s writing %s", ErrNotOwner, s.sys, k)
	}
	return nil
}

func (s *scopedWriter) Set(e EntityID, k Kind, v any) error {
	if err := s.check(k); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.ops = append(s.ops, op{e: e, k: k, v: raw})
	return nil
}

func (s *scopedWriter) Remove(e EntityID, k Kind) error {
	if err := s.check(k); err != nil {
		return err
	}
	s.ops = append(s.ops, op{e: e, k: k, del: true})
	return nil
}

type view struct {
	comps map[Kind]map[EntityID]json.RawMessage
}

func (v view) Get(e EntityID, k Kind) (json.RawMessage, bool) {
	raw, ok := v.comps[k][e]
	return raw, ok
}

func (v view) Has(e EntityID, k Kind) bool {
	_, ok := v.comps[k][e]
	return ok
}

func (v view) Entities(k Kind) []EntityID {
	m := v.comps[k]
	out := make([]EntityID, 0, len(m))
	for e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MemLog is an in-memory Log for tests.
type MemLog struct {
	mu     sync.Mutex
	Events []Event
}

func (l *MemLog) Append(ev Event) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ev.Seq = int64(len(l.Events) + 1)
	l.Events = append(l.Events, ev)
	return ev.Seq, nil
}

func (l *MemLog) Replay(fn func(Event) error) error {
	l.mu.Lock()
	evs := append([]Event(nil), l.Events...)
	l.mu.Unlock()
	for _, ev := range evs {
		if err := fn(ev); err != nil {
			return err
		}
	}
	return nil
}
