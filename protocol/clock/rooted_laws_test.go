package clock

import (
	"crypto/ed25519"
	"strconv"
	"testing"

	"github.com/marnyg/talos-config/protocol/cert"
	"pgregory.net/rapid"
)

// This file ports the six laws + the stranger witness of
// verification/quint/clock.qnt (issue zev) END TO END: the state machine
// is the model's (real time t, an adversarial local clock, issue /
// present / restart), but every "cert" is a real, signed BUNDLE fed to
// cert.Authorize with Now = mark.Now(local), and the mark is the real
// clock.Mark fed from Result.Verified via ObserveAll. The model's
// `isRooted` is therefore not assumed — it is what buildRooted computes,
// and the laws check it against the history's ground truth.
//
// Issuer classes (the model's Kind) are realised as concrete signers:
//
//	Honest    a ROOTED issuer with a correct clock (iat = t)
//	Lying     a ROOTED issuer with a future iat
//	Stranger  signature valid, NO consent chain to R
//
// and "rooted" itself has four shapes, two of which are rooted-but-never-
// authorized (the rulings this file pins):
//
//	OWNER      R-signed live consent; signs directly            (rooted, authorizable)
//	HUB        OWNER's hot key via a live speak-as              (rooted, authorizable)
//	OLD_OWNER  consent EXPIRED at every now — decision 7ry:     (rooted, never OK)
//	OLD_HUB    hot key of OLD_OWNER via a live speak-as         (rooted, never OK)
//	NOVERB_HUB OWNER's hot key, speak-as cav.verbs = [invoke]
//	           only — decision jo8:                              (rooted, never OK)
//	STRANGER   self-signed wallet nobody consented to           (unrooted)
//	SHUB       STRANGER's hot key via a STRANGER-signed speak-as (unrooted)
//
// Every cert in a presented bundle (member, grant, speak-as) shares one
// iat/exp so the bundle IS the model's cert; R's consents carry iat 0.
// Laws are stated over the history's ground truth (which signer, which
// t), never via buildRooted, so a bug seeded in Authorize cannot hide
// behind the same bug in a law.
//
// The model updates the mark then judges; Go judges with the caller's
// Now then returns Verified (decision c4c: "update first, then judge" is
// across bundles). For a single bundle with iat < exp the two orders
// agree (clock.qnt mutant m8), which is what lets these laws hold 1:1.

// issuer enumerates the concrete signers above.
type issuer int

const (
	issOwner issuer = iota
	issHub
	issOldOwner  // 7ry: consent expired at now
	issOldHub    // 7ry: hot key of an expired-consent principal
	issNoVerbHub // jo8: speak-as omits `member`
	issStranger
	issStrangerHub
	numIssuers
)

var rootedIssuers = []issuer{issOwner, issHub, issOldOwner, issOldHub, issNoVerbHub}
var strangerIssuers = []issuer{issStranger, issStrangerHub}

func (i issuer) rooted() bool       { return i != issStranger && i != issStrangerHub }
func (i issuer) authorizable() bool { return i == issOwner || i == issHub }

func (i issuer) String() string {
	return [...]string{"OWNER", "HUB", "OLD_OWNER", "OLD_HUB", "NOVERB_HUB", "STRANGER", "SHUB"}[i]
}

// signer / principal (nil ⇒ signs directly) per issuer.
func (i issuer) keys() (signer, principal string) {
	switch i {
	case issOwner:
		return "OWNER", ""
	case issHub:
		return "HUB", "OWNER"
	case issOldOwner:
		return "OLD_OWNER", ""
	case issOldHub:
		return "OLD_HUB", "OLD_OWNER"
	case issNoVerbHub:
		return "NOVERB_HUB", "OWNER"
	case issStranger:
		return "STRANGER", ""
	default:
		return "SHUB", "STRANGER"
	}
}

// fataler is what the fixture needs from a test handle: *testing.T and
// *rapid.T both satisfy it.
type fataler interface{ Fatal(args ...any) }

// world is the closed set of keys plus R's consents.
type world struct {
	signer   map[string]cert.Signer
	id       map[string]cert.ActorID
	consents []cert.Cert
}

var worldNames = []string{"R", "CALLER", "OWNER", "HUB", "OLD_OWNER", "OLD_HUB", "NOVERB_HUB", "STRANGER", "SHUB"}

func newWorld(t fataler) world {
	w := world{signer: map[string]cert.Signer{}, id: map[string]cert.ActorID{}}
	seed := byte(70)
	for _, n := range worldNames {
		s := cert.NewEdSigner(ed25519.NewKeyFromSeed(seedBytes(seed)))
		w.signer[n] = s
		w.id[n] = s.ActorID()
		seed++
	}
	consentCav := cert.Caveats{Target: []cert.ActorID{w.id["R"]}, Facet: []string{"apid"}, Delegable: true}
	w.consents = []cert.Cert{
		// live for every now the history can reach (now ≤ HORIZON+LIFE+1)
		w.sign(t, "R", cert.Cert{Aud: string(w.id["OWNER"]), Can: cert.VerbInvoke, Cav: consentCav, Iat: 0, Exp: 100}),
		// 7ry: R DID sign it, but it is expired at every now ≥ 0
		w.sign(t, "R", cert.Cert{Aud: string(w.id["OLD_OWNER"]), Can: cert.VerbInvoke, Cav: consentCav, Iat: 0, Exp: 0}),
	}
	return w
}

func seedBytes(b byte) []byte {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = b
	}
	return s
}

func (w world) sign(t fataler, by string, c cert.Cert) cert.Cert {
	out, err := cert.Sign(c, w.signer[by])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// issued is one of the model's certs: a bundle whose certs share iat/exp.
type issued struct {
	iss    issuer
	iat    int64
	exp    int64
	honest bool // clock-correct at issuance (iat = t); false for Lying/Stranger
	bundle cert.Bundle
}

// issue builds the bundle for issuer i at iat/exp: member + grant to
// CALLER signed by the issuer's key, plus the speak-as from its
// principal when it is a hot key.
func (w world) issue(t fataler, i issuer, iat, exp int64, honest bool) issued {
	signer, principal := i.keys()
	member := w.sign(t, signer, cert.Cert{Aud: string(w.id["CALLER"]), Can: cert.VerbMember,
		Cav: cert.Caveats{Groups: []string{"admins"}, Name: "laptop"}, Iat: iat, Exp: exp})
	grant := w.sign(t, signer, cert.Cert{Aud: string(w.id["CALLER"]), Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{w.id["R"]}, Facet: []string{"apid"}}, Iat: iat, Exp: exp})
	b := cert.Bundle{Member: member, Grants: []cert.Cert{grant}}
	if principal != "" {
		verbs := []string{"member", "invoke"}
		if i == issNoVerbHub {
			verbs = []string{"invoke"} // jo8: cannot resolve the member cert
		}
		b.SpeakAs = []cert.Cert{w.sign(t, principal, cert.Cert{Aud: string(w.id[signer]), Can: cert.VerbSpeakAs,
			Cav: cert.Caveats{Verbs: verbs, Groups: []string{"admins"}}, Iat: iat, Exp: exp})}
	}
	return issued{iss: i, iat: iat, exp: exp, honest: honest, bundle: b}
}

// present runs one bundle through Authorize with the effective clock and
// feeds Verified to the mark — what a receiver does per connect.
func (w world) present(mark *Mark, c issued, local int64) cert.Result {
	res := cert.Authorize(cert.Input{
		Receiver: w.id["R"], AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Consents: w.consents, Blocklist: map[cert.ActorID]bool{}, Now: mark.Now(local),
		ALPN: "mesh/apid/v1", Peer: w.id["CALLER"], Bundle: c.bundle,
	})
	mark.ObserveAll(res.Verified)
	return res
}

// presentation is the model's Outcome plus the Verified slice.
type presentation struct {
	c          issued
	t, local   int64
	hsBefore   int64
	liarBefore bool
	accepted   bool
	verified   []cert.Cert
}

// history is the model's state at one step.
type history struct {
	mark  *Mark
	t     int64
	hs    int64 // max honest ROOTED iat fed to the mark since restart
	liar  bool  // a Lying (rooted, future-iat) bundle fed the mark since restart
	seen  []issued
	pres  []presentation
	world world
}

// actionWeights biases the uniform `any { … }` of the model toward
// issue/present so that a 100-history run reaches every law's antecedent
// (TestHistoryCoverage): uniform choice leaves ~2 presentations per
// history. `resync` (local = t) is not a model action but is within the
// adversary's power (it sets local anywhere); without it local == t
// holds only in histories with no clock move at all, and
// invHonestCorrectClockAccepted would be near-vacuous.
var actionWeights = []int{
	2, // tick
	1, // rollback
	1, // rollforward
	1, // resync
	3, // issueHonest
	1, // issueLying
	2, // issueStranger
	5, // present
	1, // restart
}

func drawAction(t *rapid.T) int {
	total := 0
	for _, w := range actionWeights {
		total += w
	}
	n := rapid.IntRange(0, total-1).Draw(t, "action")
	for i, w := range actionWeights {
		if n < w {
			return i
		}
		n -= w
	}
	return len(actionWeights) - 1
}

// runHistory drives the clock.qnt state machine (tick, rollback,
// rollforward, issueHonest, issueLying, issueStranger, present, restart)
// and calls check after every step.
func runHistory(t *rapid.T, w world, check func(t *rapid.T, h *history)) {
	h := &history{mark: &Mark{}, world: w}
	var (
		local int64
		certs []issued
	)
	steps := rapid.IntRange(20, 80).Draw(t, "steps")
	for i := 0; i < steps; i++ {
		switch drawAction(t) {
		case 0: // tick
			if h.t < horizon {
				h.t++
				local++
			}
		case 1: // rollback
			if h.t >= 1 {
				local = rapid.Int64Range(0, h.t-1).Draw(t, "rb")
			}
		case 2: // rollforward
			local = rapid.Int64Range(h.t+1, horizon+life+1).Draw(t, "rf")
		case 3: // resync: the clock is set right again (NTP)
			local = h.t
		case 4: // issueHonest: any rooted issuer, iat = t
			iss := rapid.SampledFrom(rootedIssuers).Draw(t, "honestIss")
			certs = append(certs, w.issue(t, iss, h.t, h.t+life, true))
		case 5: // issueLying: any rooted issuer, future iat
			iss := rapid.SampledFrom(rootedIssuers).Draw(t, "lyingIss")
			f := rapid.Int64Range(h.t+1, horizon+1).Draw(t, "lie")
			certs = append(certs, w.issue(t, iss, f, f+life, false))
		case 6: // issueStranger: iat past / plausible-now / far future
			iss := rapid.SampledFrom(strangerIssuers).Draw(t, "strangerIss")
			f := rapid.SampledFrom([]int64{0, h.t, horizon + 1}).Draw(t, "sIat")
			certs = append(certs, w.issue(t, iss, f, f+life, false))
		case 7: // present
			if len(certs) == 0 {
				break
			}
			c := certs[rapid.IntRange(0, len(certs)-1).Draw(t, "present")]
			hsBefore, liarBefore := h.hs, h.liar
			res := w.present(h.mark, c, local)
			h.seen = append(h.seen, c)
			if c.iss.rooted() {
				if c.honest {
					h.hs = max64(h.hs, c.iat)
				} else {
					h.liar = true
				}
			}
			h.pres = append(h.pres, presentation{c: c, t: h.t, local: local,
				hsBefore: hsBefore, liarBefore: liarBefore, accepted: res.OK, verified: res.Verified})
		case 8: // restart: the volatile mark forgets everything but the clock
			h.mark = &Mark{}
			h.hs, h.liar = 0, false
			h.seen, h.pres = nil, nil
		}
		check(t, h)
	}
}

func (h *history) isStrangerKey(k cert.ActorID) bool {
	return k == h.world.id["STRANGER"] || k == h.world.id["SHUB"]
}

// TestInvStrangerNeverAdvances — clock.qnt invStrangerNeverAdvances:
// lw == max iat over the ROOTED bundles seen since restart (0 if none;
// R's consents carry iat 0). Strangers never move it, in either
// direction; and no stranger-signed cert ever reaches Verified.
func TestInvStrangerNeverAdvances(t *testing.T) {
	w := newWorld(t)
	rapid.Check(t, func(t *rapid.T) {
		runHistory(t, w, func(t *rapid.T, h *history) {
			var want int64
			for _, c := range h.seen {
				if c.iss.rooted() {
					want = max64(want, c.iat)
				}
			}
			if got := h.mark.LowWater(); got != want {
				t.Fatalf("invStrangerNeverAdvances: lw = %d, want max rooted iat %d (seen %s)", got, want, describe(h.seen))
			}
			for _, p := range h.pres {
				for _, v := range p.verified {
					if h.isStrangerKey(v.Iss) {
						t.Fatalf("invStrangerNeverAdvances: stranger-signed cert (iat %d) in Verified", v.Iat)
					}
				}
			}
		})
	})
}

// TestInvStrangerNeverAccepted — clock.qnt invStrangerNeverAccepted: a
// bundle whose member's issuer has no consent chain to R is never
// authorized, whatever exp, local or lw. Go refinement (7ry/jo8): a
// ROOTED-but-unauthorizable issuer (expired consent; speak-as without
// `member`) is never accepted either — rooting feeds the mark, it does
// not grant.
func TestInvStrangerNeverAccepted(t *testing.T) {
	w := newWorld(t)
	rapid.Check(t, func(t *rapid.T) {
		runHistory(t, w, func(t *rapid.T, h *history) {
			for _, p := range h.pres {
				if !p.c.iss.rooted() && p.accepted {
					t.Fatalf("invStrangerNeverAccepted: %s bundle accepted (iat %d exp %d local %d)", p.c.iss, p.c.iat, p.c.exp, p.local)
				}
				if !p.c.iss.authorizable() && p.accepted {
					t.Fatalf("rooted-only issuer %s accepted (iat %d exp %d local %d)", p.c.iss, p.c.iat, p.c.exp, p.local)
				}
			}
		})
	})
}

// TestInvLWCoversHonestSeen — lw ≥ iat of every honest ROOTED bundle
// seen since restart. Includes the 7ry/jo8 shapes: their certs are
// rooted, so their iat is evidence even though authorization rejects.
func TestInvLWCoversHonestSeen(t *testing.T) {
	w := newWorld(t)
	rapid.Check(t, func(t *rapid.T) {
		runHistory(t, w, func(t *rapid.T, h *history) {
			for _, c := range h.seen {
				if c.iss.rooted() && c.honest && c.iat > h.mark.LowWater() {
					t.Fatalf("invLWCoversHonestSeen: honest %s iat %d > lw %d", c.iss, c.iat, h.mark.LowWater())
				}
			}
		})
	})
}

// TestInvResurrectionBounded — an accepted bundle that is truly expired
// (exp ≤ t) expired after every honest rooted iat the verifier had
// seen: rollback resurrects only what died inside the starvation
// window, and liars cannot widen it.
func TestInvResurrectionBounded(t *testing.T) {
	w := newWorld(t)
	rapid.Check(t, func(t *rapid.T) {
		runHistory(t, w, func(t *rapid.T, h *history) {
			for _, p := range h.pres {
				if p.accepted && p.c.exp <= p.t && !(p.c.exp > p.hsBefore) {
					t.Fatalf("invResurrectionBounded: accepted expired %s exp=%d hsBefore=%d t=%d", p.c.iss, p.c.exp, p.hsBefore, p.t)
				}
			}
		})
	})
}

// TestInvHonestCorrectClockAccepted — correct clock + honest,
// authorizable issuer + live bundle + no LIAR in the history ⇒ accepted.
// Strangers in the history are not liars and must not flip this.
func TestInvHonestCorrectClockAccepted(t *testing.T) {
	w := newWorld(t)
	rapid.Check(t, func(t *rapid.T) {
		runHistory(t, w, func(t *rapid.T, h *history) {
			for _, p := range h.pres {
				if p.local == p.t && p.c.honest && p.c.iss.authorizable() && p.c.exp > p.t && !p.liarBefore && !p.accepted {
					t.Fatalf("invHonestCorrectClockAccepted: live honest %s rejected exp=%d t=%d local=%d lw=%d", p.c.iss, p.c.exp, p.t, p.local, h.mark.LowWater())
				}
			}
		})
	})
}

// TestInvNoAcceptBeyondLocal — roll-forward can only deny: OK ⇒ exp > local.
func TestInvNoAcceptBeyondLocal(t *testing.T) {
	w := newWorld(t)
	rapid.Check(t, func(t *rapid.T) {
		runHistory(t, w, func(t *rapid.T, h *history) {
			for _, p := range h.pres {
				if p.accepted && !(p.c.exp > p.local) {
					t.Fatalf("invNoAcceptBeyondLocal: accepted %s exp=%d local=%d", p.c.iss, p.c.exp, p.local)
				}
			}
		})
	})
}

// TestStrangerDenial — the model's strangerDenialTest witness: a
// self-signed bundle with iat far in the future is presented at t=1,
// rejected, and leaves the mark at 0; a live honest bundle presented
// right after is accepted and the mark moves to 1. Both stranger shapes.
func TestStrangerDenial(t *testing.T) {
	w := newWorld(t)
	for _, iss := range strangerIssuers {
		mark := &Mark{}
		stranger := w.issue(t, iss, horizon+1, horizon+1+life, false)
		honest := w.issue(t, issOwner, 1, 1+life, true) // t = 1
		local := int64(1)
		res := w.present(mark, stranger, local)
		if res.OK {
			t.Fatalf("%s: stranger bundle accepted", iss)
		}
		if mark.LowWater() != 0 {
			t.Fatalf("%s: stranger moved the mark to %d", iss, mark.LowWater())
		}
		res = w.present(mark, honest, local)
		if !res.OK {
			t.Fatalf("%s: live honest bundle rejected after the stranger", iss)
		}
		if mark.LowWater() != 1 {
			t.Fatalf("%s: lw = %d, want 1", iss, mark.LowWater())
		}
	}
}

// TestRootedNotAuthorized pins the two rulings directly, outside the
// random history: the 7ry shape (expired consent, fresh hot-key cert)
// and the jo8 shape (speak-as without `member`) are rejected AND rooted
// — Verified carries their certs and the mark advances.
func TestRootedNotAuthorized(t *testing.T) {
	w := newWorld(t)
	for _, iss := range []issuer{issOldOwner, issOldHub, issNoVerbHub} {
		mark := &Mark{}
		c := w.issue(t, iss, 5, 5+life, true)
		res := w.present(mark, c, 5)
		if res.OK {
			t.Fatalf("%s: authorized", iss)
		}
		if mark.LowWater() != 5 {
			t.Fatalf("%s: lw = %d, want 5 (rooted cert must advance the mark)", iss, mark.LowWater())
		}
		found := 0
		for _, v := range res.Verified {
			if v.Iat == 5 {
				found++
			}
		}
		if want := 2 + len(c.bundle.SpeakAs); found != want {
			t.Fatalf("%s: %d of %d bundle certs in Verified", iss, found, want)
		}
	}
	// Control: the authorizable shapes accept under the same clock.
	for _, iss := range []issuer{issOwner, issHub} {
		mark := &Mark{}
		if res := w.present(mark, w.issue(t, iss, 5, 5+life, true), 5); !res.OK {
			t.Fatalf("%s: live honest bundle rejected", iss)
		}
	}
}

// TestHistoryCoverage proves the laws above are not vacuous: over the
// default run the histories reach Accept, present every issuer shape,
// and hit the antecedents that matter (accepted-while-expired for
// invResurrectionBounded, correct-clock honest presentations with and
// without a prior liar, strangers with a far-future iat).
func TestHistoryCoverage(t *testing.T) {
	w := newWorld(t)
	const minHits = 5
	hits := map[string]int{}
	presented := map[issuer]int{}
	rapid.Check(t, func(t *rapid.T) {
		var last int
		runHistory(t, w, func(t *rapid.T, h *history) {
			if last > len(h.pres) { // restart cleared the outcomes
				last = 0
			}
			for _, p := range h.pres[last:] {
				presented[p.c.iss]++
				if p.accepted {
					hits["accepted"]++
					if p.c.iss == issHub {
						hits["accepted via hot key"]++
					}
					if p.c.exp <= p.t {
						hits["accepted while truly expired"]++
					}
					if !p.c.honest {
						hits["accepted lying"]++
					}
				}
				if p.local == p.t && p.c.honest && p.c.iss.authorizable() && p.c.exp > p.t {
					if p.liarBefore {
						hits["honest live, correct clock, liar before"]++
					} else {
						hits["honest live, correct clock, no liar"]++
					}
				}
				if !p.c.iss.rooted() && p.c.iat == horizon+1 {
					hits["stranger far-future iat"]++
				}
				if p.c.iss.rooted() && !p.c.iss.authorizable() && p.c.honest && p.c.exp > p.t && p.local == p.t {
					hits["rooted-only live, correct clock (7ry/jo8)"]++
				}
				if p.local < p.t {
					hits["presented under rollback"]++
				}
			}
			last = len(h.pres)
		})
	})
	for iss := issuer(0); iss < numIssuers; iss++ {
		t.Logf("%-12s presented %d times", iss, presented[iss])
		if presented[iss] < minHits {
			t.Errorf("%s presented only %d times (< %d)", iss, presented[iss], minHits)
		}
	}
	for _, k := range []string{"accepted", "accepted via hot key", "accepted while truly expired", "accepted lying",
		"honest live, correct clock, no liar", "stranger far-future iat",
		"rooted-only live, correct clock (7ry/jo8)", "presented under rollback"} {
		t.Logf("%-45s %d", k, hits[k])
		if hits[k] < minHits {
			t.Errorf("%q hit only %d times (< %d)", k, hits[k], minHits)
		}
	}
	// Informational: the case invHonestCorrectClockAccepted deliberately
	// EXCLUDES (a liar already fed the mark); rare, no threshold.
	t.Logf("%-45s %d", "honest live, correct clock, liar before", hits["honest live, correct clock, liar before"])
}

func describe(seen []issued) string {
	s := ""
	for _, c := range seen {
		s += c.iss.String() + "@" + strconv.FormatInt(c.iat, 10) + " "
	}
	return s
}
