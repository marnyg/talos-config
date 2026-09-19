package issuer

// #bundle: the talos-layer half of a member's beat (ADR-0024 option I,
// decision mdv). The protocol-generic #renew re-signs what the member
// already holds; #bundle is where the Owner's recipe reaches a caller
// for the first time: the Issuer compiles talos/mesh-policy-v3.yaml for
// the member's identity, signs the grants with the hot key, and returns
// them with the current blocklist, speak-as and name map. The Reply
// signature covers everything — no second signed-document format.
//
// Identity comes from the member cert the caller presents in the
// request, never from anything else it claims: the Issuer verifies the
// cert the way a receiver would (own signature, or a dead hubkey's
// resolved through the same wallet's speak-as in the proof — a member
// minted before a deploy bundles at the new process without renewing
// first), binds its aud to the envelope's signer, and compiles for its
// cav.name / cav.groups. Git is compiler input here (invariant 2): the
// recipe and blocklist are read from the checkout on every beat, and
// the Issuer keeps nothing.
//
// The ADR sketched the payload as `{}` with the member cert "in the
// caller's chain"; an envelope proof chain is invoke-only (VerifyChain
// rule 1), so the member cert rides in the payload instead.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/envelope"
)

// FacetBundle is the Issuer facet a member invokes for its grants. Like
// #renew it is an ORDINARY facet behind an ordinary grant — the Kit's
// beat grant names both — served for the wallet (target: wallet,
// ADR-0024 F).
const FacetBundle = "#bundle"

var (
	// ErrNoPolicy: the Issuer has no policy source wired; #bundle cannot
	// compile. A refusal, never an empty bundle (policy.Load's stance).
	ErrNoPolicy = errors.New("issuer: no policy source")
	// ErrBlocked: the caller's key is on the blocklist.
	ErrBlocked = errors.New("issuer: member key is blocklisted")
	// ErrMember: the presented member cert is not one this hub answers
	// for, or does not name the caller.
	ErrMember = errors.New("issuer: member cert rejected")
)

// PolicySource yields the current recipe and blocklist — git as
// compiler input. Called on every #bundle (and the blocklist on every
// #renew); nothing is cached in the Issuer.
type PolicySource func() (policy.Recipe, []cert.ActorID, error)

// FilePolicy is the PolicySource over a talos/ checkout.
func FilePolicy(root string) PolicySource {
	return func() (policy.Recipe, []cert.ActorID, error) {
		r, err := policy.Load(root)
		if err != nil {
			return policy.Recipe{}, nil, err
		}
		bl, err := policy.LoadBlocklist(root)
		if err != nil {
			return policy.Recipe{}, nil, err
		}
		return r, bl, nil
	}
}

// BundleRequest is #bundle's payload: the caller's current member cert
// (cert.Encode wire JSON).
type BundleRequest struct {
	Member json.RawMessage `json:"member"`
}

// EncodeBundleRequest renders a member cert as a #bundle payload.
func EncodeBundleRequest(member cert.Cert) ([]byte, error) {
	raw, err := cert.Encode(member)
	if err != nil {
		return nil, err
	}
	return json.Marshal(BundleRequest{Member: raw})
}

// Bundle is the #bundle reply: the invoke grants the recipe compiles for
// the caller (hubkey-signed, recipe order), the blocklist the receiver
// replaces its copy with (decision j0b), and the speak-as that resolves
// this hubkey — the same one #renew's output needs — and the name map:
// every member this hub has witnessed on the beat, with its location
// when known (namemap.go; the caller's own entry included).
type Bundle struct {
	Grants    []cert.Cert
	Blocklist []cert.ActorID
	SpeakAs   cert.Cert
	NameMap   []NameEntry
}

type wireBundle struct {
	Grants    []json.RawMessage `json:"grants"`
	Blocklist []cert.ActorID    `json:"blocklist"`
	SpeakAs   json.RawMessage   `json:"speak_as"`
	NameMap   []wireNameEntry   `json:"name_map"`
}

// EncodeBundle renders a Bundle as JSON.
func EncodeBundle(b Bundle) ([]byte, error) {
	w := wireBundle{Grants: make([]json.RawMessage, 0, len(b.Grants)), Blocklist: b.Blocklist}
	if w.Blocklist == nil {
		w.Blocklist = []cert.ActorID{}
	}
	for _, g := range b.Grants {
		raw, err := cert.Encode(g)
		if err != nil {
			return nil, err
		}
		w.Grants = append(w.Grants, raw)
	}
	var err error
	if w.SpeakAs, err = cert.Encode(b.SpeakAs); err != nil {
		return nil, err
	}
	if w.NameMap, err = encodeNameMap(b.NameMap); err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

// DecodeBundle parses EncodeBundle's output, verifying every cert's
// signature and every blocklist id's shape.
func DecodeBundle(raw []byte) (Bundle, error) {
	var w wireBundle
	if err := json.Unmarshal(raw, &w); err != nil {
		return Bundle{}, fmt.Errorf("issuer: bundle: %w", err)
	}
	b := Bundle{Grants: make([]cert.Cert, 0, len(w.Grants)), Blocklist: w.Blocklist}
	for i, raw := range w.Grants {
		g, err := cert.DecodeCert(raw)
		if err != nil {
			return Bundle{}, fmt.Errorf("issuer: bundle grant %d: %w", i, err)
		}
		if err := cert.Verify(g); err != nil {
			return Bundle{}, fmt.Errorf("issuer: bundle grant %d: %w", i, err)
		}
		b.Grants = append(b.Grants, g)
	}
	for _, id := range b.Blocklist {
		if err := id.Validate(); err != nil {
			return Bundle{}, fmt.Errorf("issuer: bundle blocklist: %w", err)
		}
	}
	sa, err := cert.DecodeCert(w.SpeakAs)
	if err != nil {
		return Bundle{}, fmt.Errorf("issuer: bundle speak-as: %w", err)
	}
	if err := cert.Verify(sa); err != nil {
		return Bundle{}, fmt.Errorf("issuer: bundle speak-as: %w", err)
	}
	b.SpeakAs = sa
	if b.NameMap, err = decodeNameMap(w.NameMap); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

// blocked reports whether id is on the current blocklist. No policy
// source ⇒ nothing is blocked (the v2 plane has its own list); a
// source that fails to load fails closed.
func (i *Issuer) blocked(id cert.ActorID) error {
	if i.Policy == nil || id == "" {
		return nil
	}
	_, bl, err := i.Policy()
	if err != nil {
		return fmt.Errorf("issuer: blocklist: %w", err)
	}
	return blockedIn(bl, id)
}

// blockedIn is the one blocklist rule, over an already-loaded list.
func blockedIn(bl []cert.ActorID, id cert.ActorID) error {
	if slices.Contains(bl, id) {
		return fmt.Errorf("%w: %s", ErrBlocked, id)
	}
	return nil
}

// bundleHandler serves #bundle. Authorization of the CALLER happened in
// the inbox (a chain rooted in the consent to the wallet, facet
// #bundle); this verifies the member cert INSIDE the request and
// compiles for it.
func (i *Issuer) bundleHandler(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	if err := i.Serving(); err != nil {
		return nil, err
	}
	if i.Policy == nil {
		return nil, ErrNoPolicy
	}
	recipe, bl, err := i.Policy()
	if err != nil {
		return nil, fmt.Errorf("issuer: bundle: %w", err)
	}
	if err := blockedIn(bl, inv.From); err != nil {
		return nil, err
	}
	var req BundleRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("issuer: bundle: %w", err)
	}
	now := i.now()
	_, proofSpeakAs := envelope.SplitProof(inv.Envelope.Proof)
	m, err := i.verifyMember(req.Member, inv.From, proofSpeakAs, now)
	if err != nil {
		return nil, err
	}
	i.witness(m)
	caller := policy.Caller{Key: inv.From, Name: m.Cav.Name, Groups: m.Cav.Groups}
	out := Bundle{Blocklist: bl, SpeakAs: *i.SpeakAs(), NameMap: i.nameMap(now, bl)}
	for _, g := range policy.Compile(recipe, caller, now) {
		signed, err := cert.Sign(g, i.signer)
		if err != nil {
			return nil, fmt.Errorf("issuer: signing grant: %w", err)
		}
		out.Grants = append(out.Grants, signed)
	}
	return EncodeBundle(out)
}

// verifyMember is the member-cert check a receiver runs (glossary steps
// 2, 2a), on the signing side: verb member, signature valid, unexpired
// at now, aud == the envelope's signer, and its issuer is this hubkey
// or a key the wallet THIS hub speaks for vouches for through a live
// speak-as in the caller's proof (cert.SpeaksFor — one rule, the same
// one #renew and VerifyChain use). The proof's speak-as never widens
// whose member certs this hub compiles for: the wallet is fixed by the
// held speak-as.
func (i *Issuer) verifyMember(raw json.RawMessage, from cert.ActorID, proofSpeakAs []cert.Cert, now int64) (cert.Cert, error) {
	m, err := cert.DecodeCert(raw)
	if err != nil {
		return cert.Cert{}, fmt.Errorf("%w: %v", ErrMember, err)
	}
	if m.Can != cert.VerbMember {
		return cert.Cert{}, fmt.Errorf("%w: not a member cert", ErrMember)
	}
	if err := cert.Verify(m); err != nil {
		return cert.Cert{}, fmt.Errorf("%w: %v", ErrMember, err)
	}
	if m.Cav.Unknown {
		return cert.Cert{}, fmt.Errorf("%w: unknown caveat", ErrMember)
	}
	if m.Exp <= now {
		return cert.Cert{}, fmt.Errorf("%w: expired", ErrMember)
	}
	if m.Aud != string(from) {
		return cert.Cert{}, fmt.Errorf("%w: aud %s is not the caller %s", ErrMember, m.Aud, from)
	}
	if m.Iss != i.ID() && !cert.SpeaksFor(i.Wallet(), m.Iss, cert.VerbMember, proofSpeakAs, now) {
		return cert.Cert{}, fmt.Errorf("%w: issuer %s is not a hot key of %s", ErrMember, m.Iss, i.Wallet())
	}
	return m, nil
}
