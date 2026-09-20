//go:build iroh

package meshtun

import (
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// TestCopyCountedIsLive is the property the status surface needs: the
// counter moves while the copy is still running. Accounting at the end
// of a flow read as zero throughput for a whole movie (359.9.4.4).
func TestCopyCountedIsLive(t *testing.T) {
	var n atomic.Int64
	pr, pw := io.Pipe()
	done := make(chan int64, 1)
	go func() { done <- copyCounted(io.Discard, pr, &n) }()

	if _, err := pw.Write(make([]byte, 1000)); err != nil {
		t.Fatal(err)
	}
	// The write above is synchronous with a read on the other side, but
	// the counter is added to just after that read returns.
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := n.Load(); got != 1000 {
		t.Fatalf("counter mid-copy = %d, want 1000 (counted only at close?)", got)
	}

	if _, err := pw.Write(make([]byte, 500)); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	total := <-done
	if total != 1500 {
		t.Errorf("copied = %d, want 1500", total)
	}
	if got := n.Load(); got != total {
		t.Errorf("counter = %d, want %d (same bytes, counted once)", got, total)
	}
}

// A nil counter is the bridge's case (cmd/irohup): still copies, still
// reports its total, accounts for nothing.
func TestCopyCountedNilCounter(t *testing.T) {
	pr, pw := io.Pipe()
	done := make(chan int64, 1)
	go func() { done <- copyCounted(io.Discard, pr, nil) }()
	if _, err := pw.Write(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	if total := <-done; total != 64 {
		t.Errorf("copied = %d, want 64", total)
	}
}

// Counters' accessors tolerate a nil receiver, so Pipe can take nil.
func TestNilCountersAccessors(t *testing.T) {
	var c *Counters
	if c.in() != nil || c.out() != nil {
		t.Error("nil *Counters should yield nil counters")
	}
	empty := &Counters{}
	if empty.in() != nil || empty.out() != nil {
		t.Error("empty Counters should yield nil counters")
	}
}
