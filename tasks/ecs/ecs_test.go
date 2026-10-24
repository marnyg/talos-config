package ecs

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// a owns a.X; b owns b.Y and reduces a.Set by mirroring into b.Y.
type sysA struct{}

func (sysA) Name() string  { return "a" }
func (sysA) Kinds() []Kind { return []Kind{"a.X"} }
func (sysA) Reduce(ev Event, r Reader, w Writer) error {
	switch {
	case ev.Is("a", "Set"):
		var v int
		if err := ev.Decode(&v); err != nil {
			return err
		}
		return w.Set(ev.Entity, "a.X", v)
	case ev.Is("a", "Clear"):
		return w.Remove(ev.Entity, "a.X")
	case ev.Is("a", "Reject"):
		return errors.New("nope")
	case ev.Is("a", "Trespass"):
		return w.Set(ev.Entity, "b.Y", 1)
	}
	return nil
}

type sysB struct{ sawPrev []int }

func (sysB) Name() string  { return "b" }
func (sysB) Kinds() []Kind { return []Kind{"b.Y"} }
func (b *sysB) Reduce(ev Event, r Reader, w Writer) error {
	if ev.Is("a", "Set") {
		prev, _ := Get[int](r, ev.Entity, "a.X")
		b.sawPrev = append(b.sawPrev, prev)
		var v int
		_ = ev.Decode(&v)
		return w.Set(ev.Entity, "b.Y", v*10)
	}
	return nil
}

func TestWorld(t *testing.T) {
	lg := &MemLog{}
	b := &sysB{}
	w, err := New(lg, sysA{}, b)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEntityID()

	if _, err := w.Raise("a", "Set", e, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Raise("a", "Set", e, 2); err != nil {
		t.Fatal(err)
	}
	w.Read(func(r Reader) {
		if x, _ := Get[int](r, e, "a.X"); x != 2 {
			t.Errorf("a.X = %d, want 2", x)
		}
		if y, _ := Get[int](r, e, "b.Y"); y != 20 {
			t.Errorf("b.Y = %d, want 20 (cross-system reduce)", y)
		}
	})
	// Reducers see the pre-event state.
	if len(b.sawPrev) != 2 || b.sawPrev[0] != 0 || b.sawPrev[1] != 1 {
		t.Errorf("b saw prev %v, want [0 1]", b.sawPrev)
	}

	// A rejected event is not appended.
	if _, err := w.Raise("a", "Reject", e, nil); err == nil {
		t.Error("Reject: want error")
	}
	// Writing another system's kind is refused and not appended.
	if _, err := w.Raise("a", "Trespass", e, nil); !errors.Is(err, ErrNotOwner) {
		t.Errorf("Trespass: got %v, want ErrNotOwner", err)
	}
	// Unknown namespace is refused.
	if _, err := w.Raise("zzz", "X", e, nil); !errors.Is(err, ErrUnknownSystem) {
		t.Errorf("unknown system: got %v", err)
	}
	if len(lg.Events) != 2 {
		t.Fatalf("log has %d events, want 2", len(lg.Events))
	}

	if _, err := w.Raise("a", "Clear", e, nil); err != nil {
		t.Fatal(err)
	}
	// Rebuild from the same log reproduces the state.
	w2, err := New(lg, sysA{}, &sysB{})
	if err != nil {
		t.Fatal(err)
	}
	w2.Read(func(r Reader) {
		if r.Has(e, "a.X") {
			t.Error("a.X should be cleared after replay")
		}
		if y, _ := Get[int](r, e, "b.Y"); y != 20 {
			t.Errorf("b.Y = %d after replay, want 20", y)
		}
		if got := r.Entities("b.Y"); len(got) != 1 || got[0] != e {
			t.Errorf("Entities(b.Y) = %v", got)
		}
	})
}

func TestRegistration(t *testing.T) {
	type bad struct{ sysA }
	if _, err := New(&MemLog{}, sysA{}, sysA{}); err == nil {
		t.Error("duplicate system name should fail")
	}
	outside := kindSys{"c", []Kind{"a.X"}}
	if _, err := New(&MemLog{}, outside); err == nil {
		t.Error("kind outside namespace should fail")
	}
	_ = bad{}
}

type kindSys struct {
	name  string
	kinds []Kind
}

func (k kindSys) Name() string                     { return k.name }
func (k kindSys) Kinds() []Kind                    { return k.kinds }
func (kindSys) Reduce(Event, Reader, Writer) error { return nil }

func TestSQLiteLogRoundTrip(t *testing.T) {
	lg, err := OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	at := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)
	seq, err := lg.Append(Event{At: at, System: "a", Type: "Set", Entity: "e1", Payload: []byte(`7`)})
	if err != nil || seq != 1 {
		t.Fatalf("append: seq=%d err=%v", seq, err)
	}
	if _, err := lg.Append(Event{At: at, System: "a", Type: "Nil"}); err != nil {
		t.Fatal(err)
	}
	var got []Event
	if err := lg.Replay(func(ev Event) error { got = append(got, ev); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("replayed %d", len(got))
	}
	if got[0].Seq != 1 || !got[0].At.Equal(at) || got[0].Entity != "e1" || string(got[0].Payload) != "7" {
		t.Errorf("event 1 = %+v", got[0])
	}
	if got[1].Entity != "" || len(got[1].Payload) != 0 {
		t.Errorf("event 2 = %+v", got[1])
	}
}
