package envelope

import (
	"sync"

	"github.com/marnyg/talos-config/protocol/cert"
)

// HWM is the receiver's per-(sender, receiver) seq high-water mark: a
// small volatile counter (invariant 8). Loss on restart reopens the
// replay window only until the sender's certs expire; it never grants
// anything stronger. The zero value is ready to use.
type HWM struct {
	mu sync.Mutex
	m  map[string]int64
}

// NewHWM returns an empty high-water mark table.
func NewHWM() *HWM { return &HWM{m: make(map[string]int64)} }

func hwmKey(from, to cert.ActorID) string {
	// Ids carry a scheme prefix and no whitespace; a space separator can
	// therefore never collide across (from, to) pairs.
	return string(from) + " " + string(to)
}

// Check reports whether seq is strictly above the mark for (from, to)
// and, if so, advances the mark to seq. The mark starts at 0, so the
// first accepted seq is ≥ 1. Check-and-advance is one atomic step.
func (h *HWM) Check(from, to cert.ActorID, seq int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m == nil {
		h.m = make(map[string]int64)
	}
	k := hwmKey(from, to)
	if seq <= h.m[k] {
		return false
	}
	h.m[k] = seq
	return true
}

// Peek returns the current mark for (from, to) without advancing it
// (0 when the pair has never been seen).
func (h *HWM) Peek(from, to cert.ActorID) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.m[hwmKey(from, to)]
}
