// Package envelope is the sovereign-actor protocol's messaging record:
// a self-authenticating Envelope (one capability invocation) and the
// Reply bound to it. Spec: protocol/docs ADR-0001 § Decision Outcome
// (envelope / reply / invocation / location record).
//
// Both records are signed exactly like a cert: the signature covers the
// RFC 8785 (JCS) canonical JSON of the record WITHOUT its "sig" field,
// under the scheme the signer's actor id selects (cert.Signer to sign,
// cert.VerifyBytes to check). Payload is opaque bytes; the protocol
// never parses it.
//
// # Canonical form
//
// Envelope (keys in JCS order, all always present):
//
//	{"from","loc","payload","proof","seq","to":{"facet","target"}}
//
// loc is the wire JSON of a cert or null; proof is an array of cert wire
// JSON (each including its own sig — the chain is data the sender
// commits to); payload is base64. Reply:
//
//	{"from","loc","payload","re"}
//
// re is the SHA-256 of the request envelope's wire bytes (Encode), i.e.
// of the exact signed instance the requester sent.
//
// # Verify is cost-ordered
//
// Envelope signature → to.target == receiver → seq high-water mark →
// loc (if present) → proof chain via the injected ChainVerifier. The
// chain step is the only expensive one and the only one this package
// does not own: VerifyChain lives in cert; this package takes it as a
// function value so the envelope shape does not depend on chain rules.
//
// Signer ≠ transport peer is allowed, which makes the per-(sender,
// receiver) seq high-water mark load-bearing (a volatile counter,
// invariant 8), not defence in depth.
package envelope

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gowebpki/jcs"

	"github.com/marnyg/talos-config/protocol/cert"
)

// Address names the receiving actor and the facet class the invocation
// is aimed at. Facet is matched by the ChainVerifier against the
// effective cert's cav.facet.
type Address struct {
	Target cert.ActorID
	Facet  string
}

// Envelope is one capability invocation: {from, to, seq, payload,
// proof[], loc?, sig}.
//
//   - From is the signing actor (set by Sign).
//   - Seq is the sender's monotone counter towards To.Target; starts at
//     1 (the receiver's high-water mark starts at 0).
//   - Proof is the caller-carried chain: delegation links plus any
//     speak-as certs needed to resolve their signers. Verify splits them
//     by verb (can: speak-as → speakAs, everything else → chain, order
//     preserved) before handing them to the ChainVerifier.
//   - Loc is an optional reach-me-at cert issued by From (discovery
//     piggyback). A present-but-invalid Loc rejects the whole envelope.
type Envelope struct {
	From    cert.ActorID
	To      Address
	Seq     int64
	Payload []byte
	Proof   []cert.Cert
	Loc     *cert.Cert
	Sig     []byte
}

// Reply is the at-most-one answer to an Envelope: {re, from, payload,
// loc?, sig}. It carries no proof chain — the open stream is the
// invitation; only the requester could have opened it.
type Reply struct {
	Re      []byte // SHA-256 of the request's wire bytes (Hash)
	From    cert.ActorID
	Payload []byte
	Loc     *cert.Cert
	Sig     []byte
}

// ChainVerifier is the proof-chain rule, injected so this package never
// depends on chain semantics. Contract (cert.VerifyChain): chain is the
// caller-presented links; the verifier prepends the receiver-held consent
// it resolves for the first link's signer, folds Attenuate, checks
// target/facet/expiry/delegability/aud binding against signer, and
// returns the effective cert plus the rooted certs the caller should
// feed to its clock.Mark (ObserveAll).
type ChainVerifier func(
	receiver cert.ActorID,
	consents, chain, speakAs []cert.Cert,
	signer cert.ActorID,
	facet string,
	now int64,
) (eff cert.Cert, verified []cert.Cert, err error)

// Receiver is the verifier-side state an actor holds for Verify: its own
// id, the consents it issued (roots of every valid chain, invariant 2),
// the chain rule, and its volatile seq high-water marks.
type Receiver struct {
	ID       cert.ActorID
	Consents []cert.Cert
	Chain    ChainVerifier
	HWM      *HWM
}

// Result is what a successful Verify hands the actor: the effective
// authority the envelope invoked under, the rooted certs for the
// low-water mark, and the sender's location record if it carried one.
type Result struct {
	Eff      cert.Cert
	Verified []cert.Cert
	Loc      *cert.Cert
}

var (
	// ErrSig marks an envelope or reply whose sig does not verify under from.
	ErrSig = errors.New("envelope: signature does not verify under from")
	// ErrWrongTarget marks an envelope addressed to another actor.
	ErrWrongTarget = errors.New("envelope: to.target is not this receiver")
	// ErrReplay marks a seq at or below the sender's high-water mark.
	ErrReplay = errors.New("envelope: seq not above high-water mark")
	// ErrBadLoc marks a present reach-me-at record that fails sig, verb,
	// issuer, or expiry — the whole envelope is rejected (fail closed).
	ErrBadLoc = errors.New("envelope: invalid loc record")
	// ErrNoChainVerifier marks a Receiver with no chain rule: fail closed.
	ErrNoChainVerifier = errors.New("envelope: receiver has no ChainVerifier")
	// ErrChain wraps a ChainVerifier rejection.
	ErrChain = errors.New("envelope: proof chain rejected")
	// ErrReplyMismatch marks a reply whose re is not the request's hash.
	ErrReplyMismatch = errors.New("envelope: reply.re does not match request")
	// ErrReplyFrom marks a reply not signed by the request's to.target.
	ErrReplyFrom = errors.New("envelope: reply.from is not the request target")
)

// ---- canonical / wire form ------------------------------------------

type wireTo struct {
	Facet  string `json:"facet"`
	Target string `json:"target"`
}

type wireEnvelope struct {
	From    string            `json:"from"`
	Loc     json.RawMessage   `json:"loc"`
	Payload []byte            `json:"payload"`
	Proof   []json.RawMessage `json:"proof"`
	Seq     int64             `json:"seq"`
	Sig     string            `json:"sig,omitempty"`
	To      wireTo            `json:"to"`
}

type wireReply struct {
	From    string          `json:"from"`
	Loc     json.RawMessage `json:"loc"`
	Payload []byte          `json:"payload"`
	Re      string          `json:"re"`
	Sig     string          `json:"sig,omitempty"`
}

func nonNilBytes(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func encodeLoc(loc *cert.Cert) (json.RawMessage, error) {
	if loc == nil {
		return json.RawMessage("null"), nil
	}
	raw, err := cert.Encode(*loc)
	if err != nil {
		return nil, fmt.Errorf("encode loc: %w", err)
	}
	return raw, nil
}

func envelopeShape(e Envelope, withSig bool) (wireEnvelope, error) {
	loc, err := encodeLoc(e.Loc)
	if err != nil {
		return wireEnvelope{}, err
	}
	proof := make([]json.RawMessage, len(e.Proof))
	for i, c := range e.Proof {
		raw, err := cert.Encode(c)
		if err != nil {
			return wireEnvelope{}, fmt.Errorf("encode proof[%d]: %w", i, err)
		}
		proof[i] = raw
	}
	w := wireEnvelope{
		From:    string(e.From),
		Loc:     loc,
		Payload: nonNilBytes(e.Payload),
		Proof:   proof,
		Seq:     e.Seq,
		To:      wireTo{Facet: e.To.Facet, Target: string(e.To.Target)},
	}
	if withSig {
		w.Sig = hex.EncodeToString(e.Sig)
	}
	return w, nil
}

func replyShape(r Reply, withSig bool) (wireReply, error) {
	loc, err := encodeLoc(r.Loc)
	if err != nil {
		return wireReply{}, err
	}
	w := wireReply{
		From:    string(r.From),
		Loc:     loc,
		Payload: nonNilBytes(r.Payload),
		Re:      hex.EncodeToString(r.Re),
	}
	if withSig {
		w.Sig = hex.EncodeToString(r.Sig)
	}
	return w, nil
}

func canonicalize(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	canon, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("jcs canonicalize: %w", err)
	}
	return canon, nil
}

// CanonicalBytes returns the JCS bytes an envelope's sig covers (no sig).
func CanonicalBytes(e Envelope) ([]byte, error) {
	w, err := envelopeShape(e, false)
	if err != nil {
		return nil, err
	}
	return canonicalize(w)
}

// Encode returns the envelope's wire bytes: canonical JSON including sig
// (lowercase hex). Hash is computed over exactly these bytes.
func Encode(e Envelope) ([]byte, error) {
	w, err := envelopeShape(e, true)
	if err != nil {
		return nil, err
	}
	return canonicalize(w)
}

// Hash is the SHA-256 of Encode(e): the value a Reply's re must carry.
func Hash(e Envelope) ([]byte, error) {
	wire, err := Encode(e)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(wire)
	return sum[:], nil
}

// ReplyCanonicalBytes returns the JCS bytes a reply's sig covers (no sig).
func ReplyCanonicalBytes(r Reply) ([]byte, error) {
	w, err := replyShape(r, false)
	if err != nil {
		return nil, err
	}
	return canonicalize(w)
}

// EncodeReply returns the reply's wire bytes (canonical JSON incl. sig).
func EncodeReply(r Reply) ([]byte, error) {
	w, err := replyShape(r, true)
	if err != nil {
		return nil, err
	}
	return canonicalize(w)
}

func decodeLoc(raw json.RawMessage) (*cert.Cert, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	c, err := cert.DecodeCert(raw)
	if err != nil {
		return nil, fmt.Errorf("loc: %w", err)
	}
	return &c, nil
}

func decodeHex(field, s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return nil, fmt.Errorf("envelope: %s not hex: %w", field, err)
	}
	return b, nil
}

func strictDecode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("envelope: strict decode: %w", err)
	}
	if dec.More() {
		return errors.New("envelope: trailing data")
	}
	return nil
}

// Decode strictly decodes wire JSON into an Envelope: unknown keys,
// malformed ids, and undecodable certs reject. It does not verify.
func Decode(data []byte) (Envelope, error) {
	var w wireEnvelope
	if err := strictDecode(data, &w); err != nil {
		return Envelope{}, err
	}
	from := cert.ActorID(w.From)
	if err := from.Validate(); err != nil {
		return Envelope{}, fmt.Errorf("envelope: from: %w", err)
	}
	target := cert.ActorID(w.To.Target)
	if err := target.Validate(); err != nil {
		return Envelope{}, fmt.Errorf("envelope: to.target: %w", err)
	}
	proof := make([]cert.Cert, len(w.Proof))
	for i, raw := range w.Proof {
		c, err := cert.DecodeCert(raw)
		if err != nil {
			return Envelope{}, fmt.Errorf("envelope: proof[%d]: %w", i, err)
		}
		proof[i] = c
	}
	loc, err := decodeLoc(w.Loc)
	if err != nil {
		return Envelope{}, fmt.Errorf("envelope: %w", err)
	}
	sig, err := decodeHex("sig", w.Sig)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		From:    from,
		To:      Address{Target: target, Facet: w.To.Facet},
		Seq:     w.Seq,
		Payload: w.Payload,
		Proof:   proof,
		Loc:     loc,
		Sig:     sig,
	}, nil
}

// DecodeReply strictly decodes wire JSON into a Reply. It does not verify.
func DecodeReply(data []byte) (Reply, error) {
	var w wireReply
	if err := strictDecode(data, &w); err != nil {
		return Reply{}, err
	}
	from := cert.ActorID(w.From)
	if err := from.Validate(); err != nil {
		return Reply{}, fmt.Errorf("envelope: reply.from: %w", err)
	}
	loc, err := decodeLoc(w.Loc)
	if err != nil {
		return Reply{}, fmt.Errorf("envelope: reply %w", err)
	}
	re, err := decodeHex("re", w.Re)
	if err != nil {
		return Reply{}, err
	}
	sig, err := decodeHex("sig", w.Sig)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Re: re, From: from, Payload: w.Payload, Loc: loc, Sig: sig}, nil
}

// ---- sign ---------------------------------------------------------------

// Sign sets e.From to the signer's actor id, canonicalizes, and returns
// the signed envelope. Any canonicalization error fails closed.
func Sign(e Envelope, s cert.Signer) (Envelope, error) {
	e.From = s.ActorID()
	canon, err := CanonicalBytes(e)
	if err != nil {
		return Envelope{}, err
	}
	sig, err := s.Sign(canon)
	if err != nil {
		return Envelope{}, fmt.Errorf("envelope: sign: %w", err)
	}
	e.Sig = sig
	return e, nil
}

// NewReply builds the unsigned reply to req: re = Hash(req).
func NewReply(req Envelope, payload []byte, loc *cert.Cert) (Reply, error) {
	re, err := Hash(req)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Re: re, Payload: payload, Loc: loc}, nil
}

// SignReply sets r.From to the signer's actor id and signs.
func SignReply(r Reply, s cert.Signer) (Reply, error) {
	r.From = s.ActorID()
	canon, err := ReplyCanonicalBytes(r)
	if err != nil {
		return Reply{}, err
	}
	sig, err := s.Sign(canon)
	if err != nil {
		return Reply{}, fmt.Errorf("envelope: sign reply: %w", err)
	}
	r.Sig = sig
	return r, nil
}

// ---- verify -------------------------------------------------------------

// VerifySig checks e.Sig over e's canonical bytes under e.From. It is
// step 1 of Verify, exposed for callers that only need authenticity.
func VerifySig(e Envelope) error {
	canon, err := CanonicalBytes(e)
	if err != nil {
		return err
	}
	if err := cert.VerifyBytes(e.From, canon, e.Sig); err != nil {
		return fmt.Errorf("%w: %w", ErrSig, err)
	}
	return nil
}

// VerifyReplySig checks r.Sig over r's canonical bytes under r.From.
func VerifyReplySig(r Reply) error {
	canon, err := ReplyCanonicalBytes(r)
	if err != nil {
		return err
	}
	if err := cert.VerifyBytes(r.From, canon, r.Sig); err != nil {
		return fmt.Errorf("%w: %w", ErrSig, err)
	}
	return nil
}

// checkLoc is the one fail-closed rule for a piggybacked location
// record: a reach-me-at cert, issued by the record's carrier, whose
// signature verifies and which is unexpired at now. Aud is not judged
// here (the ADR writes "*"); endpoints are opaque to the protocol.
func checkLoc(loc *cert.Cert, from cert.ActorID, now int64) error {
	if loc == nil {
		return nil
	}
	switch {
	case loc.Can != cert.VerbReachMeAt:
		return fmt.Errorf("%w: can=%q", ErrBadLoc, loc.Can)
	case loc.Iss != from:
		return fmt.Errorf("%w: iss %s is not from %s", ErrBadLoc, loc.Iss, from)
	case loc.Exp <= now:
		return fmt.Errorf("%w: expired", ErrBadLoc)
	}
	if err := cert.Verify(*loc); err != nil {
		return fmt.Errorf("%w: %w", ErrBadLoc, err)
	}
	return nil
}

// splitProof partitions the caller-carried proof into speak-as certs and
// chain links, preserving order within each.
func splitProof(proof []cert.Cert) (chain, speakAs []cert.Cert) {
	for _, c := range proof {
		if c.Can == cert.VerbSpeakAs {
			speakAs = append(speakAs, c)
		} else {
			chain = append(chain, c)
		}
	}
	return chain, speakAs
}

// Verify runs the cost-ordered check of an inbound envelope for r at
// local time now:
//
//  1. signature under e.From
//  2. e.To.Target == r.ID
//  3. seq above the (e.From, r.ID) high-water mark — advances it
//  4. loc, if present (sig / verb / issuer / expiry)
//  5. proof chain via r.Chain with signer = e.From, facet = e.To.Facet
//
// The high-water mark advances at step 3 even if a later step rejects:
// the seq was spent by a signature only the sender could produce, so
// nothing a third party can do burns a sender's numbers, and a sender
// never reuses one. On success the returned Result carries the rooted
// certs for the caller's clock.Mark and the validated loc for its cache.
func Verify(e Envelope, r Receiver, now int64) (Result, error) {
	if err := VerifySig(e); err != nil {
		return Result{}, err
	}
	if e.To.Target != r.ID {
		return Result{}, fmt.Errorf("%w: %s", ErrWrongTarget, e.To.Target)
	}
	if r.HWM == nil || !r.HWM.Check(e.From, r.ID, e.Seq) {
		return Result{}, fmt.Errorf("%w: seq=%d", ErrReplay, e.Seq)
	}
	if err := checkLoc(e.Loc, e.From, now); err != nil {
		return Result{}, err
	}
	if r.Chain == nil {
		return Result{}, ErrNoChainVerifier
	}
	chain, speakAs := splitProof(e.Proof)
	eff, verified, err := r.Chain(r.ID, r.Consents, chain, speakAs, e.From, e.To.Facet, now)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrChain, err)
	}
	return Result{Eff: eff, Verified: verified, Loc: e.Loc}, nil
}

// VerifyReply checks an inbound reply against the request the caller
// sent: signature under rep.From, re == Hash(req), rep.From ==
// req.To.Target, and loc if present. Returns the validated loc (nil when
// absent) for the requester's location cache.
func VerifyReply(rep Reply, req Envelope, now int64) (*cert.Cert, error) {
	if err := VerifyReplySig(rep); err != nil {
		return nil, err
	}
	want, err := Hash(req)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(rep.Re, want) {
		return nil, ErrReplyMismatch
	}
	if rep.From != req.To.Target {
		return nil, fmt.Errorf("%w: %s", ErrReplyFrom, rep.From)
	}
	if err := checkLoc(rep.Loc, rep.From, now); err != nil {
		return nil, err
	}
	return rep.Loc, nil
}
