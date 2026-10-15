package cert

import (
	"crypto/ed25519"
	"errors"
	"strconv"
	"strings"
	"testing"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"pgregory.net/rapid"
)

// Tests for rotation.go against verification/quint/rotation.qnt. The
// fixture mirrors the model's policy table: P0 {root 1, guardians
// {2,3,4,5}, k 2}, PA {root 6, same guardians}, PB {root 6, guardians
// {4,5,6}}, PT the thief's target {root 1, guardians {1}, k 1}.

type rotFixture struct {
	t              *testing.T
	address        ActorID
	signer         map[int]Signer
	id             map[int]ActorID
	P0, PA, PB, PT Policy
}

func newRotFixture(t *testing.T) rotFixture {
	t.Helper()
	f := rotFixture{t: t, signer: map[int]Signer{}, id: map[int]ActorID{}}
	for k := 1; k <= 7; k++ {
		s := NewEdSigner(ed25519.NewKeyFromSeed(bytesFill(byte(100 + k))))
		f.signer[k] = s
		f.id[k] = s.ActorID()
	}
	// The identity's address is an eth id (a CREATE2 account in pin 1);
	// derivation is out of scope, any valid ActorID will do.
	f.address = ActorID("eth:0x" + strings.Repeat("ab", 20))
	g := func(ks ...int) []ActorID {
		out := make([]ActorID, len(ks))
		for i, k := range ks {
			out[i] = f.id[k]
		}
		return out
	}
	f.P0 = Policy{Root: f.id[1], Guardians: g(2, 3, 4, 5), K: 2}
	f.PA = Policy{Root: f.id[6], Guardians: g(2, 3, 4, 5), K: 2}
	f.PB = Policy{Root: f.id[6], Guardians: g(4, 5, 6), K: 2}
	f.PT = Policy{Root: f.id[1], Guardians: g(1), K: 1}
	return f
}

const rotEXP = int64(100)

// certs signs one rotate-root body prev→next at epoch with each signer.
func (f rotFixture) certs(prev, next Policy, epoch int64, exp int64, signers ...int) []Cert {
	f.t.Helper()
	body := Cert{Aud: string(f.address), Can: VerbRotateRoot,
		Cav: Caveats{Epoch: epoch, Prev: prev.Hash(), Policy: &next}, Iat: 1, Exp: exp}
	out := make([]Cert, 0, len(signers))
	for _, s := range signers {
		c, err := Sign(body, f.signer[s])
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// rot assembles prev→next at epoch signed by signers, at now = 1.
func (f rotFixture) rot(prev, next Policy, epoch int64, signers ...int) Rotation {
	f.t.Helper()
	r, err := AssembleRotation(f.address, prev, epoch, f.certs(prev, next, epoch, rotEXP, signers...), 1)
	if err != nil {
		f.t.Fatalf("assemble %d→: %v", epoch, err)
	}
	return r
}

func (f rotFixture) pin() *Pin { return NewPin(f.address, f.P0) }

func wantView(t *testing.T, p *Pin, epoch int64, pol Policy, dead bool) {
	t.Helper()
	v := p.View()
	if v.Epoch != epoch || v.Policy.Hash() != pol.Hash() || v.Dead != dead {
		t.Fatalf("view = {epoch %d, policy %s, dead %v}, want {%d, %s, %v}",
			v.Epoch, v.Policy.Hash()[:8], v.Dead, epoch, pol.Hash()[:8], dead)
	}
}

// --- Policy ---------------------------------------------------------

func TestPolicyHashCanonical(t *testing.T) {
	f := newRotFixture(t)
	a := Policy{Root: f.id[1], Guardians: []ActorID{f.id[3], f.id[2], f.id[2]}, K: 2}
	b := Policy{Root: f.id[1], Guardians: []ActorID{f.id[2], f.id[3]}, K: 2}
	if a.Hash() != b.Hash() {
		t.Fatal("guardian order / duplicates changed the hash")
	}
	if !validPolicyHash(a.Hash()) {
		t.Fatalf("hash not 64 lowercase hex: %s", a.Hash())
	}
	c := b
	c.K = 3
	if c.Hash() == b.Hash() {
		t.Fatal("k not in the hash")
	}
	if err := (Policy{Root: f.id[1], K: 0}).Validate(); err == nil {
		t.Fatal("k=0 accepted")
	}
	if err := (Policy{Root: f.id[1], K: 1}).Validate(); err != nil {
		t.Fatalf("no-guardian (irrecoverable) policy rejected: %v", err)
	}
}

// --- AssembleRotation (rotation.qnt `verifies`) ----------------------

func TestRotationAssembleHappy(t *testing.T) {
	f := newRotFixture(t)
	r := f.rot(f.P0, f.PA, 1, 4, 5)
	if r.Epoch != 1 || r.Prev.Hash() != f.P0.Hash() || r.Next.Hash() != f.PA.Hash() || len(r.Certs) != 2 {
		t.Fatalf("rotation = %+v", r)
	}
	// Extra signatures beyond k are fine.
	if _, err := AssembleRotation(f.address, f.P0, 1, f.certs(f.P0, f.PA, 1, rotEXP, 2, 3, 4, 5), 1); err != nil {
		t.Fatal(err)
	}
	// An eth guardian is just a guardian (scheme-agnostic).
	priv, _ := secp.GeneratePrivateKey()
	eth := NewEthSigner(priv)
	pe := Policy{Root: f.id[1], Guardians: []ActorID{eth.ActorID(), f.id[2]}, K: 1}
	body := Cert{Aud: string(f.address), Can: VerbRotateRoot, Cav: Caveats{Epoch: 1, Prev: pe.Hash(), Policy: &f.PA}, Iat: 1, Exp: rotEXP}
	c, err := Sign(body, eth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AssembleRotation(f.address, pe, 1, []Cert{c}, 1); err != nil {
		t.Fatalf("eth guardian rejected: %v", err)
	}
}

func TestRotationAssembleRejects(t *testing.T) {
	f := newRotFixture(t)
	cases := []struct {
		name  string
		certs []Cert
		prev  Policy
		epoch int64
		now   int64
		want  error
	}{
		{"k-1 guardians", f.certs(f.P0, f.PA, 1, rotEXP, 4), f.P0, 1, 1, ErrInsufficientGuardians},
		{"duplicate guardian counted once", f.certs(f.P0, f.PA, 1, rotEXP, 4, 4), f.P0, 1, 1, ErrInsufficientGuardians},
		{"no certs", nil, f.P0, 1, 1, ErrInsufficientGuardians},
		{"non-guardian signer", f.certs(f.P0, f.PA, 1, rotEXP, 4, 6), f.P0, 1, 1, ErrNotGuardian},
		{"root alone is not a guardian", f.certs(f.P0, f.PA, 1, rotEXP, 1, 1), f.P0, 1, 1, ErrNotGuardian},
		{"body mismatch: different next", append(f.certs(f.P0, f.PA, 1, rotEXP, 4), f.certs(f.P0, f.PB, 1, rotEXP, 5)...), f.P0, 1, 1, ErrBodyMismatch},
		{"body mismatch: different exp", append(f.certs(f.P0, f.PA, 1, rotEXP, 4), f.certs(f.P0, f.PA, 1, rotEXP+1, 5)...), f.P0, 1, 1, ErrBodyMismatch},
		{"wrong epoch", f.certs(f.P0, f.PA, 2, rotEXP, 4, 5), f.P0, 1, 1, ErrBodyMismatch},
		{"wrong prev", f.certs(f.PA, f.PB, 1, rotEXP, 4, 5), f.P0, 1, 1, ErrBodyMismatch},
		{"expired at assembly", f.certs(f.P0, f.PA, 1, rotEXP, 4, 5), f.P0, 1, rotEXP, ErrAssemblyExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AssembleRotation(f.address, tc.prev, tc.epoch, tc.certs, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// Wrong address / tampered signature / wrong verb.
	certs := f.certs(f.P0, f.PA, 1, rotEXP, 4, 5)
	if _, err := AssembleRotation(ActorID("eth:0x"+strings.Repeat("cd", 20)), f.P0, 1, certs, 1); !errors.Is(err, ErrBodyMismatch) {
		t.Fatalf("other address: %v", err)
	}
	tampered := []Cert{certs[0], certs[1]}
	tampered[1].Sig = certs[0].Sig
	if _, err := AssembleRotation(f.address, f.P0, 1, tampered, 1); !errors.Is(err, ErrSigMismatch) {
		t.Fatalf("tampered sig: %v", err)
	}
	inv := Cert{Aud: string(f.address), Can: VerbInvoke, Iat: 1, Exp: rotEXP}
	inv, _ = Sign(inv, f.signer[4])
	if _, err := AssembleRotation(f.address, f.P0, 1, []Cert{inv}, 1); !errors.Is(err, ErrBodyMismatch) {
		t.Fatalf("invoke cert: %v", err)
	}
}

// --- Pin (rotation.qnt `view` / `admits`) -----------------------------

func TestPinHappyAdvance(t *testing.T) {
	f := newRotFixture(t)
	p := f.pin()
	wantView(t, p, 0, f.P0, false)
	if !p.Admits(f.id[1]) || p.Admits(f.id[6]) {
		t.Fatal("epoch 0 admits the wrong root")
	}
	if err := p.Hold(f.rot(f.P0, f.PA, 1, 4, 5)); err != nil {
		t.Fatalf("hold: %v", err)
	}
	wantView(t, p, 1, f.PA, false)
	// invOldRootDiesOnAdvance: the old root is no longer admitted.
	if p.Admits(f.id[1]) || !p.Admits(f.id[6]) {
		t.Fatal("old root admitted after advance / new root not admitted")
	}
	if err := p.Hold(f.rot(f.PA, f.PB, 2, 2, 3)); err != nil {
		t.Fatalf("hold 2: %v", err)
	}
	wantView(t, p, 2, f.PB, false)
	if len(p.View().Chain) != 2 || len(p.Held()) != 2 {
		t.Fatalf("chain/held = %d/%d", len(p.View().Chain), len(p.Held()))
	}
	// Same transition from other signers is the same transition.
	if err := p.Hold(f.rot(f.P0, f.PA, 1, 2, 3)); err != nil {
		t.Fatalf("restating a chained transition: %v", err)
	}
	wantView(t, p, 2, f.PB, false)
}

func TestPinHoldRejectsUnverified(t *testing.T) {
	f := newRotFixture(t)
	p := f.pin()
	// A hand-built Rotation whose Next disagrees with its signed certs.
	r := f.rot(f.P0, f.PA, 1, 4, 5)
	r.Next = f.PB
	if err := p.Hold(r); !errors.Is(err, ErrBodyMismatch) {
		t.Fatalf("forged Next: %v", err)
	}
	// k-1 certs smuggled into a struct literal.
	r = f.rot(f.P0, f.PA, 1, 4, 5)
	r.Certs = r.Certs[:1]
	if err := p.Hold(r); !errors.Is(err, ErrInsufficientGuardians) {
		t.Fatalf("k-1: %v", err)
	}
	// Another identity's rotation.
	r = f.rot(f.P0, f.PA, 1, 4, 5)
	r.Address = ActorID("eth:0x" + strings.Repeat("cd", 20))
	if err := p.Hold(r); !errors.Is(err, ErrBodyMismatch) {
		t.Fatalf("other address: %v", err)
	}
	if len(p.Held()) != 0 {
		t.Fatal("an unverified rotation was held")
	}
	wantView(t, p, 0, f.P0, false)
}

func TestPinNotRootedIsHeld(t *testing.T) {
	f := newRotFixture(t)
	p := f.pin()
	// prev = PA, which the pin cannot (yet) resolve.
	err := p.Hold(f.rot(f.PA, f.PB, 2, 2, 3))
	if !errors.Is(err, ErrNotRooted) {
		t.Fatalf("err = %v, want ErrNotRooted", err)
	}
	if len(p.Held()) != 1 {
		t.Fatal("unrooted rotation was not held")
	}
	wantView(t, p, 0, f.P0, false)
}

// Out-of-order delivery converges: epoch 2 before epoch 1.
func TestPinOutOfOrderConverges(t *testing.T) {
	f := newRotFixture(t)
	r1, r2 := f.rot(f.P0, f.PA, 1, 4, 5), f.rot(f.PA, f.PB, 2, 2, 3)
	p := f.pin()
	if err := p.Hold(r2); !errors.Is(err, ErrNotRooted) {
		t.Fatalf("epoch 2 first: %v", err)
	}
	wantView(t, p, 0, f.P0, false)
	if err := p.Hold(r1); err != nil {
		t.Fatalf("epoch 1 second: %v", err)
	}
	wantView(t, p, 2, f.PB, false)
	q := f.pin()
	_ = q.Hold(r1)
	_ = q.Hold(r2)
	if q.View().Policy.Hash() != p.View().Policy.Hash() || q.View().Epoch != p.View().Epoch {
		t.Fatal("order changed the view")
	}
}

// Rooted equivocation after the pin advanced on one side: dead, with
// both bodies as proof (invEquivocationKills, either order).
func TestEquivocationAfterAdvanceKills(t *testing.T) {
	f := newRotFixture(t)
	hon, bad := f.rot(f.P0, f.PA, 1, 4, 5), f.rot(f.P0, f.PT, 1, 2, 3)
	for _, order := range [][]Rotation{{hon, bad}, {bad, hon}} {
		p := f.pin()
		if err := p.Hold(order[0]); err != nil {
			t.Fatal(err)
		}
		wantView(t, p, 1, order[0].Next, false)
		err := p.Hold(order[1])
		var ee *EquivocationError
		if !errors.Is(err, ErrEquivocation) || !errors.As(err, &ee) {
			t.Fatalf("err = %v, want *EquivocationError", err)
		}
		if ee.Epoch != 1 || ee.Prev.Hash() != f.P0.Hash() || len(ee.Proof) != 2 {
			t.Fatalf("proof = %+v", ee)
		}
		for _, r := range ee.Proof {
			if len(r.Certs) < f.P0.K {
				t.Fatal("proof body is not self-certifying")
			}
		}
		wantView(t, p, 0, f.P0, true)
		// invDeadAcceptsNothing
		for _, k := range []int{1, 6} {
			if p.Admits(f.id[k]) {
				t.Fatalf("dead pin admits %d", k)
			}
		}
		// Dead stays dead: a later honest rotation does not revive it.
		if err := p.Hold(f.rot(f.PA, f.PB, 2, 2, 3)); !errors.Is(err, ErrEquivocation) {
			t.Fatalf("after death: %v", err)
		}
		wantView(t, p, 0, f.P0, true)
		if len(p.Held()) != 3 {
			t.Fatal("rotations dropped after death")
		}
	}
}

// Equivocation held BEFORE the chain reaches its prev is latent: not
// dead yet, dead the moment the chain gets there.
func TestEquivocationLatentUntilReached(t *testing.T) {
	f := newRotFixture(t)
	p := f.pin()
	a, b := f.rot(f.PA, f.PB, 2, 2, 3), f.rot(f.PA, f.P0, 2, 4, 5)
	if err := p.Hold(a); !errors.Is(err, ErrNotRooted) {
		t.Fatal(err)
	}
	if err := p.Hold(b); !errors.Is(err, ErrNotRooted) {
		t.Fatalf("latent pair reported as %v", err)
	}
	wantView(t, p, 0, f.P0, false)
	if !p.Admits(f.id[1]) {
		t.Fatal("alive pin at epoch 0 rejects its root")
	}
	if err := p.Hold(f.rot(f.P0, f.PA, 1, 4, 5)); !errors.Is(err, ErrEquivocation) {
		t.Fatalf("reaching the pair: %v", err)
	}
	wantView(t, p, 1, f.PA, true)
}

// The FINDING: an invented policy with the thief's keys as guardians,
// equivocated from, kills nothing (invDeadOnlyByRootedEquivocation).
func TestEquivocationUnrootedDoesNotKill(t *testing.T) {
	f := newRotFixture(t)
	p := f.pin()
	invented := Policy{Root: f.id[7], Guardians: []ActorID{f.id[7]}, K: 1}
	x, y := f.rot(invented, f.PT, 1, 7), f.rot(invented, f.PB, 1, 7)
	if err := p.Hold(x); !errors.Is(err, ErrNotRooted) {
		t.Fatal(err)
	}
	if err := p.Hold(y); !errors.Is(err, ErrNotRooted) {
		t.Fatalf("unrooted equivocation reported as %v", err)
	}
	wantView(t, p, 0, f.P0, false)
	if !p.Admits(f.id[1]) {
		t.Fatal("stranger's equivocation killed the identity")
	}
	// Nor does the same at epoch 2 from a prev the chain reaches at a
	// different epoch (epoch in the key).
	if err := p.Hold(f.rot(f.P0, f.PA, 1, 4, 5)); err != nil {
		t.Fatal(err)
	}
	wantView(t, p, 1, f.PA, false)
}

// Recurrence P0→PA→P0→PB: H(P0) appears at epochs 0 and 2; the epoch
// in the key keeps the epoch-3 rotation from P0 apart from the epoch-1
// one (modelling note a).
func TestPinRecurrenceIsNotEquivocation(t *testing.T) {
	f := newRotFixture(t)
	p := f.pin()
	for i, r := range []Rotation{
		f.rot(f.P0, f.PA, 1, 4, 5),
		f.rot(f.PA, f.P0, 2, 2, 3),
		f.rot(f.P0, f.PB, 3, 4, 5),
	} {
		if err := p.Hold(r); err != nil {
			t.Fatalf("hold %d: %v", i+1, err)
		}
	}
	wantView(t, p, 3, f.PB, false)
	if !p.Admits(f.id[6]) || p.Admits(f.id[1]) {
		t.Fatal("wrong root admitted after recurrence")
	}
}

// exp bounds assembly, not validity: a rotation assembled in time is
// Held long after its exp (invExpiredRotationStillProves), while
// assembly after exp is refused.
func TestRotationExpiredStillProves(t *testing.T) {
	f := newRotFixture(t)
	certs := f.certs(f.P0, f.PA, 1, 10, 4, 5)
	if _, err := AssembleRotation(f.address, f.P0, 1, certs, 10); !errors.Is(err, ErrAssemblyExpired) {
		t.Fatalf("assembly at exp: %v", err)
	}
	r, err := AssembleRotation(f.address, f.P0, 1, certs, 9)
	if err != nil {
		t.Fatal(err)
	}
	// "Later": Pin has no clock; it holds the same value whatever now is.
	p := f.pin()
	if err := p.Hold(r); err != nil {
		t.Fatalf("expired rotation refused: %v", err)
	}
	wantView(t, p, 1, f.PA, false)
	if !p.Admits(f.id[6]) || p.Admits(f.id[1]) {
		t.Fatal("expired rotation did not move the root")
	}
}

// Property: for any subset of a fixed rotation pool, every permutation
// yields the same View (order independence), the view is a chain from
// the pin, and dead ⇔ a rooted equivocating pair exists.
func TestPinViewLaws(t *testing.T) {
	f := newRotFixture(t)
	pool := []Rotation{
		f.rot(f.P0, f.PA, 1, 4, 5), f.rot(f.P0, f.PT, 1, 2, 3), f.rot(f.P0, f.PB, 1, 2, 5),
		f.rot(f.PA, f.PB, 2, 2, 3), f.rot(f.PA, f.P0, 2, 4, 5),
		f.rot(f.PB, f.PA, 3, 4, 5), f.rot(f.P0, f.PB, 3, 4, 5),
		f.rot(f.PT, f.PB, 2, 1), f.rot(f.PT, f.PA, 2, 1),
	}
	rapid.Check(t, func(rt *rapid.T) {
		idx := rapid.SliceOfDistinct(rapid.IntRange(0, len(pool)-1), rapid.ID[int]).Draw(rt, "subset")
		perm := rapid.Permutation(idx).Draw(rt, "perm")
		p := f.pin()
		for _, i := range idx {
			_ = p.Hold(pool[i])
		}
		q := f.pin()
		for _, i := range perm {
			_ = q.Hold(pool[i])
		}
		v, w := p.View(), q.View()
		if v.Epoch != w.Epoch || v.Policy.Hash() != w.Policy.Hash() || v.Dead != w.Dead {
			rt.Fatalf("order-dependent view: %+v vs %+v", v, w)
		}
		// invRootedAdvance: chain from the pin, one per epoch.
		prev := f.P0.Hash()
		for i, c := range v.Chain {
			if c.Epoch != int64(i+1) || c.Prev.Hash() != prev {
				rt.Fatalf("chain link %d not rooted", i)
			}
			prev = c.Next.Hash()
		}
		if int64(len(v.Chain)) != v.Epoch || (v.Epoch > 0 && v.Chain[len(v.Chain)-1].Next.Hash() != v.Policy.Hash()) {
			rt.Fatalf("epoch/policy disagree with chain")
		}
		// invEquivocationKills ⇔ invDeadOnlyByRootedEquivocation.
		rooted := map[string]bool{f.P0.Hash() + ":0": true}
		for _, c := range v.Chain {
			rooted[c.Next.Hash()+":"+itoa(c.Epoch)] = true
		}
		pair := false
		held := p.Held()
		for _, a := range held {
			for _, b := range held {
				if a.Epoch == b.Epoch && a.Prev.Hash() == b.Prev.Hash() && a.Next.Hash() != b.Next.Hash() &&
					rooted[a.Prev.Hash()+":"+itoa(a.Epoch-1)] {
					pair = true
				}
			}
		}
		if pair != v.Dead {
			rt.Fatalf("dead = %v but rooted pair = %v", v.Dead, pair)
		}
		if v.Dead && len(v.Proof) < 2 {
			rt.Fatal("dead without proof")
		}
	})
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// --- wire + chain boundaries ---------------------------------------

func TestDecodeRotationRoundTrip(t *testing.T) {
	f := newRotFixture(t)
	c := f.certs(f.P0, f.PA, 1, rotEXP, 4)[0]
	enc, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"epoch":1`, `"prev":"` + f.P0.Hash() + `"`, `"policy":{`, `"k":2`} {
		if !strings.Contains(string(enc), key) {
			t.Fatalf("encoded rotation lacks %s: %s", key, enc)
		}
	}
	dec, err := DecodeCert(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(dec); err != nil {
		t.Fatalf("decoded rotation fails verify: %v", err)
	}
	if dec.Cav.Policy == nil || dec.Cav.Policy.Hash() != f.PA.Hash() || dec.Cav.Epoch != 1 || dec.Cav.Prev != f.P0.Hash() {
		t.Fatalf("rotation caveats lost: %+v", dec.Cav)
	}
	// And the decoded cert still assembles.
	if _, err := AssembleRotation(f.address, f.P0, 1, append([]Cert{dec}, f.certs(f.P0, f.PA, 1, rotEXP, 5)...), 1); err != nil {
		t.Fatalf("decoded cert does not assemble: %v", err)
	}
	// Non-rotation certs encode without the v4 keys.
	inv, _ := Sign(Cert{Aud: string(f.address), Can: VerbInvoke, Iat: 1, Exp: 2}, f.signer[1])
	enc, _ = Encode(inv)
	for _, key := range []string{`"epoch"`, `"prev"`, `"policy"`} {
		if strings.Contains(string(enc), key) {
			t.Fatalf("invoke cert carries %s: %s", key, enc)
		}
	}
}

func TestDecodeRotationCaveatsRejectedElsewhere(t *testing.T) {
	f := newRotFixture(t)
	id := "ed:" + strings.Repeat("00", 32)
	base := `{"iss":"` + id + `","aud":"` + id + `","can":"%s","cav":{"target":[],"facet":[],"groups":[],"name":"","delegable":false,"verbs":[]%s},"iat":1,"exp":2,"sig":""}`
	policy := `,"policy":{"root":"` + id + `","guardians":["` + id + `"],"k":1}`
	cases := []struct{ name, can, extra string }{
		{"invoke+epoch", "invoke", `,"epoch":1`},
		{"invoke+prev", "invoke", `,"prev":"` + f.P0.Hash() + `"`},
		{"member+policy", "member", policy},
		{"rotate-root without policy", "rotate-root", `,"epoch":1,"prev":"` + f.P0.Hash() + `"`},
		{"rotate-root epoch 0", "rotate-root", `,"epoch":0,"prev":"` + f.P0.Hash() + `"` + policy},
		{"rotate-root bad prev", "rotate-root", `,"epoch":1,"prev":"nope"` + policy},
		{"rotate-root k 0", "rotate-root", `,"epoch":1,"prev":"` + f.P0.Hash() + `","policy":{"root":"` + id + `","guardians":[],"k":0}`},
		{"rotate-root bad guardian id", "rotate-root", `,"epoch":1,"prev":"` + f.P0.Hash() + `","policy":{"root":"` + id + `","guardians":["bob"],"k":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(strings.Replace(strings.Replace(base, "%s", tc.can, 1), "%s", tc.extra, 1))
			if _, err := DecodeCert(raw); err == nil {
				t.Fatalf("accepted: %s", raw)
			} else if !errors.Is(err, ErrUnknownCaveat) && !strings.Contains(err.Error(), "policy") {
				t.Fatalf("unexpected error class: %v", err)
			}
		})
	}
	// Well-formed rotate-root decodes.
	raw := []byte(strings.Replace(strings.Replace(base, "%s", "rotate-root", 1), "%s", `,"epoch":1,"prev":"`+f.P0.Hash()+`"`+policy, 1))
	if _, err := DecodeCert(raw); err != nil {
		t.Fatalf("well-formed rotate-root rejected: %v", err)
	}
	// In-memory: signing an invoke cert carrying rotation caveats fails.
	if _, err := Sign(Cert{Aud: string(f.address), Can: VerbInvoke, Cav: Caveats{Epoch: 1}, Iat: 1, Exp: 2}, f.signer[1]); !errors.Is(err, ErrRotationCaveat) {
		t.Fatalf("signed invoke with epoch: %v", err)
	}
}

// A rotate-root cert never roots, links, or attenuates a chain, and a
// bundle carrying one is rejected by Authorize (fail-closed).
func TestRotationRejectedInChains(t *testing.T) {
	f := newRotFixture(t)
	rc := f.certs(f.P0, f.PA, 1, rotEXP, 4)[0]
	if _, err := Attenuate(rc, rc); !errors.Is(err, ErrRotationNotChain) {
		t.Fatalf("Attenuate: %v", err)
	}
	inv := Cert{Cav: Caveats{Delegable: true}, Can: VerbInvoke}
	if _, err := Attenuate(inv, rc); !errors.Is(err, ErrRotationNotChain) {
		t.Fatalf("Attenuate child: %v", err)
	}
	recv := Receiver{ID: f.id[4], Consents: []Cert{rc}}
	if _, _, err := VerifyChain(recv, VerbRotateRoot, nil, nil, f.id[5], "apid", 1); !errors.Is(err, ErrRotationNotChain) {
		t.Fatalf("VerifyChain: %v", err)
	}

	// Authorize: R consents to OWNER1 (f.id[1]); a rotate-root cert
	// signed by OWNER1 is offered as the grant, and as the member cert.
	R, owner, caller := f.signer[4], f.signer[1], f.signer[5]
	consent, _ := Sign(Cert{Aud: string(owner.ActorID()), Can: VerbInvoke,
		Cav: Caveats{Target: []ActorID{R.ActorID()}, Facet: []string{"apid"}, Delegable: true}, Iat: 1, Exp: 100}, R)
	member, _ := Sign(Cert{Aud: string(caller.ActorID()), Can: VerbMember, Cav: Caveats{Name: "c"}, Iat: 1, Exp: 100}, owner)
	rotByOwner := Cert{Aud: string(f.address), Can: VerbRotateRoot, Cav: Caveats{Epoch: 1, Prev: f.P0.Hash(), Policy: &f.PA}, Iat: 1, Exp: 100}
	rotByOwner, _ = Sign(rotByOwner, owner)
	grant, _ := Sign(Cert{Aud: string(caller.ActorID()), Can: VerbInvoke,
		Cav: Caveats{Target: []ActorID{R.ActorID()}, Facet: []string{"apid"}}, Iat: 1, Exp: 100}, owner)
	in := Input{
		Receiver: Receiver{ID: R.ActorID(), Consents: []Cert{consent}}, AcceptTable: map[string]string{"mesh/apid/v1": "apid"},
		Blocklist: map[ActorID]bool{}, Now: testNOW, ALPN: "mesh/apid/v1", Peer: caller.ActorID(),
		Bundle: Bundle{Member: member, Grants: []Cert{grant}},
	}
	if !Authorize(in).OK {
		t.Fatal("control bundle rejected")
	}
	in.Bundle.Grants = []Cert{rotByOwner}
	res := Authorize(in)
	if res.OK {
		t.Fatal("rotate-root grant admitted")
	}
	for _, c := range res.Verified {
		if c.Can == VerbRotateRoot {
			t.Fatal("rotate-root cert fed the mark")
		}
	}
	in.Bundle = Bundle{Member: rotByOwner, Grants: []Cert{grant}}
	if Authorize(in).OK {
		t.Fatal("rotate-root member admitted")
	}
}
