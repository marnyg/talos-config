package cert

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gowebpki/jcs"
)

// Root rotation via a guardian-signed epoch chain (spike talos-config-
// aja2, design pins 1–4; oracle verification/quint/rotation.qnt, whose
// pure `verifies` / `view` / `admits` the functions below port 1:1).
//
// An identity is an address plus the epoch-0 POLICY {root, guardians,
// k} it commits to (pin 1). The guardian set is the identity's root;
// the root key is a hot key with a longer leash (pin 3). A ROTATION is
// k `rotate-root` certs with identical bodies {epoch, prev =
// H(policy_n), policy_{n+1}}, one per DISTINCT guardian of policy_n
// (pin 4); it verifies offline by signatures alone. A verifier keeps a
// PIN (its epoch-0 policy) plus every verifying rotation it has been
// shown and DERIVES its VIEW by folding from the pin one epoch at a
// time. Two verifying rotations from the same (epoch, prev) with
// different next, AT a prev the fold has reached, are EQUIVOCATION =
// capture: the identity is dead at this verifier until re-pinned out
// of band (pin 2, as refined by the model's FINDING: unrooted
// equivocation kills nothing, else anyone holds a kill-switch).
//
// Out of scope here, by the brief: CREATE2 address derivation, EIP-712
// typed signing, any chain code, persistence of the Pin, wiring into
// Authorize. The Go verifier accepts any ActorID scheme as a guardian
// (eth: or ed:); pin 5's "guardians are secp256k1" is the on-chain
// follower's constraint, not this package's.

// Rotation errors. The first four are the verifier's verdicts; the
// rest are assembly-time rejections (a cert that does not belong).
var (
	// ErrEquivocation: the pin's view is dead — two verifying rotations
	// from the same rooted (epoch, prev) name different policies. The
	// error value is an *EquivocationError carrying both bodies (the
	// self-certifying proof). Pin.Hold keeps the rotation.
	ErrEquivocation = errors.New("cert: rotation equivocation — identity dead at this pin")
	// ErrNotRooted: the rotation verified and is HELD, but its prev is
	// not (yet) a policy the fold from the pin reaches. Out-of-order
	// delivery or an invented prev look the same here; neither moves
	// the view (rotation.qnt invDeadOnlyByRootedEquivocation).
	ErrNotRooted = errors.New("cert: rotation held but not rooted at this pin")
	// ErrInsufficientGuardians: fewer than prev.K DISTINCT guardians of
	// prev signed the body.
	ErrInsufficientGuardians = errors.New("cert: rotation has fewer than k distinct guardian signatures")
	// ErrBodyMismatch: the certs do not share one (aud, can, cav, iat,
	// exp) body, or the body does not name (address, epoch, prev).
	ErrBodyMismatch = errors.New("cert: rotation certs do not share one body")
	// ErrNotGuardian: a cert's iss is not a guardian of prev.
	ErrNotGuardian = errors.New("cert: rotation signer is not a guardian of prev")
	// ErrAssemblyExpired: k signatures were not gathered inside exp
	// (pin 4: exp bounds ASSEMBLY, not validity — Pin never reads it).
	ErrAssemblyExpired = errors.New("cert: rotation assembled after exp")
	// ErrRotationCaveat: the v4 caveats {epoch, prev, policy} appear on
	// a verb other than rotate-root, or are missing/malformed on one.
	// It IS an ErrUnknownCaveat for consumers that only know
	// fail-closed.
	ErrRotationCaveat = fmt.Errorf("%w: rotation caveats", ErrUnknownCaveat)
	// ErrRotationNotChain: a rotate-root cert was offered where a chain
	// link, consent or grant was expected. A rotation is a transition,
	// not a presented credential (pin 4; invariant 6 untouched).
	ErrRotationNotChain = errors.New("cert: rotate-root is not a chain link")
)

// Policy is what an identity commits to at an epoch: the root key
// that delegates, the guardians that rotate, and the threshold k.
// Guardians are a SET; canonical form sorts and dedups them. K >= 1
// always; a policy with fewer than k guardians (including none) chose
// irrecoverability — legal, declared.
type Policy struct {
	Root      ActorID
	Guardians []ActorID
	K         int
}

// canonPolicy is the JCS shape Hash commits to: {"guardians":[sorted,
// distinct],"k":n,"root":"..."}.
type canonPolicy struct {
	Guardians []string `json:"guardians"`
	K         int      `json:"k"`
	Root      string   `json:"root"`
}

func (p Policy) canonical() canonPolicy {
	g := targetsToStrings(p.Guardians)
	slices.Sort(g)
	g = slices.Compact(g)
	return canonPolicy{Guardians: nonNil(g), K: p.K, Root: string(p.Root)}
}

// guardianSet is the distinct guardians as a set.
func (p Policy) guardianSet() map[ActorID]bool {
	s := make(map[ActorID]bool, len(p.Guardians))
	for _, g := range p.Guardians {
		s[g] = true
	}
	return s
}

// Validate checks the ids and k >= 1.
func (p Policy) Validate() error {
	if err := p.Root.Validate(); err != nil {
		return fmt.Errorf("policy root: %w", err)
	}
	for i, g := range p.Guardians {
		if err := g.Validate(); err != nil {
			return fmt.Errorf("policy guardians[%d]: %w", i, err)
		}
	}
	if p.K < 1 {
		return fmt.Errorf("policy k %d < 1", p.K)
	}
	return nil
}

// Hash is H(policy): lowercase hex of keccak256 over the JCS form of
// the canonical policy. It is the value a rotate-root's cav.prev
// carries and the key the fold chains on (rotation.qnt's policy id).
// keccak over sha256 because the eth scheme already pulls it in and
// the on-chain follower (pin 5) recomputes it natively; the JCS input
// is a v0 choice an EIP-712 struct hash may replace — any consumer
// must treat the string as opaque.
func (p Policy) Hash() string {
	raw, err := json.Marshal(p.canonical())
	if err != nil {
		panic("cert: policy marshal: " + err.Error()) // strings and ints only; unreachable
	}
	canon, err := jcs.Transform(raw)
	if err != nil {
		panic("cert: policy jcs: " + err.Error()) // valid JSON in; unreachable
	}
	return hex.EncodeToString(keccak256(canon))
}

// validPolicyHash: 64 lowercase hex chars.
func validPolicyHash(h string) bool {
	if len(h) != 64 || strings.ToLower(h) != h {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

func hasRotationCaveats(c Caveats) bool {
	return c.Epoch != 0 || c.Prev != "" || c.Policy != nil
}

// Rotation is one assembled k-of-n transition Prev → Next at Epoch for
// Address, with the guardian certs that prove it. A value built by
// hand is not trusted: Pin.Hold re-verifies it from Certs.
type Rotation struct {
	Address ActorID
	Epoch   int64
	Prev    Policy
	Next    Policy
	Certs   []Cert
}

// rotKey identifies a TRANSITION: same (epoch, prev, next) with other
// signers or exp is the same transition, not equivocation
// (rotation.qnt modelling note b).
type rotKey struct {
	Epoch      int64
	Prev, Next string
}

func (r Rotation) key() rotKey {
	return rotKey{Epoch: r.Epoch, Prev: r.Prev.Hash(), Next: r.Next.Hash()}
}

// bodyBytes is the cert's canonical bytes with iss blanked: the part of
// a rotate-root cert that every guardian must sign identically.
func bodyBytes(c Cert) ([]byte, error) {
	c.Iss = ""
	return canonicalBytes(c)
}

// AssembleRotation ports rotation.qnt `verifies` at the moment k
// signatures are gathered: every cert is rotate-root for address with
// one shared body naming (epoch, prev.Hash()), each verifies under its
// iss, each iss is a guardian of prev, and the DISTINCT issuers number
// at least prev.K (a duplicate guardian counts once). now is read ONLY
// here: assembly must complete while the body is live (exp > now).
// The Rotation it returns proves the transition for ever after —
// Pin.Hold never consults exp (pin 4; rotation.qnt
// invExpiredRotationStillProves).
func AssembleRotation(address ActorID, prev Policy, epoch int64, certs []Cert, now int64) (Rotation, error) {
	r, err := verifyRotation(address, prev, epoch, certs)
	if err != nil {
		return Rotation{}, err
	}
	if certs[0].Exp <= now {
		return Rotation{}, fmt.Errorf("%w: exp %d, now %d", ErrAssemblyExpired, certs[0].Exp, now)
	}
	return r, nil
}

// verifyRotation is `verifies` without the clock.
func verifyRotation(address ActorID, prev Policy, epoch int64, certs []Cert) (Rotation, error) {
	if err := prev.Validate(); err != nil {
		return Rotation{}, fmt.Errorf("%w: prev: %v", ErrBodyMismatch, err)
	}
	if len(certs) == 0 {
		return Rotation{}, fmt.Errorf("%w: no certs", ErrInsufficientGuardians)
	}
	head := certs[0]
	switch {
	case head.Can != VerbRotateRoot:
		return Rotation{}, fmt.Errorf("%w: can %q", ErrBodyMismatch, head.Can)
	case head.Aud != string(address):
		return Rotation{}, fmt.Errorf("%w: aud %q is not %q", ErrBodyMismatch, head.Aud, address)
	case head.Cav.Policy == nil:
		return Rotation{}, fmt.Errorf("%w: no cav.policy", ErrBodyMismatch)
	case head.Cav.Epoch != epoch:
		return Rotation{}, fmt.Errorf("%w: cav.epoch %d is not %d", ErrBodyMismatch, head.Cav.Epoch, epoch)
	case head.Cav.Prev != prev.Hash():
		return Rotation{}, fmt.Errorf("%w: cav.prev does not name prev", ErrBodyMismatch)
	}
	if err := head.Cav.Policy.Validate(); err != nil {
		return Rotation{}, fmt.Errorf("%w: cav.policy: %v", ErrBodyMismatch, err)
	}
	body, err := bodyBytes(head)
	if err != nil {
		return Rotation{}, err
	}
	guardians := prev.guardianSet()
	signed := map[ActorID]bool{}
	for i, c := range certs {
		b, err := bodyBytes(c)
		if err != nil {
			return Rotation{}, err
		}
		if string(b) != string(body) {
			return Rotation{}, fmt.Errorf("%w: certs[%d]", ErrBodyMismatch, i)
		}
		if !guardians[c.Iss] {
			return Rotation{}, fmt.Errorf("%w: certs[%d] iss %s", ErrNotGuardian, i, c.Iss)
		}
		if err := Verify(c); err != nil {
			return Rotation{}, fmt.Errorf("certs[%d]: %w", i, err)
		}
		signed[c.Iss] = true
	}
	if len(signed) < prev.K {
		return Rotation{}, fmt.Errorf("%w: %d of %d", ErrInsufficientGuardians, len(signed), prev.K)
	}
	return Rotation{Address: address, Epoch: epoch, Prev: prev, Next: *head.Cav.Policy, Certs: certs}, nil
}

// View is the verifier's derived position: the policy at Epoch reached
// by folding from the pin, Dead when the fold met a rooted
// equivocation (Proof = the equivocating rotations, each carrying its
// k certs — self-certifying, it travels), Chain = the rotations
// folded through, in epoch order.
type View struct {
	Epoch  int64
	Policy Policy
	Dead   bool
	Proof  []Rotation
	Chain  []Rotation
}

// EquivocationError is ErrEquivocation with the proof attached.
type EquivocationError struct {
	Address ActorID
	Epoch   int64  // the epoch the equivocating rotations enter
	Prev    Policy // the rooted policy they leave
	Proof   []Rotation
}

func (e *EquivocationError) Error() string {
	return fmt.Sprintf("%v: %s at epoch %d from %s (%d bodies)", ErrEquivocation, e.Address, e.Epoch, e.Prev.Hash()[:8], len(e.Proof))
}

func (e *EquivocationError) Unwrap() error { return ErrEquivocation }

// Pin is one verifier's commitment to an identity: the address, the
// epoch-0 policy (pin 1), and every rotation it has verified — ALL of
// them, rooted or not, since a rotation unrooted now may chain (or
// kill) once an earlier epoch arrives. The View is never stored; it is
// recomputed from Policy0 and Held, so the order rotations arrive in
// cannot matter (rotation.qnt `view`).
//
// Durability is the consumer's: this struct is volatile. A restart
// that loses Held revives a dead identity at epoch 0 and re-accepts a
// rotated-away root until shown the chain again (rotation.qnt
// residualDeadStaysDead / residualTruncatedHistory). The equivocation
// record is NOT safe-to-lose by ADR-0019's test — persist Held()
// (git, for talos) and re-feed it through Hold.
//
// Not safe for concurrent use.
type Pin struct {
	Address ActorID
	Policy0 Policy
	held    []Rotation // insertion order, one per rotKey
	index   map[rotKey]int
}

// NewPin commits to policy0 for address at epoch 0.
func NewPin(address ActorID, policy0 Policy) *Pin {
	return &Pin{Address: address, Policy0: policy0, index: map[rotKey]int{}}
}

// Held returns the verified rotations in the order they were held —
// what a consumer persists and re-feeds after restart.
func (p *Pin) Held() []Rotation { return slices.Clone(p.held) }

// Hold ports rotation.qnt showRotation: re-verify r from its certs
// (signatures only — never exp), keep it if it verifies, recompute the
// view. The verdict is about the VIEW after holding:
//
//   - nil: r is on the chain from the pin (the view advanced on it, or
//     it restates a transition already on the chain);
//   - ErrNotRooted: held, but the fold does not reach r.Prev at r.Epoch
//     (out-of-order, or an invented prev — the FINDING);
//   - *EquivocationError (Is ErrEquivocation): the view is dead; r is
//     still held, the proof is in the error and in View().Proof.
//
// A rotation that does not verify is NOT held and the error says why
// (ErrBodyMismatch, ErrNotGuardian, ErrInsufficientGuardians,
// ErrSigMismatch). Dead stays dead for every later Hold until the pin
// is rebuilt out of band.
func (p *Pin) Hold(r Rotation) error {
	if r.Address != p.Address {
		return fmt.Errorf("%w: rotation for %s offered to pin %s", ErrBodyMismatch, r.Address, p.Address)
	}
	checked, err := verifyRotation(p.Address, r.Prev, r.Epoch, r.Certs)
	if err != nil {
		return err
	}
	if checked.Next.Hash() != r.Next.Hash() {
		return fmt.Errorf("%w: Next does not match the signed cav.policy", ErrBodyMismatch)
	}
	k := checked.key()
	if _, seen := p.index[k]; !seen {
		p.index[k] = len(p.held)
		p.held = append(p.held, checked)
	}
	v := p.View()
	if v.Dead {
		return &EquivocationError{Address: p.Address, Epoch: v.Epoch + 1, Prev: v.Policy, Proof: v.Proof}
	}
	for _, c := range v.Chain {
		if c.key() == k {
			return nil
		}
	}
	return ErrNotRooted
}

// View ports rotation.qnt `view`: fold from the pin one epoch at a
// time over the held rotations. At epoch e the candidates are those
// with prev == the current policy and epoch == e (keyed on BOTH, since
// H(policy) recurs under P0→PA→P0); more than one distinct next is
// equivocation (dead, at the epoch before), none ends the chain, one
// advances. Monotone and order-independent by construction
// (invMonotone, invRootedAdvance, invEquivocationKills,
// invDeadOnlyByRootedEquivocation).
func (p *Pin) View() View {
	v := View{Policy: p.Policy0}
	cur := p.Policy0.Hash()
	for e := int64(1); ; e++ {
		var cands []Rotation
		nexts := map[string]bool{}
		for _, r := range p.held {
			if r.Epoch == e && r.Prev.Hash() == cur {
				cands = append(cands, r)
				nexts[r.Next.Hash()] = true
			}
		}
		switch {
		case len(nexts) > 1:
			slices.SortFunc(cands, func(a, b Rotation) int { return strings.Compare(a.Next.Hash(), b.Next.Hash()) })
			v.Dead, v.Proof = true, cands
			return v
		case len(nexts) == 0:
			return v
		}
		v.Epoch, v.Policy, cur = e, cands[0].Next, cands[0].Next.Hash()
		v.Chain = append(v.Chain, cands[0])
	}
}

// Admits ports rotation.qnt `admits`: a delegation signed by root is
// acceptable iff the view is alive and root is the CURRENT policy's
// root (pin 3: only the root delegates, old roots die with their epoch
// — invOldRootDiesOnAdvance, invDeadAcceptsNothing). The hub uses it
// to judge a speak-as's issuer; it is not wired into Authorize.
func (p *Pin) Admits(root ActorID) bool {
	v := p.View()
	return !v.Dead && v.Policy.Root == root
}
