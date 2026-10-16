package cert

import (
	"crypto/ed25519"
	"reflect"
	"slices"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

// This file ports the receiver-held pin laws of
// verification/quint/authorize.qnt (ADR-0010, d0o0) over real
// signatures, the way authorize_laws_test.go ports the rest: the same
// genNear bundles, plus the model's OWNER1_NEW (OWNER1's root after a
// rotation) and the pin draws PIN_TRIVIAL / PIN_ROTATED / PIN_DEAD.
//
//   invTrivialPinsPreserveVerdicts  TestPinLaws/… + TestPinTrivialSweep
//   invOldRootDiesOnRotation        TestPinLaws/…
//   invNewRootAdmittedViaPin        TestPinLaws/…
//   invDeadPinAdmitsNothing         TestPinLaws/…
//   invCallerPinsIgnored            by construction — see TestPinCallerPinsIgnored
//
// Deviation from the model's generator (not from a law): the model
// flips every OWNER1 signature to OWNER1_NEW under FRootRotated but
// keeps the literal OWNER1 wherever a TARGET names it. Here the
// post-rotation shape is built with a twin fixture whose "OWNER1" IS
// the new key, so a target that names OWNER1 names the new key too.
// Every law is a necessary condition or an equality of two verdicts on
// the same bundle, so the shift changes which draws hit an antecedent,
// never whether a law holds.

// pinKind is the model's PIN1_DRAWS minus the empty set (a receiver
// holding no pin at all is today's receiver and is covered by every
// other suite).
type pinKind int

const (
	pinTrivial pinKind = iota // PIN_TRIVIAL1: epoch-0 policy, no rotation
	pinRotated                // PIN_ROTATED: OWNER1 → OWNER1_NEW held
	pinDead                   // PIN_DEAD: equivocation at R; OWNER1 is dead
	numPinKinds
)

// trivialPolicy is the legacy-EOA shape every consumer pins today:
// the identity IS its root key and its only guardian.
func trivialPolicy(addr ActorID) Policy {
	return Policy{Root: addr, Guardians: []ActorID{addr}, K: 1}
}

// rotateCert signs one rotate-root body prev→next at epoch for address
// with signer (the sole guardian under trivialPolicy).
func rotateCert(address ActorID, prev, next Policy, epoch int64, signer Signer) Cert {
	c, err := Sign(Cert{Aud: string(address), Can: VerbRotateRoot,
		Cav: Caveats{Epoch: epoch, Prev: prev.Hash(), Policy: &next}, Iat: 1, Exp: 10}, signer)
	if err != nil {
		panic(err)
	}
	return c
}

// pinFor builds R's pin for OWNER1 of the given kind: trivial, rotated
// to OWNER1_NEW at epoch 1 (signed by OWNER1, the epoch-0 guardian), or
// dead (two distinct epoch-1 successors signed by that guardian — the
// equivocation rotation.qnt kills on).
func pinFor(f fixture, kind pinKind) *Pin {
	old, next := f.id["OWNER1"], f.id["OWNER1_NEW"]
	p0 := trivialPolicy(old)
	pin := NewPin(old, p0)
	if kind == pinTrivial {
		return pin
	}
	p1 := trivialPolicy(next)
	r, err := AssembleRotation(old, p0, 1, []Cert{rotateCert(old, p0, p1, 1, f.signer["OWNER1"])}, 1)
	if err != nil {
		panic(err)
	}
	if err := pin.Hold(r); err != nil {
		panic(err)
	}
	if kind == pinRotated {
		return pin
	}
	// A second, distinct successor at the same epoch from the same prev:
	// the view dies (PIN_DEAD keeps its root, admits nothing).
	p1b := Policy{Root: next, Guardians: []ActorID{next, old}, K: 1}
	rb, err := AssembleRotation(old, p0, 1, []Cert{rotateCert(old, p0, p1b, 1, f.signer["OWNER1"])}, 1)
	if err != nil {
		panic(err)
	}
	if err := pin.Hold(rb); !isEquivocation(err) {
		panic("expected equivocation, got " + errString(err))
	}
	return pin
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// isEquivocation is a test-local predicate so the dead-pin construction
// reads like the model's PIN_DEAD.
func isEquivocation(err error) bool {
	_, ok := err.(*EquivocationError)
	return ok
}

// withNewRoot adds the model's OWNER1_NEW to f and returns the twin
// fixture whose "OWNER1" is that new key — the post-rotation signing
// shape (the model's FRootRotated / FChainRootRotated). The twin has
// its own build cache: specs are keyed by principal name.
func withNewRoot(f fixture) (base, rotated fixture) {
	if _, ok := f.signer["OWNER1_NEW"]; !ok {
		s := NewEdSigner(ed25519.NewKeyFromSeed(bytesFill(0x77)))
		if f.id["OWNER1"] == s.ActorID() { // detFixture seeds collide only by construction
			panic("OWNER1_NEW seed collides with OWNER1")
		}
		f.signer["OWNER1_NEW"] = s
		f.id["OWNER1_NEW"] = s.ActorID()
	}
	rotated = fixture{signer: map[string]Signer{}, id: map[string]ActorID{}, built: &sync.Map{}}
	for k, v := range f.signer {
		rotated.signer[k] = v
	}
	for k, v := range f.id {
		rotated.id[k] = v
	}
	rotated.signer["OWNER1"] = f.signer["OWNER1_NEW"]
	rotated.id["OWNER1"] = f.id["OWNER1_NEW"]
	return f, rotated
}

// withNewRootRapid is withNewRoot with a fresh OWNER1_NEW key.
func withNewRootRapid(t *rapid.T, f fixture) (base, rotated fixture) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewEdSigner(priv)
	f.signer["OWNER1_NEW"] = s
	f.id["OWNER1_NEW"] = s.ActorID()
	return withNewRoot(f)
}

// pinScenario is one draw of the model's pinned world: a connection-
// level bundle and a chain, each built either as today (OWNER1 signs)
// or post-rotation (OWNER1_NEW signs everything OWNER1 would), R's
// consents and held speak-as ALWAYS naming the old OWNER1 (the identity;
// the consent never moves), and R's pins {pin1, PIN_TRIVIAL2}.
type pinScenario struct {
	f       fixture // base: OWNER1 is the old key; OWNER1_NEW present
	kind    pinKind
	rotated bool // bundle / chain signed under OWNER1_NEW
	s       scenario
	cs      chainScenario
	pins    []*Pin
}

func buildPinScenario(base, rotated fixture, kind pinKind, rootRotated bool, p scenarioParams, cp chainParams) pinScenario {
	bs := buildScenario(base, p)
	bcs := buildChainScenario(base, cp)
	s, cs := bs, bcs
	if rootRotated {
		s = buildScenario(rotated, p)
		s.in.Receiver.Consents, s.in.Receiver.SpeakAs = bs.in.Receiver.Consents, bs.in.Receiver.SpeakAs
		s.att.Receiver.Consents, s.att.Receiver.SpeakAs = bs.att.Receiver.Consents, bs.att.Receiver.SpeakAs
		s.consents, s.held, s.heldAtt = bs.consents, bs.held, bs.heldAtt
		cs = buildChainScenario(rotated, cp)
		cs.consents, cs.held, cs.heldAtt = bcs.consents, bcs.held, bcs.heldAtt
	}
	pins := []*Pin{pinFor(base, kind), NewPin(base.id["OWNER2"], trivialPolicy(base.id["OWNER2"]))}
	return pinScenario{f: base, kind: kind, rotated: rootRotated, s: s, cs: cs, pins: pins}
}

func genPin(t *rapid.T, base, rotated fixture) pinScenario {
	p := scenarioParams{
		f1:        fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f1")),
		f2:        fault(rapid.IntRange(0, int(numFaults)-1).Draw(t, "f2")),
		audKind:   rapid.IntRange(0, 1).Draw(t, "audKind"),
		attKind:   rapid.IntRange(0, numAtts-1).Draw(t, "att"),
		attG:      rapid.SampledFrom(modelGroups).Draw(t, "attG"),
		hubSigned: rapid.Bool().Draw(t, "hubSigned"),
	}
	cp := chainParams{
		cf1:       cfault(rapid.IntRange(0, int(numCFaults)-1).Draw(t, "cf1")),
		cf2:       cfault(rapid.IntRange(0, int(numCFaults)-1).Draw(t, "cf2")),
		kind:      chainKind(rapid.IntRange(0, int(numKinds)-1).Draw(t, "kind")),
		verb:      rapid.SampledFrom(chainVerbs).Draw(t, "cv"),
		hubSigned: rapid.Bool().Draw(t, "cHub"),
		f1:        p.f1,
		f2:        p.f2,
		cPostage:  rapid.SampledFrom([]string{noPostage, postagePoW}).Draw(t, "cPostage"),
		attI:      0,
		attKind:   p.attKind,
		attT:      "R",
		attF:      "apid",
		attG:      p.attG,
		attE:      modelEndpoints[0],
	}
	kind := pinKind(rapid.IntRange(0, int(numPinKinds)-1).Draw(t, "pin"))
	rootRotated := rapid.Bool().Draw(t, "rootRotated")
	return buildPinScenario(base, rotated, kind, rootRotated, p, cp)
}

// withPins is the pinned receiver: the same Input, R holding pins.
func withPins(in Input, pins []*Pin) Input {
	in.Receiver.Pins = pins
	return in
}

// evalChainPinned is evalChain with R holding pins.
func evalChainPinned(id map[string]ActorID, s chainScenario, pins []*Pin) chainResult {
	ctx := newAuthCtx()
	r := Receiver{ID: id["R"], Consents: s.consents, SpeakAs: s.held, Pins: pins}
	vs, err := ctx.verifyChain(r, s.verb, s.chain, s.speakAs, s.signer, s.facet, testNOW)
	eff, _, pubErr := VerifyChain(r, s.verb, s.chain, s.speakAs, s.signer, s.facet, testNOW)
	return chainResult{verdicts: vs, err: err, pubEff: eff, pubErr: pubErr}
}

// vouchers ports the model's vouchers: everything signer k could be
// attributed to, hot keys removed (the loosest reading).
func vouchers(id map[string]ActorID, speakAs []Cert, k ActorID) []ActorID {
	var out []ActorID
	for _, w := range attributable(speakAs, k) {
		if !isHotKey(id, w) {
			out = append(out, w)
		}
	}
	return out
}

// toNewRoot ports the model's toNewRoot on a REAL cert: a consent
// addressed to OWNER1 is re-addressed to OWNER1_NEW and re-signed by
// whoever signed it (R, OTHER_R, or the FORGER claiming R — a forged
// consent stays forged).
func toNewRoot(f fixture, c Cert) Cert {
	if c.Aud != string(f.id["OWNER1"]) {
		return c
	}
	var signer Signer
	if Verify(c) == nil {
		for name, id := range f.id {
			if id == c.Iss {
				signer = f.signer[name]
			}
		}
	} else {
		signer = f.signer["FORGER"]
	}
	c.Aud = string(f.id["OWNER1_NEW"])
	c.Sig = nil
	out, err := Sign(c, signer)
	if err != nil {
		panic(err)
	}
	out.Iss = c.Iss
	return out
}

func anyLink(chain []Cert, pred func(Cert) bool) bool {
	for _, l := range chain {
		if pred(l) {
			return true
		}
	}
	return false
}

func subsetIDs(xs []ActorID, allowed ...ActorID) bool {
	for _, x := range xs {
		if !slices.Contains(allowed, x) {
			return false
		}
	}
	return true
}

// pinLaw is one inv*Pin* of authorize.qnt; hit says the antecedent
// fired (so the sweep can prove no law holds vacuously).
type pinLaw struct {
	name  string
	check func(ps pinScenario) (hit, ok bool)
}

var pinLaws = []pinLaw{
	// Trivial pins are the identity map: a receiver holding only pins
	// whose root is the identity itself judges exactly as one holding
	// none — the whole Result, and the chain's verdict set.
	{"invTrivialPinsPreserveVerdicts", func(ps pinScenario) (bool, bool) {
		if ps.kind != pinTrivial {
			return false, true
		}
		res, resPin := Authorize(ps.s.in), Authorize(withPins(ps.s.in, ps.pins))
		if !reflect.DeepEqual(res, resPin) {
			return true, false
		}
		cr, crPin := evalChain(ps.f.id, ps.cs, ps.cs.chain, ps.cs.speakAs, ps.cs.held), evalChainPinned(ps.f.id, ps.cs, ps.pins)
		return true, reflect.DeepEqual(cr.verdicts, crPin.verdicts) && cr.err == crPin.err &&
			reflect.DeepEqual(cr.pubEff, crPin.pubEff) && cr.pubErr == crPin.pubErr
	}},
	// The old root dies with its epoch: with OWNER1 rotated to
	// OWNER1_NEW, a cert that only OWNER1 could sign as admits nothing —
	// the consent to OWNER1 names the identity, not the key.
	{"invOldRootDiesOnRotation", func(ps pinScenario) (bool, bool) {
		if ps.kind != pinRotated {
			return false, true
		}
		id := ps.f.id
		hit, ok := false, true
		if subsetIDs(vouchers(id, ps.s.speakAs, ps.s.member.Iss), id["OWNER1"]) {
			hit = true
			ok = ok && !Authorize(withPins(ps.s.in, ps.pins)).OK
		}
		if anyLink(ps.cs.chain, func(l Cert) bool { return subsetIDs(vouchers(id, ps.cs.speakAs, l.Iss), id["OWNER1"]) }) {
			hit = true
			ok = ok && !evalChainPinned(id, ps.cs, ps.pins).accepted()
		}
		return hit, ok
	}},
	// The pin hop is exactly as strong as identity: with the pin
	// OWNER1→OWNER1_NEW held, the pinned receiver's verdict on ANY bundle
	// equals the unpinned receiver's verdict with every consent to OWNER1
	// re-addressed to OWNER1_NEW — same bundle, old key included.
	{"invNewRootAdmittedViaPin", func(ps pinScenario) (bool, bool) {
		if ps.kind != pinRotated {
			return false, true
		}
		twin := ps.s.in
		twin.Receiver.Consents = make([]Cert, len(ps.s.in.Receiver.Consents))
		for i, c := range ps.s.in.Receiver.Consents {
			twin.Receiver.Consents[i] = toNewRoot(ps.f, c)
		}
		return true, Authorize(withPins(ps.s.in, ps.pins)).OK == Authorize(twin).OK
	}},
	// A dead pin admits nothing: with OWNER1 dead at R, a cert only
	// OWNER1 or OWNER1_NEW could sign as admits nothing — the identity is
	// gone and the new key is a stranger under its own name.
	{"invDeadPinAdmitsNothing", func(ps pinScenario) (bool, bool) {
		if ps.kind != pinDead {
			return false, true
		}
		id := ps.f.id
		hit, ok := false, true
		if subsetIDs(vouchers(id, ps.s.speakAs, ps.s.member.Iss), id["OWNER1"], id["OWNER1_NEW"]) {
			hit = true
			ok = ok && !Authorize(withPins(ps.s.in, ps.pins)).OK
		}
		if anyLink(ps.cs.chain, func(l Cert) bool {
			return subsetIDs(vouchers(id, ps.cs.speakAs, l.Iss), id["OWNER1"], id["OWNER1_NEW"])
		}) {
			hit = true
			ok = ok && !evalChainPinned(id, ps.cs, ps.pins).accepted()
		}
		return hit, ok
	}},
	// Only a pin R HOLDS admits a new root: when no held pin is alive
	// with root OWNER1_NEW, a cert only OWNER1_NEW could sign as admits
	// nothing. (The model's `bundlePins` half — pins the CALLER carries —
	// has no Go counterpart: Bundle has no pins field, see
	// TestPinCallerPinsIgnored.)
	{"invCallerPinsIgnored", func(ps pinScenario) (bool, bool) {
		if ps.kind == pinRotated {
			return false, true
		}
		id := ps.f.id
		hit, ok := false, true
		if subsetIDs(vouchers(id, ps.s.speakAs, ps.s.member.Iss), id["OWNER1_NEW"]) {
			hit = true
			ok = ok && !Authorize(withPins(ps.s.in, ps.pins)).OK
		}
		if anyLink(ps.cs.chain, func(l Cert) bool { return subsetIDs(vouchers(id, ps.cs.speakAs, l.Iss), id["OWNER1_NEW"]) }) {
			hit = true
			ok = ok && !evalChainPinned(id, ps.cs, ps.pins).accepted()
		}
		return hit, ok
	}},
}

// TestPinLaws is the rapid suite: one property per law, same names as
// the model.
func TestPinLaws(t *testing.T) {
	for _, l := range pinLaws {
		l := l
		t.Run(l.name, func(t *testing.T) {
			rapid.Check(t, func(t *rapid.T) {
				base, rotated := withNewRootRapid(t, newFixture(t))
				ps := genPin(t, base, rotated)
				if _, ok := l.check(ps); !ok {
					t.Fatalf("%s violated (kind=%d rotated=%v faults=%v cfaults=%v)", l.name, ps.kind, ps.rotated, ps.s.f, ps.cs.f)
				}
			})
		})
	}
}

// TestPinTrivialSweep is the deterministic backbone of
// invTrivialPinsPreserveVerdicts: EVERY fault pair of the connection
// half (today's signing shape), judged identically with trivial pins
// and without — the acceptance of d0o0.3. The post-rotation shape and
// the rotated / dead pins join on the single-fault scenarios, so each
// other law's antecedent is reached deterministically too; the pairs
// are the rapid suite's.
func TestPinTrivialSweep(t *testing.T) {
	base, rotated := withNewRoot(detFixture(60))
	hits := map[string]int{}
	n := 0
	for f1 := fault(0); f1 < numFaults; f1++ {
		for f2 := f1; f2 < numFaults; f2++ {
			for _, hubSigned := range []bool{false, true} {
				if !hubSigned && (f1 >= fSpeakAsAMissing || f2 >= fSpeakAsAMissing) {
					continue
				}
				for _, rootRotated := range []bool{false, true} {
					single := f1 == fNone || f2 == fNone
					if rootRotated && !single {
						continue
					}
					p := scenarioParams{f1: f1, f2: f2, audKind: n % 2, attKind: n % numAtts, attG: modelGroups[n%2], hubSigned: hubSigned}
					cp := chainParams{cf1: cfault(n % int(numCFaults)), cf2: cfault((n / 3) % int(numCFaults)),
						kind: chainKind(n % int(numKinds)), verb: chainVerbs[n%2], hubSigned: hubSigned, f1: f1, f2: f2,
						cPostage: noPostage, attT: "R", attF: "apid", attG: modelGroups[n%2], attE: modelEndpoints[0]}
					kinds := numPinKinds
					if !single {
						kinds = pinTrivial + 1
					}
					for kind := pinKind(0); kind < kinds; kind++ {
						ps := buildPinScenario(base, rotated, kind, rootRotated, p, cp)
						for _, l := range pinLaws {
							hit, ok := l.check(ps)
							if !ok {
								t.Fatalf("%s violated: %+v rotated=%v kind=%d", l.name, p, rootRotated, kind)
							}
							if hit {
								hits[l.name]++
							}
						}
					}
					n++
				}
			}
		}
	}
	t.Logf("swept %d scenarios", n)
	for _, l := range pinLaws {
		t.Logf("%-32s antecedent hit %d times", l.name, hits[l.name])
		if hits[l.name] == 0 {
			t.Errorf("%s: antecedent never hit", l.name)
		}
	}
}

// TestPinRotatedAccepts is the model's pinRotatedTest witness: after
// OWNER1 rotates to OWNER1_NEW and R holds the rotation, a bundle signed
// under OWNER1_NEW is accepted against R's unchanged consent to OWNER1;
// the same bundle at a receiver with the trivial pin (or no pin) is
// rejected — OWNER1_NEW is a stranger there; and the OLD key's bundle,
// accepted today, is rejected at the rotated receiver.
func TestPinRotatedAccepts(t *testing.T) {
	base, rotated := withNewRoot(detFixture(70))
	p := scenarioParams{attG: "admins"}
	cp := chainParams{attT: "R", attF: "apid", attG: "admins", attE: modelEndpoints[0]}
	for _, hubSigned := range []bool{false, true} {
		p.hubSigned, cp.hubSigned = hubSigned, hubSigned
		old := buildPinScenario(base, rotated, pinRotated, false, p, cp)
		neu := buildPinScenario(base, rotated, pinRotated, true, p, cp)
		trivial := buildPinScenario(base, rotated, pinTrivial, true, p, cp)
		dead := buildPinScenario(base, rotated, pinDead, true, p, cp)

		// today: OWNER1 signs, no pins / trivial pin ⇒ accept
		if !Authorize(old.s.in).OK || !Authorize(withPins(old.s.in, []*Pin{pinFor(base, pinTrivial)})).OK {
			t.Fatalf("hub=%v: old root rejected without a rotation", hubSigned)
		}
		// rotated receiver: OWNER1_NEW's bundle admits under the consent to OWNER1
		if !Authorize(withPins(neu.s.in, neu.pins)).OK {
			t.Fatalf("hub=%v: new root rejected at the rotated receiver", hubSigned)
		}
		if !evalChainPinned(base.id, neu.cs, neu.pins).accepted() {
			t.Fatalf("hub=%v: new root's chain rejected at the rotated receiver", hubSigned)
		}
		// rotated receiver: the OLD key's bundle is dead
		if Authorize(withPins(old.s.in, old.pins)).OK {
			t.Fatalf("hub=%v: old root still admitted after rotation", hubSigned)
		}
		if evalChainPinned(base.id, old.cs, old.pins).accepted() {
			t.Fatalf("hub=%v: old root's chain still admitted after rotation", hubSigned)
		}
		// unrotated receiver: OWNER1_NEW is a stranger
		if Authorize(withPins(trivial.s.in, trivial.pins)).OK || Authorize(trivial.s.in).OK {
			t.Fatalf("hub=%v: new root admitted without the rotation held", hubSigned)
		}
		// dead receiver: neither key admits
		if Authorize(withPins(dead.s.in, dead.pins)).OK || Authorize(withPins(old.s.in, dead.pins)).OK {
			t.Fatalf("hub=%v: dead pin admitted a key", hubSigned)
		}
	}
}

// TestPinResolveHop pins the hop itself at the unit level (the model's
// resolve, table form): subtractive for pinned addresses, additive
// through Admits, one hop, EffExp/Via inherited from the admitting key.
func TestPinResolveHop(t *testing.T) {
	base, _ := withNewRoot(detFixture(80))
	id := base.id
	old, neu, o2 := id["OWNER1"], id["OWNER1_NEW"], id["OWNER2"]
	sa := base.build(certSpec{iss: "OWNER1", aud: string(id["HUB_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: modelVerbs, Groups: modelGroups}, exp: 8})
	saNew := base.build(certSpec{iss: "OWNER1_NEW", aud: string(id["HUB_A"]), can: VerbSpeakAs,
		cav: Caveats{Verbs: modelVerbs, Groups: modelGroups}, exp: 8})
	byOld := base.build(certSpec{iss: "OWNER1", aud: string(id["CALLER"]), can: VerbMember, cav: Caveats{Name: "x"}, exp: 10})
	byHub := base.build(certSpec{iss: "HUB_A", aud: string(id["CALLER"]), can: VerbMember, cav: Caveats{Name: "x"}, exp: 10})
	ids := func(ss []sovereign) []ActorID {
		out := make([]ActorID, len(ss))
		for i, s := range ss {
			out[i] = s.ID
		}
		return out
	}
	cases := []struct {
		name    string
		c       Cert
		speakAs []Cert
		pins    []*Pin
		want    []ActorID
	}{
		{"no pins: as today", byHub, []Cert{sa}, nil, []ActorID{id["HUB_A"], old}},
		{"trivial pin: identity map", byHub, []Cert{sa}, []*Pin{pinFor(base, pinTrivial)}, []ActorID{id["HUB_A"], old}},
		{"trivial pin, direct signer", byOld, nil, []*Pin{pinFor(base, pinTrivial)}, []ActorID{old}},
		{"rotated pin: old key subtracted", byOld, nil, []*Pin{pinFor(base, pinRotated)}, nil},
		{"rotated pin: old speak-as subtracted", byHub, []Cert{sa}, []*Pin{pinFor(base, pinRotated)}, []ActorID{id["HUB_A"]}},
		{"rotated pin: new root admitted as the identity", byHub, []Cert{saNew}, []*Pin{pinFor(base, pinRotated)}, []ActorID{id["HUB_A"], neu, old}},
		{"dead pin admits nothing", byHub, []Cert{sa, saNew}, []*Pin{pinFor(base, pinDead)}, []ActorID{id["HUB_A"], neu}},
		{"unrelated pin leaves the set alone", byHub, []Cert{sa}, []*Pin{NewPin(o2, trivialPolicy(o2))}, []ActorID{id["HUB_A"], old}},
	}
	ctx := newAuthCtx()
	for _, tc := range cases {
		got := ctx.resolve(tc.c, tc.speakAs, testNOW, nil, tc.pins)
		if !slices.Equal(ids(got), tc.want) {
			t.Errorf("%s: resolve = %v, want %v", tc.name, ids(got), tc.want)
		}
		for _, s := range got {
			if s.ViaPin != nil && s.ID != s.ViaPin.Address {
				t.Errorf("%s: ViaPin names %s for sovereign %s", tc.name, s.ViaPin.Address, s.ID)
			}
		}
	}
	// The pinned entry inherits the admitting key's expiry and Via: the
	// pin maps a vouching path to a name, it does not lengthen it.
	got := ctx.resolve(byHub, []Cert{saNew}, testNOW, nil, []*Pin{pinFor(base, pinRotated)})
	last := got[len(got)-1]
	if last.ID != old || last.EffExp != 8 || last.Via == nil || last.Via.Iss != neu || last.ViaPin == nil {
		t.Fatalf("pinned sovereign = %+v", last)
	}
}

// TestPinCallerPinsIgnored is invCallerPinsIgnored's `bundlePins` half,
// which the type system discharges: Bundle has no pins field, so a
// caller cannot present one. The test asserts the absence so a future
// field cannot be added without meeting this comment (the model's
// mutant p4, `pins ∪ bundlePins`, needs a channel that must not exist).
func TestPinCallerPinsIgnored(t *testing.T) {
	bt := reflect.TypeOf(Bundle{})
	for i := 0; i < bt.NumField(); i++ {
		f := bt.Field(i)
		if f.Type == reflect.TypeOf([]*Pin(nil)) || f.Type == reflect.TypeOf([]Pin(nil)) || f.Type == reflect.TypeOf([]Rotation(nil)) {
			t.Fatalf("Bundle carries %s %v: pins are receiver-held (ADR-0003/0010)", f.Name, f.Type)
		}
	}
	if _, ok := reflect.TypeOf(Receiver{}).FieldByName("Pins"); !ok {
		t.Fatal("Receiver.Pins missing: pins must be receiver-held")
	}
}
