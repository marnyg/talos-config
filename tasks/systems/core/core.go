// Package core is the state-owning heart of the tasks app: the
// components every view needs the same way (title, body, done, sleep,
// groom flag) and the events that change them. Other systems read
// these; only core writes them.
package core

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
)

// Name is the system namespace.
const Name = "core"

// Component kinds owned by core.
const (
	Title         ecs.Kind = "core.Title"         // string
	Body          ecs.Kind = "core.Body"          // string
	CreatedAt     ecs.Kind = "core.CreatedAt"     // time.Time
	Done          ecs.Kind = "core.Done"          // time.Time (when)
	SleepUntil    ecs.Kind = "core.SleepUntil"    // time.Time
	NeedsGrooming ecs.Kind = "core.NeedsGrooming" // time.Time (when flagged)
)

// Event types (namespace core.*).
const (
	EvCreated            = "Created"
	EvEdited             = "Edited"
	EvDone               = "Done"
	EvReopened           = "Reopened"
	EvSnoozed            = "Snoozed"
	EvWoken              = "Woken"
	EvFlaggedForGrooming = "FlaggedForGrooming"
	EvGroomed            = "Groomed"
)

// Payloads.
type (
	Created struct {
		Title string `json:"title"`
		Body  string `json:"body,omitempty"`
	}
	// Edited is per field so the narrative can say "renamed" vs "rewrote".
	Edited struct {
		Field string `json:"field"` // "title" | "body"
		Value string `json:"value"`
	}
	Snoozed struct {
		Until time.Time `json:"until"`
	}
)

// System implements ecs.System.
type System struct{}

func (System) Name() string { return Name }

func (System) Kinds() []ecs.Kind {
	return []ecs.Kind{Title, Body, CreatedAt, Done, SleepUntil, NeedsGrooming}
}

func (System) Reduce(ev ecs.Event, r ecs.Reader, w ecs.Writer) error {
	if ev.System != Name {
		return nil
	}
	e := ev.Entity
	if e == "" {
		return errors.New("event without entity")
	}
	switch ev.Type {
	case EvCreated:
		var p Created
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if p.Title == "" {
			return errors.New("empty title")
		}
		if r.Has(e, Title) {
			return fmt.Errorf("entity %s already created", e)
		}
		return first(
			w.Set(e, Title, p.Title),
			w.Set(e, Body, p.Body),
			w.Set(e, CreatedAt, ev.At),
		)
	case EvEdited:
		var p Edited
		if err := ev.Decode(&p); err != nil {
			return err
		}
		switch p.Field {
		case "title":
			if p.Value == "" {
				return errors.New("empty title")
			}
			return w.Set(e, Title, p.Value)
		case "body":
			return w.Set(e, Body, p.Value)
		default:
			return fmt.Errorf("unknown field %q", p.Field)
		}
	case EvDone:
		return w.Set(e, Done, ev.At)
	case EvReopened:
		return w.Remove(e, Done)
	case EvSnoozed:
		var p Snoozed
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if p.Until.IsZero() {
			return errors.New("snooze without until")
		}
		return w.Set(e, SleepUntil, p.Until)
	case EvWoken:
		return w.Remove(e, SleepUntil)
	case EvFlaggedForGrooming:
		return w.Set(e, NeedsGrooming, ev.At)
	case EvGroomed:
		return w.Remove(e, NeedsGrooming)
	default:
		return fmt.Errorf("unknown event %s", ev.Name())
	}
}

func first(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// --- raise helpers: the typed write side any UI may call -------------

// Create raises core.Created on a fresh entity.
func Create(w *ecs.World, title, body string) (ecs.EntityID, error) {
	id := ecs.NewEntityID()
	_, err := w.Raise(Name, EvCreated, id, Created{Title: title, Body: body})
	return id, err
}

// Edit raises core.Edited for one field.
func Edit(w *ecs.World, id ecs.EntityID, field, value string) error {
	return raise(w, id, EvEdited, Edited{Field: field, Value: value})
}

func MarkDone(w *ecs.World, id ecs.EntityID) error { return raise(w, id, EvDone, nil) }
func Reopen(w *ecs.World, id ecs.EntityID) error   { return raise(w, id, EvReopened, nil) }
func Snooze(w *ecs.World, id ecs.EntityID, until time.Time) error {
	return raise(w, id, EvSnoozed, Snoozed{Until: until.UTC()})
}
func Wake(w *ecs.World, id ecs.EntityID) error { return raise(w, id, EvWoken, nil) }
func FlagForGrooming(w *ecs.World, id ecs.EntityID) error {
	return raise(w, id, EvFlaggedForGrooming, nil)
}
func Groom(w *ecs.World, id ecs.EntityID) error { return raise(w, id, EvGroomed, nil) }

// ErrNoSuchTask is returned when the entity carries no core.Title.
var ErrNoSuchTask = errors.New("core: no such task")

func raise(w *ecs.World, id ecs.EntityID, typ string, payload any) error {
	exists := false
	w.Read(func(r ecs.Reader) { exists = r.Has(id, Title) })
	if !exists {
		return ErrNoSuchTask
	}
	_, err := w.Raise(Name, typ, id, payload)
	return err
}

// --- derivations -------------------------------------------------------

// Task is the read-side projection of one entity's core components.
type Task struct {
	ID            ecs.EntityID
	Title         string
	Body          string
	CreatedAt     time.Time
	Done          *time.Time
	SleepUntil    *time.Time
	NeedsGrooming bool
}

// Sleeping is derived from SleepUntil and now; never stored.
func (t Task) Sleeping(now time.Time) bool {
	return t.SleepUntil != nil && t.SleepUntil.After(now)
}

// IsDone reports whether core.Done is attached.
func (t Task) IsDone() bool { return t.Done != nil }

// Load projects entity id into a Task.
func Load(r ecs.Reader, id ecs.EntityID) (Task, bool) {
	title, ok := ecs.Get[string](r, id, Title)
	if !ok {
		return Task{}, false
	}
	t := Task{ID: id, Title: title}
	t.Body, _ = ecs.Get[string](r, id, Body)
	t.CreatedAt, _ = ecs.Get[time.Time](r, id, CreatedAt)
	if v, ok := ecs.Get[time.Time](r, id, Done); ok {
		t.Done = &v
	}
	if v, ok := ecs.Get[time.Time](r, id, SleepUntil); ok {
		t.SleepUntil = &v
	}
	t.NeedsGrooming = r.Has(id, NeedsGrooming)
	return t, true
}

// All returns every task, oldest first.
func All(r ecs.Reader) []Task {
	ids := r.Entities(Title)
	out := make([]Task, 0, len(ids))
	for _, id := range ids {
		if t, ok := Load(r, id); ok {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
