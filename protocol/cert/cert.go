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
//	{"aud","can","cav":{"delegable","facet","groups","name","target",
//	 "verbs"},"exp","iat","iss"}
//
// Keys are sorted by UTF-16 code units (JCS); iat and exp are Unix
// seconds (int64); array element order is preserved as the issuer
// signed it; empty arrays serialize as [] (never null). See
// canonicalBytes.
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
// DecodeCert's strict decoding.
type Caveats struct {
	Target    []ActorID // grants: the actors this authority may reach
	Facet     []string  // grants: the facet classes it may reach
	Groups    []string  // member certs: the groups the member is in
	Name      string    // member certs: the member's durable name
	Delegable bool      // false ⇒ no chain link may follow
	Verbs     []string  // speak-as: the verbs the delegated key may exercise
	Unknown   bool      // verifier-side: carries an unrecognised caveat
}

// Cert is the primitive: {iss, aud, can, cav, iat, exp, sig}.
//
//   - Iss is the issuer's actor id (the signing key; a hot key resolves
//     to its principal through a speak-as, see Authorize).
//   - Aud is either an actor id or "group:<name>".
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
// cert is a pure function of its authority-bearing fields. Unknown is
// deliberately absent: it is a verifier judgment, not signed data.
type canonCav struct {
	Delegable bool     `json:"delegable"`
	Facet     []string `json:"facet"`
	Groups    []string `json:"groups"`
	Name      string   `json:"name"`
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
			Facet:     nonNil(c.Cav.Facet),
			Groups:    nonNil(c.Cav.Groups),
			Name:      c.Cav.Name,
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
