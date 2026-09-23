// Package spawn is the parent side of spawning (sketch § Spawning;
// protocol ADR-0008, ADR-0009; invariant 13): the Spawner on an actor
// that rents compute from a provisioner, hands the newborn an Intro,
// answers its #birth knock with a starter Kit, and resolves the Spawn
// promise; and Born, the child side that exchanges the intro for the
// kit. Nothing here is a verifier special case — birth is Send plus a
// handler, admitted by an ordinary aud-"*" consent.
//
// # The handshake
//
//  1. Spawn mints a nonce and a per-spawn BIRTH CONSENT {iss: P, aud:
//     "*", can: invoke, cav: {target: [P], facet: [#birth], postage},
//     exp: now + window} — the frontdoor's shape (actor.Frontdoor) on a
//     dedicated facet — installs it beside P's other roots, records the
//     nonce in the pending-spawn table, and sends #spawn {image,
//     params: intro, until: consent.exp} to the provisioner. The intro
//     {parent, location, consent, nonce} is an opaque blob to it
//     (invariant 13). #spawn replies {lease}.
//  2. The child boots, mints its key, and Born(intro) sends an
//     ordinary stamped envelope {nonce} to P#birth: the birth consent
//     is presented as its chain (VerifyChain folds a chain that begins
//     with the rooting consent without it) and pays the postage the
//     consent names — one PoW to its own parent. The envelope's own
//     signature binds the child's key to the nonce.
//  3. The #birth handler matches the nonce, binds it to the first key
//     that presents it (a same-key re-knock is idempotent, any other
//     key is refused), mints the starter kit and replies with it. The
//     child's piggybacked reach-me-at is its first location record.
//  4. Born installs the kit — every chain under each (target, facet)
//     its last link names, every location into the cache — and mints
//     the child's OWN consent to its parent over the app facets it
//     serves: P→C authority never crosses the wire (invariant 3).
//  5. The promise resolves {id, location, lease} once the birth has
//     arrived AND the provisioner has answered.
//
// # What the kit mandates
//
// Exactly one chain: [P→C invoke {target: [P], facet: [#renew]}], the
// child's renewal edge to its parent. It is P's own consent (P is the
// receiver of #renew), so the spawner installs it in P's Consents as it
// mints it. Everything else — app facets, a network's publish-cap,
// siblings — is the parent's choice (Spec.Outfit) or arrives later on
// the beat.
//
// # The birth window
//
// One number: the birth consent's lifetime. When it passes the child
// can no longer knock (the consent has expired) and Sweep drops the
// pending entry and the consent, failing an unresolved promise with
// ErrBirthWindow. Sweep runs inside Spawn and the #birth handler and
// is exported for the owner's beat; nothing here starts a goroutine.
// A parent that moves or restarts inside the window loses that birth
// — GC by lapse (ADR-0008 consequences; lighthouse lookup-cap in the
// intro is the additive upgrade, thread lm2a).
package spawn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
)

// Facets. #birth is the parent's; #spawn/#extend/#kill are the
// provisioner's (ADR-0009) — named here because the spawner is their
// client and the wire types below are shared with any provisioner.
const (
	FacetBirth  = "#birth"
	FacetSpawn  = "#spawn"
	FacetExtend = "#extend"
	FacetKill   = "#kill"
)

// DefaultWindow is the birth window in seconds when Spawner.Window is
// 0: long enough to pull an image and boot a container, short enough
// that a leaked intro buys little. A number, not a rule.
const DefaultWindow = 15 * 60

var (
	// ErrNoLocation marks a Spawn on a parent with no current
	// reach-me-at: the intro must tell the child where P is.
	ErrNoLocation = errors.New("spawn: parent has no location record")
	// ErrNoProvisioner marks a Spawn with no provisioner configured or
	// named.
	ErrNoProvisioner = errors.New("spawn: no provisioner")
	// ErrBirthWindow marks a promise whose birth window passed with no
	// (complete) birth.
	ErrBirthWindow = errors.New("spawn: birth window passed")
	// ErrBadIntro marks an intro that fails its shape rule (CheckIntro).
	ErrBadIntro = errors.New("spawn: invalid intro")
	// ErrBadKit marks a starter kit that fails its rule (CheckKit).
	ErrBadKit = errors.New("spawn: invalid starter kit")
	// ErrNoRenewChain marks a kit without the mandated #renew chain.
	ErrNoRenewChain = errors.New("spawn: kit lacks the #renew chain to the parent")
)

// Refusals the #birth handler answers with (actor.StatusError text).
const (
	RefuseUnknownNonce = "unknown nonce"
	RefuseNonceBound   = "nonce is bound to another key"
)

// ---- wire ----------------------------------------------------------------

// Intro is the bootstrap artifact a parent injects into a spawn
// (glossary "Intro"): P's id, P's current signed reach-me-at (the
// sanctioned raw-address exception, invariant 11 — signed and expiring
// rather than raw), the per-spawn birth consent, and the nonce.
type Intro struct {
	Parent   cert.ActorID
	Location cert.Cert
	Consent  cert.Cert
	Nonce    string
}

type wireIntro struct {
	Parent   cert.ActorID    `json:"parent"`
	Location json.RawMessage `json:"location"`
	Consent  json.RawMessage `json:"consent"`
	Nonce    string          `json:"nonce"`
}

// EncodeIntro renders an intro as the #spawn params blob.
func EncodeIntro(in Intro) ([]byte, error) {
	loc, err := cert.Encode(in.Location)
	if err != nil {
		return nil, err
	}
	con, err := cert.Encode(in.Consent)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wireIntro{Parent: in.Parent, Location: loc, Consent: con, Nonce: in.Nonce})
}

// DecodeIntro parses an intro blob. Shape is not checked here; see
// CheckIntro.
func DecodeIntro(data []byte) (Intro, error) {
	var w wireIntro
	if err := json.Unmarshal(data, &w); err != nil {
		return Intro{}, fmt.Errorf("%w: %w", ErrBadIntro, err)
	}
	loc, err := cert.DecodeCert(w.Location)
	if err != nil {
		return Intro{}, fmt.Errorf("%w: location: %w", ErrBadIntro, err)
	}
	con, err := cert.DecodeCert(w.Consent)
	if err != nil {
		return Intro{}, fmt.Errorf("%w: consent: %w", ErrBadIntro, err)
	}
	return Intro{Parent: w.Parent, Location: loc, Consent: con, Nonce: w.Nonce}, nil
}

// CheckIntro is the child's rule for an intro at now: a parent id, a
// nonce, the parent's own live reach-me-at (actor.CheckLocation), and a
// birth consent of the required shape — issued by the parent, invoke,
// aud "*", postage named (aud "*" without it binds nobody), target
// [parent], facet [#birth], unexpired, signature verifies.
func CheckIntro(in Intro, now int64) error {
	if err := in.Parent.Validate(); err != nil {
		return fmt.Errorf("%w: parent: %w", ErrBadIntro, err)
	}
	if in.Nonce == "" {
		return fmt.Errorf("%w: no nonce", ErrBadIntro)
	}
	if err := actor.CheckLocation(in.Parent, in.Location, now); err != nil {
		return fmt.Errorf("%w: %w", ErrBadIntro, err)
	}
	return checkBirthConsent(in.Parent, in.Consent, now)
}

func checkBirthConsent(p cert.ActorID, c cert.Cert, now int64) error {
	switch {
	case c.Iss != p:
		return fmt.Errorf("%w: consent iss %s is not %s", ErrBadIntro, c.Iss, p)
	case c.Can != cert.VerbInvoke:
		return fmt.Errorf("%w: consent can=%q", ErrBadIntro, c.Can)
	case c.Aud != cert.AudAny:
		return fmt.Errorf("%w: consent aud %q is not %q", ErrBadIntro, c.Aud, cert.AudAny)
	case c.Cav.Postage == "":
		return fmt.Errorf("%w: consent without postage", ErrBadIntro)
	case len(c.Cav.Target) != 1 || c.Cav.Target[0] != p:
		return fmt.Errorf("%w: consent target %v is not [%s]", ErrBadIntro, c.Cav.Target, p)
	case len(c.Cav.Facet) != 1 || c.Cav.Facet[0] != FacetBirth:
		return fmt.Errorf("%w: consent facet %v is not [%s]", ErrBadIntro, c.Cav.Facet, FacetBirth)
	case c.Exp <= now:
		return fmt.Errorf("%w: consent expired", ErrBadIntro)
	}
	if err := cert.Verify(c); err != nil {
		return fmt.Errorf("%w: consent: %w", ErrBadIntro, err)
	}
	return nil
}

// SpawnRequest is the #spawn payload (ADR-0009): the image by digest,
// the params the provisioner injects into the container unread (the
// intro), and the lease's first deadline (the birth window's end).
type SpawnRequest struct {
	Image  string          `json:"image"`
	Params json.RawMessage `json:"params"`
	Until  int64           `json:"until"`
}

// SpawnReply is the #spawn reply body: the provisioner's lease id.
type SpawnReply struct {
	Lease string `json:"lease"`
}

// ExtendRequest is the #extend payload; KillRequest the #kill payload.
type ExtendRequest struct {
	Lease string `json:"lease"`
	Until int64  `json:"until"`
}

// KillRequest is the #kill payload.
type KillRequest struct {
	Lease string `json:"lease"`
}

// BirthRequest is the #birth payload: the nonce alone. The child's key
// is the envelope's signer; its location rides the piggyback.
type BirthRequest struct {
	Nonce string `json:"nonce"`
}

// Kit is the starter kit (glossary): root-first chains whose last aud
// is the child, and location records for the actors those chains name.
type Kit struct {
	Grants    [][]cert.Cert
	Locations []cert.Cert
}

type wireKit struct {
	Grants    [][]json.RawMessage `json:"grants"`
	Locations []json.RawMessage   `json:"locations"`
}

// EncodeKit renders a kit as the #birth reply body.
func EncodeKit(k Kit) ([]byte, error) {
	w := wireKit{Grants: make([][]json.RawMessage, len(k.Grants)), Locations: make([]json.RawMessage, len(k.Locations))}
	for i, chain := range k.Grants {
		w.Grants[i] = make([]json.RawMessage, len(chain))
		for j, c := range chain {
			raw, err := cert.Encode(c)
			if err != nil {
				return nil, err
			}
			w.Grants[i][j] = raw
		}
	}
	for i, l := range k.Locations {
		raw, err := cert.Encode(l)
		if err != nil {
			return nil, err
		}
		w.Locations[i] = raw
	}
	return json.Marshal(w)
}

// DecodeKit parses a #birth reply body. Shape is not checked here; see
// CheckKit.
func DecodeKit(data []byte) (Kit, error) {
	var w wireKit
	if err := json.Unmarshal(data, &w); err != nil {
		return Kit{}, fmt.Errorf("%w: %w", ErrBadKit, err)
	}
	k := Kit{Grants: make([][]cert.Cert, len(w.Grants)), Locations: make([]cert.Cert, len(w.Locations))}
	for i, chain := range w.Grants {
		k.Grants[i] = make([]cert.Cert, len(chain))
		for j, raw := range chain {
			c, err := cert.DecodeCert(raw)
			if err != nil {
				return Kit{}, fmt.Errorf("%w: grant %d/%d: %w", ErrBadKit, i, j, err)
			}
			k.Grants[i][j] = c
		}
	}
	for i, raw := range w.Locations {
		l, err := cert.DecodeCert(raw)
		if err != nil {
			return Kit{}, fmt.Errorf("%w: location %d: %w", ErrBadKit, i, err)
		}
		k.Locations[i] = l
	}
	return k, nil
}

// CheckKit is the child's rule for a kit from parent p at now: every
// chain is non-empty, every link verifies and is unexpired, the last
// link's aud is the child, and the mandated chain is present — one
// whose last link is issued by p with target ∋ p and facet ∋ #renew
// (ErrNoRenewChain). Locations are judged by actor.CheckLocation on
// install, each against its own issuer.
func CheckKit(k Kit, p, child cert.ActorID, now int64) error {
	renew := false
	for i, chain := range k.Grants {
		if len(chain) == 0 {
			return fmt.Errorf("%w: empty chain %d", ErrBadKit, i)
		}
		for j, c := range chain {
			if err := cert.Verify(c); err != nil {
				return fmt.Errorf("%w: grant %d/%d: %w", ErrBadKit, i, j, err)
			}
			if c.Exp <= now {
				return fmt.Errorf("%w: grant %d/%d expired", ErrBadKit, i, j)
			}
		}
		last := chain[len(chain)-1]
		if last.Aud != string(child) {
			return fmt.Errorf("%w: chain %d ends at %s, not the child", ErrBadKit, i, last.Aud)
		}
		if last.Iss == p && slices.Contains(last.Cav.Target, p) && slices.Contains(last.Cav.Facet, actor.FacetRenew) {
			renew = true
		}
	}
	if !renew {
		return ErrNoRenewChain
	}
	return nil
}

// ---- parent side ------------------------------------------------------------

// Lease is the handle a provisioner returned: how you stop an actor
// (vs. its id, how you talk to it).
type Lease struct {
	Provisioner cert.ActorID
	ID          string
}

// Birth is what a Spawn promise resolves to: the child's identity, its
// first location record (nil if the birth envelope carried none), and
// the lease.
type Birth struct {
	ID       cert.ActorID
	Location *cert.Cert
	Lease    Lease
}

// Promise is the outcome of one Spawn: resolved by the #birth handler
// once the provisioner has answered too, or failed by Sweep when the
// birth window passes.
type Promise struct {
	done  chan struct{}
	birth Birth
	err   error
}

// Done is closed when the promise has resolved or failed.
func (p *Promise) Done() <-chan struct{} { return p.done }

// Wait blocks until the promise resolves (Birth), fails (ErrBirthWindow
// or the provisioner's refusal), or ctx ends.
func (p *Promise) Wait(ctx context.Context) (Birth, error) {
	select {
	case <-p.done:
		return p.birth, p.err
	case <-ctx.Done():
		return Birth{}, ctx.Err()
	}
}

// Spec is one spawn: the image by content digest, an optional
// provisioner (else the spawner's default), an optional birth window
// (else the spawner's), and an optional Outfit that adds chains and
// locations to the kit once the child's id is known — the parent's
// choice beyond the mandated #renew chain. Outfit runs inside the
// parent's mailbox loop; it must not Send.
type Spec struct {
	Image       string
	Provisioner cert.ActorID
	Window      int64
	Outfit      func(child cert.ActorID, now int64) (Kit, error)
}

// Spawner is the parent-side library on one actor: it owns the
// pending-spawn table, the #birth handler, the starter kit and (M4.2)
// the #renew decorator. Configure the exported fields before Listen.
type Spawner struct {
	// Provisioner is the default provisioner (an actor this spawner
	// holds a chain to (Provisioner, #spawn) for — or whose consent
	// names this actor, presented empty).
	Provisioner cert.ActorID
	// Postage is the requirement every birth consent names; "" ⇒
	// postage.DefaultRequire. The child pays it once, to its parent.
	Postage string
	// Window is the default birth window in seconds; 0 ⇒ DefaultWindow.
	Window int64
	// KitTTL is the lifetime of the mandated #renew chain in seconds;
	// 0 ⇒ the birth window. It is the child's first renewal beat and
	// (M4.2) the lease's deadline after birth.
	KitTTL int64

	a       *actor.Actor
	mu      sync.Mutex
	pending map[string]*record // nonce → spawn
}

// record is one pending spawn. Guarded by Spawner.mu.
type record struct {
	consent cert.Cert
	spec    Spec
	promise *Promise
	// bound is the first key that presented the nonce ("" until then).
	bound cert.ActorID
	kit   []byte // the encoded kit, minted at first knock, re-sent verbatim
	loc   *cert.Cert
	born  bool
	lease Lease
	// leased is set when #spawn has answered; a #spawn refusal fails
	// the promise directly and drops the record.
	leased bool
}

// New registers the #birth handler on a and returns the spawner.
// Call before a.Listen.
func New(a *actor.Actor) *Spawner {
	s := &Spawner{a: a, pending: make(map[string]*record)}
	a.AcceptTable[FacetBirth] = s.birth
	return s
}

func (s *Spawner) postage() string {
	if s.Postage != "" {
		return s.Postage
	}
	return postage.DefaultRequire
}

func (s *Spawner) window(spec Spec) int64 {
	switch {
	case spec.Window > 0:
		return spec.Window
	case s.Window > 0:
		return s.Window
	}
	return DefaultWindow
}

func (s *Spawner) kitTTL(spec Spec) int64 {
	if s.KitTTL > 0 {
		return s.KitTTL
	}
	return s.window(spec)
}

// Pending reports the number of spawns in the table (unborn, or born
// and still inside their window).
func (s *Spawner) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// BirthConsent mints a per-spawn birth consent for window seconds:
// {iss: me, aud: "*", can: invoke, cav: {target: [me], facet:
// [#birth], postage}, exp: now + window}. Returned, not installed;
// Spawn installs it.
func (s *Spawner) BirthConsent(window int64) (cert.Cert, error) {
	if window <= 0 {
		return cert.Cert{}, actor.ErrBadTTL
	}
	now := s.a.Now()
	return cert.Sign(cert.Cert{
		Aud: cert.AudAny,
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{
			Target:  []cert.ActorID{s.a.ID()},
			Facet:   []string{FacetBirth},
			Postage: s.postage(),
		},
		Iat: now,
		Exp: now + window,
	}, s.a.Signer)
}

// editConsents installs add beside the actor's held roots (a cert
// already held is not added twice) and removes the consents whose sig
// is in drop unless a pending spawn still holds that very consent.
// Both rules exist for twins: two spawns minted in the same second
// are byte-identical (same shape, same iat/exp, deterministic
// signature; there is no per-spawn caveat by design), so they share
// one root and one spawn's exit must not unroot the other. One
// actor.EditConsents: atomic against the mailbox loop, Send and an
// owner's Hold. Callers hold s.mu, so s.pending is stable inside.
func (s *Spawner) editConsents(add []cert.Cert, drop [][]byte) {
	s.a.EditConsents(func(consents []cert.Cert) []cert.Cert {
		if len(drop) > 0 {
			consents = slices.DeleteFunc(consents, func(c cert.Cert) bool {
				if !slices.ContainsFunc(drop, func(sig []byte) bool { return bytes.Equal(sig, c.Sig) }) {
					return false
				}
				for _, rec := range s.pending {
					if bytes.Equal(rec.consent.Sig, c.Sig) {
						return false
					}
				}
				return true
			})
		}
		for _, c := range add {
			if !slices.ContainsFunc(consents, func(h cert.Cert) bool { return bytes.Equal(h.Sig, c.Sig) }) {
				consents = append(consents, c)
			}
		}
		return consents
	})
}

// Spawn rents a child: mints the birth consent and nonce, records the
// spawn, sends #spawn to the provisioner, and returns the promise. A
// provisioner refusal is returned (and the spawn is dropped); the
// promise then resolves when the child knocks, or fails at the window.
func (s *Spawner) Spawn(ctx context.Context, spec Spec) (*Promise, error) {
	prov := spec.Provisioner
	if prov == "" {
		prov = s.Provisioner
	}
	if prov == "" {
		return nil, ErrNoProvisioner
	}
	loc := s.a.CurrentLocation()
	if loc == nil {
		return nil, ErrNoLocation
	}
	consent, err := s.BirthConsent(s.window(spec))
	if err != nil {
		return nil, err
	}
	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	params, err := EncodeIntro(Intro{Parent: s.a.ID(), Location: *loc, Consent: consent, Nonce: nonce})
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(SpawnRequest{Image: spec.Image, Params: params, Until: consent.Exp})
	if err != nil {
		return nil, err
	}

	// Record and install BEFORE #spawn: on an in-process provisioner
	// the knock can arrive before the reply does.
	rec := &record{consent: consent, spec: spec, promise: &Promise{done: make(chan struct{})}}
	s.mu.Lock()
	s.sweepLocked(s.a.Now())
	s.pending[nonce] = rec
	s.editConsents([]cert.Cert{consent}, nil)
	s.mu.Unlock()

	rep, err := s.a.Send(ctx, prov, FacetSpawn, payload)
	if err != nil {
		s.drop(nonce, rec, err)
		return nil, err
	}
	var sr SpawnReply
	if err := json.Unmarshal(rep.Payload, &sr); err != nil {
		err = fmt.Errorf("spawn: #spawn reply: %w", err)
		s.drop(nonce, rec, err)
		return nil, err
	}
	s.mu.Lock()
	rec.lease = Lease{Provisioner: prov, ID: sr.Lease}
	rec.leased = true
	s.resolveLocked(rec)
	s.mu.Unlock()
	return rec.promise, nil
}

// drop removes a spawn whose #spawn failed: the record, its consent,
// and the promise (failed with err).
func (s *Spawner) drop(nonce string, rec *record, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[nonce] == rec {
		delete(s.pending, nonce)
		s.editConsents(nil, [][]byte{rec.consent.Sig})
	}
	s.failLocked(rec, err)
}

// resolveLocked completes the promise once both halves are in. Caller
// holds s.mu.
func (s *Spawner) resolveLocked(rec *record) {
	if !rec.born || !rec.leased {
		return
	}
	select {
	case <-rec.promise.done:
	default:
		rec.promise.birth = Birth{ID: rec.bound, Location: rec.loc, Lease: rec.lease}
		close(rec.promise.done)
	}
}

func (s *Spawner) failLocked(rec *record, err error) {
	select {
	case <-rec.promise.done:
	default:
		rec.promise.err = err
		close(rec.promise.done)
	}
}

// Sweep drops every spawn whose birth window has passed under the
// actor's effective clock — the record and its consent — failing an
// unresolved promise with ErrBirthWindow. Spawn and the #birth handler
// call it; the owner may call it on its beat.
func (s *Spawner) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.a.Now())
}

func (s *Spawner) sweepLocked(now int64) {
	var drop [][]byte
	for nonce, rec := range s.pending {
		if rec.consent.Exp > now {
			continue
		}
		delete(s.pending, nonce)
		drop = append(drop, rec.consent.Sig)
		s.failLocked(rec, ErrBirthWindow)
	}
	if len(drop) > 0 {
		s.editConsents(nil, drop)
	}
}

// birth serves #birth. The chain already bound inv.From to a live
// birth consent — ANY of them: they are the same shape and grant the
// same thing, the right to knock; the payload's nonce alone selects
// the spawn (correlation, ADR-0008). Rules, in order: unknown nonce
// refused; first key binds, same key is idempotent (the kit bytes
// minted at the first knock are re-sent), any other key refused. The
// kit's #renew chain is this actor's own consent and is installed as
// it is minted.
func (s *Spawner) birth(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	var req BirthRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("birth: payload: %w", err)
	}
	now := s.a.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	rec, ok := s.pending[req.Nonce]
	if !ok {
		return nil, errors.New(RefuseUnknownNonce)
	}
	switch rec.bound {
	case "":
		kit, err := s.mintKit(rec, inv.From, now)
		if err != nil {
			return nil, err
		}
		rec.bound = inv.From
		rec.kit = kit
	case inv.From:
		// idempotent re-knock
	default:
		return nil, errors.New(RefuseNonceBound)
	}
	if loc := s.a.GetLocation(inv.From); loc != nil {
		rec.loc = loc
	}
	rec.born = true
	s.resolveLocked(rec)
	return rec.kit, nil
}

// mintKit builds the starter kit for child: the mandated #renew chain
// (installed in this actor's Consents) plus whatever the spec's Outfit
// adds. Caller holds s.mu.
func (s *Spawner) mintKit(rec *record, child cert.ActorID, now int64) ([]byte, error) {
	renew, err := cert.Sign(cert.Cert{
		Aud: string(child),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{s.a.ID()}, Facet: []string{actor.FacetRenew}},
		Iat: now,
		Exp: now + s.kitTTL(rec.spec),
	}, s.a.Signer)
	if err != nil {
		return nil, fmt.Errorf("birth: renew chain: %w", err)
	}
	kit := Kit{Grants: [][]cert.Cert{{renew}}}
	if rec.spec.Outfit != nil {
		extra, err := rec.spec.Outfit(child, now)
		if err != nil {
			return nil, fmt.Errorf("birth: outfit: %w", err)
		}
		kit.Grants = append(kit.Grants, extra.Grants...)
		kit.Locations = append(kit.Locations, extra.Locations...)
	}
	raw, err := EncodeKit(kit)
	if err != nil {
		return nil, fmt.Errorf("birth: kit: %w", err)
	}
	s.editConsents([]cert.Cert{renew}, nil)
	return raw, nil
}

// ---- child side --------------------------------------------------------------

// Born is the newborn's half of the handshake on actor a (its key
// already minted on its own compute, its Transport up and, so the
// parent can reach it, its location published): check the intro,
// learn the parent's location, knock on P#birth under the birth
// consent (Send stamps it), check and install the kit — every chain
// under each (target, facet) its last link names (a "*" target is
// skipped: it names no one to send to), every location into the cache
// — and, when facets is non-empty, mint and hold the child's own
// consent {iss: me, aud: P, can: invoke, cav: {target: [me], facet:
// facets, delegable: true}} for consentTTL seconds so the parent can
// call in with an empty chain. The kit is returned for the caller's
// own bookkeeping (the renew loop is M4.5's).
func Born(ctx context.Context, a *actor.Actor, in Intro, facets []string, consentTTL int64) (Kit, error) {
	now := a.Now()
	if err := CheckIntro(in, now); err != nil {
		return Kit{}, err
	}
	if err := a.UpdateLocation(in.Parent, &in.Location); err != nil {
		return Kit{}, fmt.Errorf("%w: %w", ErrBadIntro, err)
	}
	a.Grant(in.Parent, FacetBirth, in.Consent)
	payload, err := json.Marshal(BirthRequest{Nonce: in.Nonce})
	if err != nil {
		return Kit{}, err
	}
	rep, err := a.Send(ctx, in.Parent, FacetBirth, payload)
	if err != nil {
		return Kit{}, err
	}
	kit, err := DecodeKit(rep.Payload)
	if err != nil {
		return Kit{}, err
	}
	if err := CheckKit(kit, in.Parent, a.ID(), a.Now()); err != nil {
		return Kit{}, err
	}
	Install(a, kit)
	if len(facets) > 0 {
		if consentTTL <= 0 {
			return Kit{}, actor.ErrBadTTL
		}
		now = a.Now()
		own, err := cert.Sign(cert.Cert{
			Aud: string(in.Parent),
			Can: cert.VerbInvoke,
			Cav: cert.Caveats{Target: []cert.ActorID{a.ID()}, Facet: append([]string(nil), facets...), Delegable: true},
			Iat: now,
			Exp: now + consentTTL,
		}, a.Signer)
		if err != nil {
			return Kit{}, err
		}
		a.EditConsents(func(consents []cert.Cert) []cert.Cert { return append(consents, own) })
	}
	return kit, nil
}

// Install puts a kit's chains and locations into a: each chain under
// every (target, facet) its last link names, each location record
// under its issuer (records failing actor.CheckLocation are skipped,
// not fatal — one stale record must not void the kit).
func Install(a *actor.Actor, kit Kit) {
	for _, chain := range kit.Grants {
		last := chain[len(chain)-1]
		for _, target := range last.Cav.Target {
			if target == cert.TargetAny {
				continue
			}
			for _, facet := range last.Cav.Facet {
				a.Grant(target, facet, chain...)
			}
		}
	}
	for _, loc := range kit.Locations {
		_ = a.UpdateLocation(loc.Iss, &loc)
	}
}
