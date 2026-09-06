// Package clock implements the verifier's low-water mark over cert iat
// (ADR-0019, verification/quint/clock.qnt).
//
// Time is a trust input with two halves. The upper bound (roll-forward =
// denial) is the verifier's local clock — ops, not protocol. The lower
// bound (rollback = resurrection of expired certs) is protocol-enforced:
// every cert carries iat, and a verifier keeps lw = max(lw, iat) over
// every cert cert.Authorize lists in Result.Verified (rooted at the
// receiver: on a chain whose root consent the receiver signed —
// signature-only provenance, decision 7ry; never a stranger's
// self-signed cert, which would let any connecting peer push the mark),
// then judges with now = max(local, lw). The advance is uncapped:
// capping it at local+skew discards exactly the honest evidence a
// rollback needs (clock.qnt FINDING).
//
// A Mark is a safe-to-lose cache: volatile in v0, optionally persisted;
// loss degrades to the local clock and is never a security regression.
// It is not safe for concurrent use; guard it if shared.
package clock

import "github.com/marnyg/talos-config/protocol/cert"

// Mark is the monotone low-water mark.
type Mark struct {
	lw int64
}

// Observe folds a cert's iat into the mark. Call it ONLY with a cert
// that cert.Authorize returned in Result.Verified — rooted at this
// receiver. cert.Verify alone is NOT sufficient: a stranger's self-signed
// cert verifies, and its iat is attacker-chosen. The advance is monotone
// and uncapped.
func (m *Mark) Observe(c cert.Cert) {
	if c.Iat > m.lw {
		m.lw = c.Iat
	}
}

// ObserveAll folds every cert's iat into the mark. Pass a
// cert.Result.Verified slice: those are exactly the certs Authorize
// found rooted at the receiver. Authorize judges with the Now the caller
// passed and Verified feeds the mark afterwards — ADR-0019's "update
// first, then judge" holds across bundles, not within one (decision
// c4c).
func (m *Mark) ObserveAll(cs []cert.Cert) {
	for _, c := range cs {
		m.Observe(c)
	}
}

// Now returns the effective clock max(local, lw) to pass as
// cert.Input.Now.
func (m *Mark) Now(local int64) int64 {
	if m.lw > local {
		return m.lw
	}
	return local
}

// LowWater returns the current mark (for persistence / diagnostics).
func (m *Mark) LowWater() int64 { return m.lw }

// Restore seeds the mark from a persisted value (best-effort; the mark
// stays monotone).
func (m *Mark) Restore(lw int64) {
	if lw > m.lw {
		m.lw = lw
	}
}
