// Package actor is the sovereign-actor protocol's runtime: the piece
// that binds envelope messaging (protocol/envelope) and the chain
// verifier (cert.VerifyChain) into a running actor. Spec: protocol/docs
// ADR-0001 § Decision Outcome (Actor facet, Invocation, Reply, Renewal
// beat, serial mailbox) and the glossary in
// protocol/docs/desired-state/domain-model.md.
//
// # Shape
//
// An Actor is a signer, an accept table (facet → Handler), the consents
// it issued (roots of every chain that may reach it, invariant 2), the
// grants and speak-as certs it holds for calling others, a volatile seq
// high-water mark table, a clock low-water Mark, and a location cache.
// It talks to the world through a Transport; the in-memory
// MemoryNetwork is the reference implementation.
//
// # Concurrency: the serial mailbox
//
// Inbound processing is a two-stage pipeline:
//
//  1. Transport goroutines (one per accepted stream) do only the pure,
//     cheap rejects — decode, envelope signature, to.target == me, and
//     an unstamped envelope to a facet whose every consent demands
//     postage (see refusesUnstamped) — and enqueue into a BOUNDED
//     mailbox. When the mailbox is full the
//     invocation is dropped (invariant 12: at-most-once, best-effort;
//     the sender retries). They never touch actor state.
//  2. ONE goroutine (the mailbox loop) dequeues and does everything
//     stateful, in order: effective now = Mark.Now(local), seq
//     high-water mark, location record, VerifyChain with the receiver's
//     own consents prepended, Mark.ObserveAll(verified) on accept AND
//     reject, the Handler, the Reply. Handlers therefore never run
//     concurrently with each other and see a consistent actor.
//
// The reply's bytes are handed back to the stream goroutine, so the
// loop never blocks on I/O. Send (outbound) may run on any goroutine —
// including inside a handler — and shares only the Mark, the location
// cache and the outbound seq counters with the loop. The Mark guards
// itself (clock.Mark owns its mutex); the location cache and the seq
// counters are guarded by one small actor mutex that is never held
// across I/O or a handler.
//
// Sends to ONE receiver are serialised (one invocation in flight per
// edge): the receiver's seq high-water mark is strictly monotone, so
// two concurrent invocations that overtook each other on the wire
// would have the straggler rejected as a replay. Sends to different
// receivers proceed in parallel.
//
// # Errors are replies
//
// Every decoded envelope gets exactly one Reply whose payload is a
// Status {code, msg, body}; transport-level closes are reserved for
// undecodable bytes and mailbox drops. The requester sees a remote
// rejection as *RemoteError from Send.
//
// # Verbs and postage (M3, ADR-0007)
//
// A facet binds one verb: the inbox roots a chain to facet f in a
// consent carrying Verbs[f] (invoke unless configured — #publish on a
// lighthouse binds publish). When the effective cert carries
// cav.postage the envelope must carry a stamp the actor's Postage
// scheme accepts; this is how the open frontdoor (aud "*") admits
// strangers at a cost. Send stamps automatically when the chain it
// holds for (to, facet) names a requirement. An unstamped envelope to
// a facet whose every consent demands postage is refused in the
// transport goroutine, before it costs a mailbox slot or a fold
// (refusesUnstamped) — the same verdict the loop would reach, earlier.
package actor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/clock"
	"github.com/marnyg/talos-config/protocol/envelope"
	"github.com/marnyg/talos-config/protocol/postage"
)

// DefaultMailbox is the mailbox depth when Actor.Mailbox is 0 (ADR-0001
// open problem 9 — a number, not a rule).
const DefaultMailbox = 64

// Invocation is one verified inbound envelope as a handler sees it.
type Invocation struct {
	Envelope *envelope.Envelope
	// From is the envelope's signer — the authority the chain bound.
	From cert.ActorID
	// Peer is the transport-authenticated dialler; may differ from From.
	Peer cert.ActorID
	// Eff is the effective cert the chain folded to (VerifyChain).
	Eff cert.Cert
}

// Handler serves one facet. The returned payload becomes the reply's
// body under Status ok; an error becomes Status "error" with its text.
type Handler func(ctx context.Context, inv *Invocation) (payload []byte, err error)

// GrantKey addresses the caller-held chain for one (target, facet).
type GrantKey struct {
	Target cert.ActorID
	Facet  string
}

// Actor is the runtime for one identity. Zero-value maps are allocated
// by New; the exported fields are configuration read at Listen/Send
// time and must not be mutated while the actor runs (use the methods).
// The one authority set that legitimately changes on a live actor —
// Consents and SpeakAs, when a hot key is (re-)delegated to (ADR-0018:
// unseal, re-unseal from the nag window) — is swapped through Hold,
// which is safe against a running Listen and Send.
type Actor struct {
	Signer cert.Signer
	// Transport carries envelopes; an Endpoint additionally lets
	// PublishLocation mint the reach-me-at record from its tags.
	Transport Transport
	// AcceptTable maps facet → handler. New pre-registers FacetRenew.
	AcceptTable map[string]Handler
	// Verbs maps facet → the verb its chains must be rooted in; a facet
	// absent here binds invoke. Set by the facet's owner (a lighthouse
	// binds #publish to publish); the verifier never infers it from the
	// caller's links (talos-config-xwu).
	Verbs map[string]cert.Verb
	// Postage is the stamp scheme for both sides: Send solves against
	// the requirement in the held chain, the inbox checks against the
	// effective cert's cav.postage. nil ⇒ postage.Default (PoW).
	Postage postage.Scheme
	// Consents are the invoke certs this actor signed (iss == me): the
	// roots VerifyChain prepends to every caller chain.
	Consents []cert.Cert
	// Grants are the chain links this actor holds for calling others,
	// root-first, EXCLUDING the target's own consent (the receiver
	// prepends that). Empty is legal: the consented principal itself.
	// A held chain may also carry the speak-as certs that resolve its
	// links' hot-key issuers (a member cert signed by a hub key rides
	// with the wallet's speak-as to that key, ADR-0018); the receiver's
	// envelope.SplitProof sorts them into the proof's speak-as set.
	Grants map[GrantKey][]cert.Cert
	// SpeakAs holds both roles of speak-as cert: those naming this
	// actor's signer as aud (attached to outbound proofs so receivers
	// can resolve a hot key, AND handed to the inbox's chain verifier as
	// the principals this actor answers for — protocol ADR-0003 rule 4)
	// and those this actor issued to its own hot keys (used by #renew to
	// recognise its own issuance).
	SpeakAs []cert.Cert
	// Mailbox is the bounded inbound queue depth; 0 ⇒ DefaultMailbox.
	Mailbox int
	// Clock is the local unauthenticated clock (Unix seconds); nil ⇒
	// time.Now. The effective clock is Mark.Now(Clock()).
	Clock func() int64
	// RenewTTL is the lifetime of a re-issued cert in seconds; 0 ⇒ the
	// original cert's own lifetime (exp − iat).
	RenewTTL int64
	// SeqBase, when set, seeds the outbound seq to a receiver this actor
	// has not sent to yet IN THIS PROCESS: the first seq is
	// max(SeqBase(), 1), later ones count up from it. seq must be
	// monotonic per (sender, receiver) across the SENDER's restarts too —
	// the receiver's high-water mark outlives them — so a sender whose
	// counterparties run longer than it does seeds from its clock:
	// stateless, and a rolled-back clock only denies, like every other
	// clock fault (ADR-0019). Use time.Now().UnixMicro() (or coarser),
	// NOT UnixNano: seq must stay ≤ envelope.MaxSeq (2^53−1) to be
	// exact on the wire, and Send fails with envelope.ErrSeqRange past
	// it (found live 2026-09-19: a nanosecond base made renew + bundle
	// in one beat collapse onto one seq). nil ⇒ start at 1.
	SeqBase func() int64
	// Serves lists the stream facets this actor accepts, advertised in
	// its reach-me-at as cav.facet beside the endpoints: "where to reach
	// me" and "on what" are one claim, so a presentation can read a
	// name's kind from the plane instead of assuming it. An
	// advertisement only — it authorizes nothing; the receiver's own
	// consent and accept table decide at admission. nil ⇒ absent (∅).
	Serves []string

	hwm  *envelope.HWM
	mail chan *inbound

	// dropped counts mailbox-full drops (diagnostics).
	dropped atomic.Int64

	// mark is the clock low-water mark. It guards itself (clock.Mark
	// owns its mutex), so it is deliberately NOT in mu's guarded set.
	mark clock.Mark

	// mu guards the fields below: shared between the mailbox loop and
	// Send. Never held across I/O or a handler.
	mu     sync.Mutex
	loc    *cert.Cert                   // own current reach-me-at
	locs   map[cert.ActorID]cert.Cert   // id → latest valid reach-me-at
	seqOut map[cert.ActorID]int64       // per-receiver outbound counter
	edges  map[cert.ActorID]*sync.Mutex // per-receiver in-flight lock
}

// New returns an actor for signer over transport with the #renew facet
// registered. Configure the exported fields before Listen.
func New(signer cert.Signer, t Transport) *Actor {
	a := &Actor{
		Signer:      signer,
		Transport:   t,
		AcceptTable: make(map[string]Handler),
		Verbs:       make(map[string]cert.Verb),
		Grants:      make(map[GrantKey][]cert.Cert),
		hwm:         envelope.NewHWM(),
		locs:        make(map[cert.ActorID]cert.Cert),
		seqOut:      make(map[cert.ActorID]int64),
		edges:       make(map[cert.ActorID]*sync.Mutex),
	}
	a.AcceptTable[FacetRenew] = a.renewHandler
	return a
}

// ID is the actor's identity (its signer's id).
func (a *Actor) ID() cert.ActorID { return a.Signer.ActorID() }

// Grant records the caller-held chain for (target, facet). A chain may
// begin with the target's own consent (a frontdoor cert as a lookup
// returned it): Send drops that link — the receiver prepends its own —
// but reads its postage requirement.
func (a *Actor) Grant(target cert.ActorID, facet string, chain ...cert.Cert) {
	a.Grants[GrantKey{Target: target, Facet: facet}] = append([]cert.Cert(nil), chain...)
}

// verbFor is the verb facet's chains must carry: Verbs[facet] or invoke.
func (a *Actor) verbFor(facet string) cert.Verb {
	if v, ok := a.Verbs[facet]; ok && v != "" {
		return v
	}
	return cert.VerbInvoke
}

func (a *Actor) postage() postage.Scheme {
	if a.Postage != nil {
		return a.Postage
	}
	return postage.Default
}

// Hold replaces the actor's authority set — the consents it signed and
// the speak-as certs it holds in both roles — atomically with respect
// to the mailbox loop and Send. This is the hot-key lifecycle: a
// sealed actor Listens with an empty set, an unseal installs one, a
// re-unseal replaces it. Copies are stored; the caller's slices are
// not retained.
func (a *Actor) Hold(consents, speakAs []cert.Cert) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Consents = append([]cert.Cert(nil), consents...)
	a.SpeakAs = append([]cert.Cert(nil), speakAs...)
}

// EditConsents applies edit to the held Consents atomically with respect
// to the mailbox loop, Send, Hold and other EditConsents: the one
// read-modify-write for a component that adds and removes roots on a
// live actor while the owner may also Hold (ADR-0005 family; asked for
// by protocol/spawn, whose per-spawn birth consents and kit chains come
// and go under a running Listen — a separate Authority()+Hold() pair
// would let an interleaving Hold drop them, or let them drop an
// unseal). edit receives a copy and returns the new set; SpeakAs is
// untouched. Authority installation only — what a chain proves is
// unchanged.
func (a *Actor) EditConsents(edit func(consents []cert.Cert) []cert.Cert) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Consents = edit(append([]cert.Cert(nil), a.Consents...))
}

// Authority returns a snapshot of the held Consents and SpeakAs (what
// Hold last installed, or the pre-Listen fields): the receiver-side
// inputs a cert.Receiver needs to run Authorize as this actor would.
// Read-only copies; safe while the actor runs.
func (a *Actor) Authority() (consents, speakAs []cert.Cert) {
	c, s := a.authority()
	return append([]cert.Cert(nil), c...), append([]cert.Cert(nil), s...)
}

// authority snapshots Consents and SpeakAs under mu for one
// verification or one Send; the snapshots are read-only.
func (a *Actor) authority() (consents, speakAs []cert.Cert) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Consents, a.SpeakAs
}

// HWM exposes the seq high-water mark table (diagnostics / tests).
func (a *Actor) HWM() *envelope.HWM { return a.hwm }

// Dropped reports how many invocations were dropped on a full mailbox.
func (a *Actor) Dropped() int64 { return a.dropped.Load() }

// LowWater reports the clock low-water mark.
func (a *Actor) LowWater() int64 { return a.mark.LowWater() }

// RestoreLowWater seeds the mark from a persisted value (a safe-to-lose
// cache, ADR-0019): the mark only ever rises, so a stale value is
// harmless and a lost one degrades to the local clock.
func (a *Actor) RestoreLowWater(lw int64) { a.mark.Restore(lw) }

// Observe feeds certs this actor verified OUTSIDE its inbox into the
// mark — a stream facet's authorize (the connection is the invocation,
// checked by the consumer with cert.Authorize) passes Result.Verified
// here so the low-water mark advances on every rooted path, exactly as
// the inbox does for envelopes.
func (a *Actor) Observe(verified []cert.Cert) { a.mark.ObserveAll(verified) }

func (a *Actor) local() int64 {
	if a.Clock != nil {
		return a.Clock()
	}
	return time.Now().Unix()
}

// now returns the effective clock max(local, lw). It needs no actor
// lock: the Mark guards itself.
func (a *Actor) now() int64 { return a.mark.Now(a.local()) }

// Now returns the effective clock the actor judges with.
func (a *Actor) Now() int64 { return a.now() }

// ---- status wrapper on reply payloads ----------------------------------

// Status codes carried in every Reply payload.
const (
	StatusOK           = "ok"
	StatusBadSig       = "bad-sig"       // envelope sig does not verify
	StatusWrongTarget  = "wrong-target"  // to.target is not this actor
	StatusReplay       = "replay"        // seq at or below high-water mark
	StatusBadLoc       = "bad-loc"       // piggybacked loc invalid (fail closed)
	StatusUnauthorized = "unauthorized"  // proof chain rejected
	StatusPostage      = "postage"       // eff requires a stamp the envelope lacks or fails
	StatusUnknownFacet = "unknown-facet" // no handler for to.facet
	StatusError        = "error"         // handler returned an error
	StatusRejected     = "rejected"      // any other verifier rejection
)

// Status is the reply payload envelope: the protocol's one way to say
// "no" (errors are replies, never transport closes).
type Status struct {
	Code string `json:"code"`
	Msg  string `json:"msg,omitempty"`
	Body []byte `json:"body,omitempty"`
}

// EncodeStatus renders a Status as reply payload bytes.
func EncodeStatus(s Status) []byte {
	b, _ := json.Marshal(s) // fixed shape; cannot fail
	return b
}

// DecodeStatus parses a reply payload.
func DecodeStatus(payload []byte) (Status, error) {
	var s Status
	if err := json.Unmarshal(payload, &s); err != nil {
		return Status{}, fmt.Errorf("actor: reply payload is not a status: %w", err)
	}
	if s.Code == "" {
		return Status{}, errors.New("actor: reply status has no code")
	}
	return s, nil
}

// RemoteError is a non-ok Status the callee replied with.
type RemoteError struct {
	Code string
	Msg  string
}

func (e *RemoteError) Error() string {
	if e.Msg == "" {
		return "actor: remote replied " + e.Code
	}
	return "actor: remote replied " + e.Code + ": " + e.Msg
}

// Reply is what Send returns: the decoded status, the handler body, the
// callee's validated location record (if any), and the wire reply.
type Reply struct {
	Status  Status
	Payload []byte
	Loc     *cert.Cert
	Wire    envelope.Reply
}

var (
	// ErrNoTransport marks Send/Listen on an actor without a Transport.
	ErrNoTransport = errors.New("actor: no transport")
	// ErrNoReply marks a stream the callee closed without replying
	// (dropped on a full mailbox, or undecodable request).
	ErrNoReply = errors.New("actor: no reply")
)

// ---- inbound: transport goroutines → mailbox → serial loop -------------

type inbound struct {
	env  envelope.Envelope
	peer cert.ActorID
	done chan []byte // signed reply wire bytes, exactly one
}

// Listen runs the actor: the serial mailbox loop plus the accept loop
// on a.Transport, until ctx is cancelled (returns ctx.Err()) or Accept
// fails permanently.
func (a *Actor) Listen(ctx context.Context) error {
	if a.Transport == nil {
		return ErrNoTransport
	}
	depth := a.Mailbox
	if depth <= 0 {
		depth = DefaultMailbox
	}
	a.mail = make(chan *inbound, depth)

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.loop(loopCtx)
	}()
	defer wg.Wait()

	for {
		s, peer, err := a.Transport.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go a.serve(ctx, s, peer)
	}
}

// serve is the transport goroutine for one accepted stream: read the
// request, do the pure cheap rejects, enqueue, relay the reply.
func (a *Actor) serve(ctx context.Context, s Stream, peer cert.ActorID) {
	defer s.Close()
	raw, err := s.RecvMsg(ctx)
	if err != nil {
		return
	}
	env, err := envelope.Decode(raw)
	if err != nil {
		return // undecodable: nothing to bind a reply to
	}
	if err := envelope.VerifySig(env); err != nil {
		a.replyStatus(ctx, s, env, Status{Code: StatusBadSig, Msg: err.Error()})
		return
	}
	if env.To.Target != a.ID() {
		a.replyStatus(ctx, s, env, Status{Code: StatusWrongTarget})
		return
	}
	if a.refusesUnstamped(env) {
		a.replyStatus(ctx, s, env, Status{Code: StatusPostage, Msg: postage.ErrMissing.Error()})
		return
	}
	in := &inbound{env: env, peer: peer, done: make(chan []byte, 1)}
	select {
	case a.mail <- in:
	default:
		a.dropped.Add(1) // invariant 12: drop on full, sender retries
		return
	}
	select {
	case wire := <-in.done:
		_ = s.SendMsg(ctx, wire)
	case <-ctx.Done():
	}
}

// replyStatus signs and sends a status reply from a transport goroutine
// (cheap rejects). Signing touches no loop-owned state.
func (a *Actor) replyStatus(ctx context.Context, s Stream, env envelope.Envelope, st Status) {
	wire, err := a.signReply(env, st)
	if err != nil {
		return
	}
	_ = s.SendMsg(ctx, wire)
}

func (a *Actor) signReply(env envelope.Envelope, st Status) ([]byte, error) {
	rep, err := envelope.NewReply(env, EncodeStatus(st), a.CurrentLocation())
	if err != nil {
		return nil, err
	}
	rep, err = envelope.SignReply(rep, a.Signer)
	if err != nil {
		return nil, err
	}
	return envelope.EncodeReply(rep)
}

// loop is the ONE goroutine that owns inbound processing.
func (a *Actor) loop(ctx context.Context) {
	for {
		select {
		case in := <-a.mail:
			st := a.process(ctx, in)
			if wire, err := a.signReply(in.env, st); err == nil {
				in.done <- wire
			} else {
				close(in.done)
			}
		case <-ctx.Done():
			return
		}
	}
}

// chainRule is cert.VerifyChain bound to the verb this actor expects
// for to.facet: invoke for an ordinary invocation (an inbound envelope
// IS an invocation of to.facet), or whatever Verbs binds — a
// lighthouse's #publish roots in a publish consent (talos-config-xwu).
func (a *Actor) chainRule(r cert.Receiver, chain, speakAs []cert.Cert, signer cert.ActorID, facet string, now int64) (cert.Cert, []cert.Cert, error) {
	return cert.VerifyChain(r, a.verbFor(facet), chain, speakAs, signer, facet, now)
}

// refusesUnstamped is the pre-mailbox postage refusal (talos-config-jjti):
// true when env carries no stamp and EVERY consent that could root a
// chain to env.To.Facet demands postage — then, postage being monotone
// in the fold, any effective cert would too and the loop's verdict
// could only be StatusPostage. Refusing here spares the fold (the
// receiver's own consent verify) and, more to the point, a mailbox
// slot: an unstamped flood at the frontdoor must not starve #renew.
//
// This is refusal-only and can never widen authority: the candidate
// set is filtered on exactly the conditions the fold ALSO requires
// of a root (iss == me, can == the facet's verb, facet ∈ cav.facet)
// and on nothing else — an unverified, expired or wildcard-target
// consent is still a candidate, and a candidate without postage
// disables the shortcut. No candidates ⇒ no shortcut (the fold
// reports unauthorized). Reads Consents under a.mu like Send does.
func (a *Actor) refusesUnstamped(env envelope.Envelope) bool {
	if env.Postage != "" {
		return false
	}
	verb := a.verbFor(env.To.Facet)
	me := a.ID()
	consents, _ := a.authority()
	found := false
	for _, c := range consents {
		if c.Iss != me || c.Can != verb || !slices.Contains(c.Cav.Facet, env.To.Facet) {
			continue
		}
		if c.Cav.Postage == "" {
			return false
		}
		found = true
	}
	return found
}

// checkPostage enforces the effective cert's cav.postage on the
// envelope: absent requirement ⇒ nothing to check (a stamp on an
// unstamped chain is ignored); present ⇒ the token must satisfy it
// under the actor's scheme, one cheap operation (postage.Scheme.Check).
func (a *Actor) checkPostage(eff cert.Cert, env envelope.Envelope) error {
	if eff.Cav.Postage == "" {
		return nil
	}
	pre, err := envelope.PostagePreimage(env)
	if err != nil {
		return err
	}
	return a.postage().Check(eff.Cav.Postage, pre, env.Postage)
}

// process runs the stateful steps for one invocation and returns the
// Status to reply with.
func (a *Actor) process(ctx context.Context, in *inbound) Status {
	env := in.env

	now := a.now()
	consents, speakAs := a.authority()
	res, err := envelope.Verify(env, envelope.Receiver{
		ID:       a.ID(),
		Consents: consents,
		SpeakAs:  speakAs,
		Chain:    a.chainRule,
		HWM:      a.hwm,
	}, now)
	// Observe on BOTH paths: res.Verified is populated alongside
	// ErrChain too, and the mark must advance from rooted certs
	// regardless of the verdict (clock contract, ADR-0019).
	a.mark.ObserveAll(res.Verified)
	if err == nil && res.Loc != nil {
		a.mu.Lock()
		a.updateLocationLocked(env.From, *res.Loc, now)
		a.mu.Unlock()
	}

	if err != nil {
		return rejectStatus(err)
	}
	if err := a.checkPostage(res.Eff, env); err != nil {
		return Status{Code: StatusPostage, Msg: err.Error()}
	}
	h, ok := a.AcceptTable[env.To.Facet]
	if !ok {
		return Status{Code: StatusUnknownFacet, Msg: env.To.Facet}
	}
	inv := &Invocation{Envelope: &env, From: env.From, Peer: in.peer, Eff: res.Eff}
	body, herr := h(ctx, inv)
	if herr != nil {
		return Status{Code: StatusError, Msg: herr.Error()}
	}
	return Status{Code: StatusOK, Body: body}
}

func rejectStatus(err error) Status {
	code := StatusRejected
	switch {
	case errors.Is(err, envelope.ErrReplay):
		code = StatusReplay
	case errors.Is(err, envelope.ErrBadLoc):
		code = StatusBadLoc
	case errors.Is(err, envelope.ErrChain):
		code = StatusUnauthorized
	case errors.Is(err, envelope.ErrSig):
		code = StatusBadSig
	case errors.Is(err, envelope.ErrWrongTarget):
		code = StatusWrongTarget
	}
	return Status{Code: code, Msg: err.Error()}
}

// ---- outbound ------------------------------------------------------------

// proofFor assembles the caller-carried proof for (to, facet): the held
// chain links (with any issuer-resolving speak-as they were stored
// with) plus every speak-as that names this actor's signer, and
// returns the postage requirement the held links name ("" if none).
// The chain is presented as held, receiver-signed first link included:
// the caller cannot tell the receiver's own consent (a frontdoor cert
// as a lookup hands it out) from a link the receiver signed AS its
// sovereign (a hub's hot key minting a member's beat grant, ADR-0018),
// so it does not try — VerifyChain folds a chain that begins with the
// rooting consent without it.
func (a *Actor) proofFor(to cert.ActorID, facet string) (proof []cert.Cert, req string) {
	held := a.Grants[GrantKey{Target: to, Facet: facet}]
	for _, c := range held {
		if req == "" {
			req = c.Cav.Postage
		}
		proof = append(proof, c)
	}
	_, speakAs := a.authority()
	for _, s := range speakAs {
		if s.Can == cert.VerbSpeakAs && s.Aud == string(a.ID()) {
			proof = append(proof, s)
		}
	}
	return proof, req
}

// edge returns the per-receiver in-flight lock.
func (a *Actor) edge(to cert.ActorID) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.edges[to]
	if !ok {
		m = &sync.Mutex{}
		a.edges[to] = m
	}
	return m
}

// Send builds, signs and sends one Invocation to facet on actor to, then
// awaits and verifies the Reply. The callee's location record, if the
// reply carried one, is cached. A non-ok Status is returned as
// *RemoteError alongside the decoded Reply. Sends to the same receiver
// are serialised (see the package doc); a dropped invocation surfaces
// as ErrNoReply and the seq it used is gone — retry means a new seq.
func (a *Actor) Send(ctx context.Context, to cert.ActorID, facet string, payload []byte) (*Reply, error) {
	if a.Transport == nil {
		return nil, ErrNoTransport
	}
	edge := a.edge(to)
	edge.Lock()
	defer edge.Unlock()

	a.mu.Lock()
	if a.seqOut[to] == 0 && a.SeqBase != nil {
		a.seqOut[to] = max(a.SeqBase()-1, 0)
	}
	a.seqOut[to]++
	seq := a.seqOut[to]
	loc := a.currentLocationLocked(a.now())
	hints := a.hintsLocked(to)
	a.mu.Unlock()

	proof, req := a.proofFor(to, facet)
	env := envelope.Envelope{
		From:    a.ID(),
		To:      envelope.Address{Target: to, Facet: facet},
		Seq:     seq,
		Payload: payload,
		Proof:   proof,
		Loc:     loc,
	}
	if req != "" {
		// Stamp before signing: the token binds to the preimage (sig and
		// postage blanked) and the signature then covers the token.
		pre, err := envelope.PostagePreimage(env)
		if err != nil {
			return nil, err
		}
		tok, err := a.postage().Solve(ctx, req, pre)
		if err != nil {
			return nil, err
		}
		env.Postage = tok
	}
	env, err := envelope.Sign(env, a.Signer)
	if err != nil {
		return nil, err
	}
	wire, err := envelope.Encode(env)
	if err != nil {
		return nil, err
	}

	s, err := a.Transport.Dial(ctx, to, hints)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := s.SendMsg(ctx, wire); err != nil {
		return nil, err
	}
	raw, err := s.RecvMsg(ctx)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrNoReply
		}
		return nil, err
	}
	rep, err := envelope.DecodeReply(raw)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	now := a.now()
	rloc, err := envelope.VerifyReply(rep, env, now)
	if err == nil && rloc != nil {
		a.updateLocationLocked(rep.From, *rloc, now)
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}

	st, err := DecodeStatus(rep.Payload)
	if err != nil {
		return nil, err
	}
	out := &Reply{Status: st, Payload: st.Body, Loc: rloc, Wire: rep}
	if st.Code != StatusOK {
		return out, &RemoteError{Code: st.Code, Msg: st.Msg}
	}
	return out, nil
}
