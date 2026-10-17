// Untagged on purpose: pure scheduling, no transport, so it compiles
// and tests without -tags iroh (main.go is tagged).
package main

import (
	"context"
	"time"
)

// Delay between consecutive expired device flows (talos-config-2nqw).
// An unapproved pod used to start a fresh flow the instant the previous
// one expired — a new user code in the log every expires_in, forever,
// and a hub hit to match. The gap starts small (nobody is watching the
// log the first time either way) and doubles to a cap, so a pod left
// unapproved for a weekend costs a handful of flows an hour rather
// than one per TTL.
const (
	flowBackoffMin = 30 * time.Second
	flowBackoffMax = 10 * time.Minute
)

// flowBackoff yields the wait before the next device flow after an
// expiry: min, 2·min, 4·min, … capped at max. Zero value is ready to
// use with the package defaults.
type flowBackoff struct {
	min, max time.Duration
	cur      time.Duration
}

// next returns the delay to wait before the next flow and advances the
// schedule. The first call returns min.
func (b *flowBackoff) next() time.Duration {
	if b.min <= 0 {
		b.min = flowBackoffMin
	}
	if b.max <= 0 {
		b.max = flowBackoffMax
	}
	if b.cur <= 0 {
		b.cur = b.min
	} else {
		b.cur *= 2
	}
	if b.cur > b.max {
		b.cur = b.max
	}
	return b.cur
}

// sleepCtx blocks for d or until ctx is done, returning ctx.Err() in
// the latter case so a SIGTERM during the gap ends the enrollment loop
// instead of waiting out the delay.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
