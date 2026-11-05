// Package core is the state-owning heart of the tasks app: the
// components every view needs the same way (title, body, done, sleep,
// groom flag, tags) and the events that change them. Other systems
// read these; only core writes them.
package core

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
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
	// Tags is a sorted set of plain labels the user put on the task —
	// the generic grouping vocabulary every view narrows by (scope),
	// and the hook a future view can key off (one board per tag).
	// Any system may read it; only core writes it, and only from a
	// user action. A system that wants to write a tag programmatically
	// wants its own kind instead. The vocabulary is derived (Vocabulary).
	Tags ecs.Kind = "core.Tags" // []string
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
	EvTagged             = "Tagged"
	EvUntagged           = "Untagged"
)

// Payloads.
type (
	Created struct {
		Title string   `json:"title"`
		Body  string   `json:"body,omitempty"`
		Tags  []string `json:"tags,omitempty"` // so "create in scope" is one event
	}
	// Edited is per field so the narrative can say "renamed" vs "rewrote".
	Edited struct {
		Field string `json:"field"` // "title" | "body"
		Value string `json:"value"`
	}
	Snoozed struct {
		Until time.Time `json:"until"`
	}
	// Tag is Tagged's and Untagged's payload.
	Tag struct {
		Tag string `json:"tag"`
	}
)

// System implements ecs.System.
type System struct{}

func (System) Name() string { return Name }

func (System) Kinds() []ecs.Kind {
	return []ecs.Kind{Title, Body, CreatedAt, Done, SleepUntil, NeedsGrooming, Tags}
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
		tags, err := normalizeTags(p.Tags)
		if err != nil {
			return err
		}
		return first(
			w.Set(e, Title, p.Title),
			w.Set(e, Body, p.Body),
			w.Set(e, CreatedAt, ev.At),
			setTags(w, e, tags),
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
	case EvTagged, EvUntagged:
		var p Tag
		if err := ev.Decode(&p); err != nil {
			return err
		}
		tag, err := NormalizeTag(p.Tag)
		if err != nil {
			return err
		}
		tags, _ := ecs.Get[[]string](r, e, Tags)
		i, has := slices.BinarySearch(tags, tag)
		if ev.Type == EvTagged {
			if has {
				return fmt.Errorf("already tagged %q", tag)
			}
			tags = slices.Insert(slices.Clone(tags), i, tag)
		} else {
			if !has {
				return fmt.Errorf("not tagged %q", tag)
			}
			tags = slices.Delete(slices.Clone(tags), i, i+1)
		}
		return setTags(w, e, tags)
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

// setTags stores the set, or removes the component when empty so an
// untagged task carries no core.Tags at all.
func setTags(w ecs.Writer, e ecs.EntityID, tags []string) error {
	if len(tags) == 0 {
		return w.Remove(e, Tags)
	}
	return w.Set(e, Tags, tags)
}

// ErrBadTag is returned for a tag that normalizes to nothing or
// contains a separator.
var ErrBadTag = errors.New("core: bad tag")

// NormalizeTag is the one spelling of a tag: trimmed, lower-cased,
// no whitespace or commas (those separate tags in forms).
func NormalizeTag(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || strings.ContainsAny(s, ", \t\n") {
		return "", fmt.Errorf("%w: %q", ErrBadTag, s)
	}
	return s, nil
}

// normalizeTags normalizes, dedupes and sorts.
func normalizeTags(in []string) ([]string, error) {
	var out []string
	for _, s := range in {
		t, err := NormalizeTag(s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// ParseTags splits a form value ("home, repo work") into tags.
func ParseTags(s string) ([]string, error) {
	return normalizeTags(strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }))
}

// --- raise helpers: the typed write side any UI may call -------------

// Create raises core.Created on a fresh entity.
func Create(w *ecs.World, title, body string, tags ...string) (ecs.EntityID, error) {
	id := ecs.NewEntityID()
	_, err := w.Raise(Name, EvCreated, id, Created{Title: title, Body: body, Tags: tags})
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
func AddTag(w *ecs.World, id ecs.EntityID, tag string) error {
	return raise(w, id, EvTagged, Tag{Tag: tag})
}
func RemoveTag(w *ecs.World, id ecs.EntityID, tag string) error {
	return raise(w, id, EvUntagged, Tag{Tag: tag})
}

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
	Tags          []string
}

// Has reports whether the task carries tag (already normalized).
func (t Task) Has(tag string) bool {
	_, ok := slices.BinarySearch(t.Tags, tag)
	return ok
}

// InScope reports whether the task is visible under scope: every task
// when scope is "", else those tagged scope. The predicate every view
// narrows by.
func (t Task) InScope(scope string) bool { return scope == "" || t.Has(scope) }

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
	t.Tags, _ = ecs.Get[[]string](r, id, Tags)
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

// Scoped returns every task in scope, oldest first.
func Scoped(r ecs.Reader, scope string) []Task {
	all := All(r)
	if scope == "" {
		return all
	}
	out := all[:0]
	for _, t := range all {
		if t.InScope(scope) {
			out = append(out, t)
		}
	}
	return out
}

// Vocabulary is the derived set of tags in use (on any task, done or
// not), sorted. Never stored: a tag exists while a task carries it.
func Vocabulary(r ecs.Reader) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range r.Entities(Tags) {
		tags, _ := ecs.Get[[]string](r, id, Tags)
		for _, t := range tags {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	slices.Sort(out)
	return out
}
