// Package cert is the sovereign-actor protocol's one primitive: a
// signed, expiring, attenuable delegation certificate, plus the pure
// per-connect check Authorize() that consumes a presented bundle of
// them.
//
// Spec: docs/desired-state/domain-model.md glossary (Verb, Grant,
// Consent grant, Facet, Attenuation, Speak-as, Time, Authorize, Group,
// Cert classes) and ADR-0017 (caller-carried grants), ADR-0018
// (speak-as / hot key resolution), ADR-0019 (iat low-water mark).
// Oracles: verification/quint/{authorize,clock}.qnt — the property
// suite in this package ports their laws 1:1.
//
// This module has NO dependency on config-server (talos, fly, nebula):
// the protocol must not know about its first consumer. It borrows the
// *approach* of config-server/ethsig (decred secp256k1 + keccak,
// EIP-191 personal_sign) rather than importing it.
//
// # Actor identifiers
//
// An actor is named by a string with a scheme prefix that selects the
// signature algorithm:
//
//	eth:0x<40 lowercase hex>   secp256k1 wallet; EIP-191 personal_sign,
//	                           verified by recovering the address.
//	ed:<64 lowercase hex>      Ed25519 public key (hub hot key, member
//	                           NodeIds, receivers).
//
// Both schemes sign the SAME bytes.
//
// # Speak-as (hot keys)
//
// A speak-as cert (can: speak-as) maps a signing key (its aud) to a
// principal (its iss): Authorize treats certs signed by the aud key as
// if signed by the iss, within cav, until exp (ADR-0018). A speak-as's
// own cav.delegable governs whether the HOT KEY may re-delegate the
// speak-as itself; it does NOT gate the member/invoke certs the hot key
// issues — those are bounded by the speak-as's cav.verbs and cav.groups.
// Resolution is single-level in v0: a speak-as whose own iss is not a
// principal the receiver holds a direct consent for resolves to nothing
// usable (the chain is not rooted at the receiver).
//
// Because the caller assembles the bundle and ANY wallet can sign a
// speak-as naming ANY key, a signer resolves to a SET of sovereigns
// (itself plus every vouching wallet). Every rule in Authorize therefore
// quantifies ONE wallet the receiver holds a live consent for: the group
// rule admits `aud: group:g` only when the same consented wallet vouches
// for both the grant's signer and the member cert's signer — never
// "resolved issuers are equal", never set overlap (decision 4oz, ruled
// from authorize.qnt mutant m14). Caveats are literal on both sides: a
// hub-signed grant to group:g needs a speak-as whose cav.groups ∋ g
// (decision w5s).
//
// # Canonical form (bytes signed)
//
// The signature covers the RFC 8785 (JCS) canonical JSON of the cert
// WITHOUT its "sig" field. The field set and canonical key order are
// fixed:
//
//	{"aud","can","cav":{"delegable","endpoints","facet","groups","name",
//	 "postage","target","verbs"},"exp","iat","iss"}
//
// Keys are sorted by UTF-16 code units (JCS); iat and exp are Unix
// seconds (int64); array element order is preserved as the issuer
// signed it; empty arrays serialize as [] (never null); an absent
// postage is the empty string. See canonicalBytes.
//
// # Chains (protocol ADR-0001)
//
// VerifyChain is the one N-link verifier: the caller presents only the
// links it holds, the receiver prepends its own consent and folds
// Attenuate over [consent, chain...]. Authorize is that verifier on a
// one-link caller chain plus the talos-only layer (member identity,
// group audiences, blocklist). Caveat vocabulary v2 adds Endpoints
// (plain intersection) and Postage (monotone presence; unlocks
// aud "*").
package cert

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gowebpki/jcs"
)

// Verb is the action a delegation grants, drawn from a closed set. An
// unknown verb is rejected (fail-closed).
type Verb string

const (
	VerbMember    Verb = "member"
	VerbInvoke    Verb = "invoke"
	VerbSpeakAs   Verb = "speak-as"
	VerbReachMeAt Verb = "reach-me-at"
	VerbRelay     Verb = "relay"
	VerbPublish   Verb = "publish"
)

// knownVerbs is the closed set; decoding any other verb rejects.
var knownVerbs = map[Verb]bool{
	VerbMember: true, VerbInvoke: true, VerbSpeakAs: true,
	VerbReachMeAt: true, VerbRelay: true, VerbPublish: true,
}

// ValidVerb reports whether v is in the closed set.
func ValidVerb(v Verb) bool { return knownVerbs[v] }

// Caveats are the structured restrictions on a cert. The verb never
// carries its object; target and facet live here so attenuation is
// field-wise intersection, not string parsing.
//
// Unknown is not a wire field: it is a verifier-side judgment set when a
// decoded cert carried a caveat key this verifier does not recognise.
// It rejects (fail-closed) inside Authorize, mirroring the Quint model's
// cav.unknown. On the wire, an unknown caveat key is caught earlier by
// DecodeCert's strict decoding. Attenuate also sets it when two links
// carry conflicting Postage (the chain is tainted).
//
// PostageConflict is likewise not a wire field: it narrows that second
// case so VerifyChain can report ErrPostageConflict (which errors.Is
// ErrUnknownCaveat) instead of the bare taint. It is never set without
// Unknown, so it changes no accept/reject decision — taint stays one
// concept, 1:1 with the model's cav.unknown.
//
// Caveat vocabulary v2 (ADR-0001): Endpoints are transport-tagged opaque
// strings attenuated by plain intersection, exactly like Target/Facet —
// an absent set is EMPTY, not "unconstrained". Postage is an opaque
// requirement string; its presence is MONOTONE over a chain (a link may
// add it, none may remove it; two links that set it must agree) and it
// never gates invoke by itself — it only unlocks aud "*" (VerifyChain).
//
// Caveat vocabulary v3 (ADR-0004): Target alone admits the wildcard
// TargetAny as its WHOLE set (a mixed ["*", id] is a decode error). It
// means "this link does not narrow the target": intersectTarget yields
// the other side, so a chain [consent{self}, grant{*}] has effective
// target {self}. It is for grants; a consent whose target is {*} roots
// nothing (VerifyChain rule 1). No other set caveat has a sentinel.
type Caveats struct {
	Target    []ActorID // grants: the actors this authority may reach, or the singleton {TargetAny}
	Facet     []string  // grants: the facet classes it may reach
	Groups    []string  // member certs: the groups the member is in
	Name      string    // member certs: the member's durable name
	Delegable bool      // false ⇒ no chain link may follow
	Verbs     []string  // speak-as: the verbs the delegated key may exercise
	Endpoints []string  // v2: transport-tagged opaque strings; intersection
	Postage   string    // v2: opaque requirement; "" = absent; monotone
	Unknown   bool      // verifier-side: carries an unrecognised caveat
	// PostageConflict is verifier-side and implies Unknown: the taint came
	// from two links setting disagreeing Postage. Diagnostics only.
	PostageConflict bool
}

// Cert is the primitive: {iss, aud, can, cav, iat, exp, sig}.
//
//   - Iss is the issuer's actor id (the signing key; a hot key resolves
//     to its principal through a speak-as, see Authorize).
//   - Aud is an actor id, "group:<name>", or "*" (anyone — bindable
//     only when the effective chain carries Postage, see VerifyChain).
//   - Iat/Exp are Unix seconds. Iat feeds the low-water mark only; it
//     never participates in authority or attenuation (ADR-0019).
//   - Sig is the raw signature over canonicalBytes: 65-byte r||s||v for
//     eth, 64-byte for ed.
type Cert struct {
	Iss ActorID
	Aud string
	Can Verb
	Cav Caveats
	Iat int64
	Exp int64
	Sig []byte
}

// canonCav / canonCert fix the JSON shape fed to JCS. All caveat fields
// are always present (empty arrays as []), so the canonical form of a
// cert is a pure function of its authority-bearing fields. Unknown and
// PostageConflict are deliberately absent: they are verifier judgments,
// not signed data.
type canonCav struct {
	Delegable bool     `json:"delegable"`
	Endpoints []string `json:"endpoints"`
	Facet     []string `json:"facet"`
	Groups    []string `json:"groups"`
	Name      string   `json:"name"`
	Postage   string   `json:"postage"`
	Target    []string `json:"target"`
	Verbs     []string `json:"verbs"`
}

type canonCert struct {
	Aud string   `json:"aud"`
	Can string   `json:"can"`
	Cav canonCav `json:"cav"`
	Exp int64    `json:"exp"`
	Iat int64    `json:"iat"`
	Iss string   `json:"iss"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// TargetAny is the wildcard target (ADR-0004): as the whole Target set
// it means the link does not narrow the target. It is not an ActorID
// any receiver answers for — a "*" that survives to the effective cert
// names no one.
const TargetAny ActorID = "*"

// IsTargetAny reports whether t is exactly the wildcard singleton.
func IsTargetAny(t []ActorID) bool { return len(t) == 1 && t[0] == TargetAny }

func targetsToStrings(t []ActorID) []string {
	out := make([]string, len(t))
	for i, a := range t {
		out[i] = string(a)
	}
	return out
}

// canonicalBytes returns the RFC 8785 canonical JSON of c without sig —
// the exact bytes an issuer signs and a verifier checks.
func canonicalBytes(c Cert) ([]byte, error) {
	cc := canonCert{
		Aud: c.Aud,
		Can: string(c.Can),
		Cav: canonCav{
			Delegable: c.Cav.Delegable,
			Endpoints: nonNil(c.Cav.Endpoints),
			Facet:     nonNil(c.Cav.Facet),
			Groups:    nonNil(c.Cav.Groups),
			Name:      c.Cav.Name,
			Postage:   c.Cav.Postage,
			Target:    targetsToStrings(c.Cav.Target),
			Verbs:     nonNil(c.Cav.Verbs),
		},
		Exp: c.Exp,
		Iat: c.Iat,
		Iss: string(c.Iss),
	}
	raw, err := json.Marshal(cc)
	if err != nil {
		return nil, fmt.Errorf("marshal cert: %w", err)
	}
	canon, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("jcs canonicalize: %w", err)
	}
	return canon, nil
}

// CanonicalBytes exposes the signed byte string for tooling and tests.
func CanonicalBytes(c Cert) ([]byte, error) { return canonicalBytes(c) }

// ErrUnknownVerb is returned by DecodeCert for a verb outside the closed
// set.
var ErrUnknownVerb = errors.New("cert: unknown verb")
