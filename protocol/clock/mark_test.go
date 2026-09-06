package clock

import (
	"testing"

	"github.com/marnyg/talos-config/protocol/cert"
	"pgregory.net/rapid"
)

// This file ports verification/quint/clock.qnt over the bare Mark: the
// four original laws over a verifier whose local clock an adversary sets
// freely, honest issuers (iat = t) and a lying trusted issuer (future
// iat). The model now has six laws; the two stranger laws and the
// end-to-end port of all six through cert.Authorize (real signatures,
// Result.Verified feeding the mark) live in rooted_laws_test.go. Certs
// here are minimal (only iat/exp matter — rootedness is Authorize's job
// and assumed done before Observe, as the model states).

const (
	horizon = 8
	life    = 3
)

func mkCert(iat, exp int64) cert.Cert { return cert.Cert{Iat: iat, Exp: exp} }

// admits ports the model's admits: exp > max(local, lw).
func admits(c cert.Cert, local, lw int64) bool { return c.Exp > max64(local, lw) }

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type honestCert struct {
	c      cert.Cert
	honest bool
}

type outcome struct {
	c          cert.Cert
	honest     bool
	t, local   int64
	hsBefore   int64
	liarBefore bool
	accepted   bool
}

// TestClockLaws drives the clock.qnt state machine with rapid and checks
// invAll after every step.
func TestClockLaws(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var (
			mark     Mark
			tt       int64 // real time
			local    int64
			hs       int64 // max honest iat seen since restart
			liar     bool  // any dishonest cert fed the mark since restart
			certs    []honestCert
			seen     []honestCert
			outcomes []outcome
		)

		steps := rapid.IntRange(1, 40).Draw(t, "steps")
		for i := 0; i < steps; i++ {
			action := rapid.IntRange(0, 6).Draw(t, "action")
			switch action {
			case 0: // tick: real time and a well-behaved clock advance
				if tt < horizon {
					tt++
					local++
				}
			case 1: // rollback: adversary sets local into the past
				if tt >= 1 {
					local = rapid.Int64Range(0, tt-1).Draw(t, "rb")
				}
			case 2: // rollforward: adversary sets local into the future
				local = rapid.Int64Range(tt+1, horizon+life+1).Draw(t, "rf")
			case 3: // issue honest: iat = t
				certs = append(certs, honestCert{c: mkCert(tt, tt+life), honest: true})
			case 4: // issue lying: a trusted issuer with a future iat
				f := rapid.Int64Range(tt+1, horizon+1).Draw(t, "lie")
				certs = append(certs, honestCert{c: mkCert(f, f+life), honest: false})
			case 5: // present: advance the mark, then judge
				if len(certs) == 0 {
					break
				}
				idx := rapid.IntRange(0, len(certs)-1).Draw(t, "present")
				hc := certs[idx]
				hsBefore, liarBefore := hs, liar
				mark.Observe(hc.c) // advance first (only after sig verify — assumed)
				acc := admits(hc.c, local, mark.LowWater())
				seen = append(seen, hc)
				if hc.honest {
					hs = max64(hs, hc.c.Iat)
				} else {
					liar = true
				}
				outcomes = append(outcomes, outcome{
					c: hc.c, honest: hc.honest, t: tt, local: local, hsBefore: hsBefore, liarBefore: liarBefore, accepted: acc,
				})
			case 6: // restart: the volatile mark forgets everything but the clock
				mark = Mark{}
				hs, liar = 0, false
				seen = nil
				outcomes = nil
			}
			checkClockLaws(t, &mark, hs, seen, outcomes)
		}
	})
}

func checkClockLaws(t *rapid.T, mark *Mark, hs int64, seen []honestCert, outcomes []outcome) {
	// invLWCoversHonestSeen: lw ≥ iat of every honest cert seen.
	for _, hc := range seen {
		if hc.honest && hc.c.Iat > mark.LowWater() {
			t.Fatalf("invLWCoversHonestSeen: honest iat %d > lw %d", hc.c.Iat, mark.LowWater())
		}
	}
	for _, o := range outcomes {
		// invResurrectionBounded: an accepted, truly-expired cert expired
		// after every honest iat the verifier had seen — resurrection ⊆
		// starvation window; liars cannot widen it.
		if o.accepted && o.c.Exp <= o.t && !(o.c.Exp > o.hsBefore) {
			t.Fatalf("invResurrectionBounded: accepted expired cert exp=%d hsBefore=%d t=%d",
				o.c.Exp, o.hsBefore, o.t)
		}
		// invHonestCorrectClockAccepted: correct clock + honest issuer +
		// no liar in history ⇒ never falsely rejected.
		if o.local == o.t && o.honest && o.c.Exp > o.t && !o.liarBefore && !o.accepted {
			t.Fatalf("invHonestCorrectClockAccepted: honest live cert rejected exp=%d t=%d local=%d",
				o.c.Exp, o.t, o.local)
		}
		// invNoAcceptBeyondLocal: roll-forward can only deny.
		if o.accepted && !(o.c.Exp > o.local) {
			t.Fatalf("invNoAcceptBeyondLocal: accepted cert exp=%d local=%d", o.c.Exp, o.local)
		}
	}
}

// TestRollbackCaught is the model's rollbackCaughtTest: an honest cert
// expires, a fresh honest cert advances the mark, the clock is rolled
// back, and the stale cert is REJECTED although local time says it is
// live.
func TestRollbackCaught(t *testing.T) {
	var mark Mark
	a := mkCert(0, 3) // iat 0, exp 3
	b := mkCert(4, 7) // iat 4, seen after time passes
	// t = 4, A expired; verify B → mark advances to 4.
	mark.Observe(b)
	if mark.LowWater() != 4 {
		t.Fatalf("lw = %d, want 4", mark.LowWater())
	}
	// rollback local to 0; present A: local says live (0 < 3) but mark says dead.
	local := int64(0)
	mark.Observe(a)
	if admits(a, local, mark.LowWater()) {
		t.Fatal("rollback not caught: stale cert accepted")
	}
	if mark.LowWater() != 4 {
		t.Fatalf("lw regressed to %d", mark.LowWater())
	}
}

// TestObserveAllMonotone: ObserveAll folds a Verified slice; the mark is
// monotone and never regresses.
func TestObserveAllMonotone(t *testing.T) {
	var mark Mark
	mark.ObserveAll([]cert.Cert{mkCert(5, 9), mkCert(2, 6), mkCert(8, 12)})
	if mark.LowWater() != 8 {
		t.Fatalf("lw = %d, want 8", mark.LowWater())
	}
	mark.ObserveAll([]cert.Cert{mkCert(3, 7)}) // lower iat must not regress
	if mark.LowWater() != 8 {
		t.Fatalf("lw regressed to %d", mark.LowWater())
	}
	if mark.Now(1) != 8 {
		t.Fatalf("Now(1) = %d, want 8", mark.Now(1))
	}
	if mark.Now(100) != 100 {
		t.Fatalf("Now(100) = %d, want 100", mark.Now(100))
	}
}
