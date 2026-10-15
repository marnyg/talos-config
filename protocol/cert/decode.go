package cert

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// wireCav / wireCert are the on-the-wire JSON shapes. They are decoded
// strictly (DisallowUnknownFields): ANY unrecognised caveat or cert key
// is a decode error — fail-closed at the boundary, before Authorize ever
// sees the cert (mirrors the Quint model's cav.unknown ⇒ reject).
type wireCav struct {
	Target    []string `json:"target"`
	Facet     []string `json:"facet"`
	Groups    []string `json:"groups"`
	Name      string   `json:"name"`
	Delegable bool     `json:"delegable"`
	Verbs     []string `json:"verbs"`
	Endpoints []string `json:"endpoints"`
	Postage   string   `json:"postage"`
	// v4 (rotation.go): present only on rotate-root certs; omitempty so
	// Encode of every other verb is byte-for-byte what it was.
	Epoch  int64       `json:"epoch,omitempty"`
	Prev   string      `json:"prev,omitempty"`
	Policy *wirePolicy `json:"policy,omitempty"`
}

// wirePolicy is Policy on the wire (and, via canonPolicy, the JCS form
// Policy.Hash commits to).
type wirePolicy struct {
	Root      string   `json:"root"`
	Guardians []string `json:"guardians"`
	K         int      `json:"k"`
}

type wireCert struct {
	Iss string  `json:"iss"`
	Aud string  `json:"aud"`
	Can string  `json:"can"`
	Cav wireCav `json:"cav"`
	Iat int64   `json:"iat"`
	Exp int64   `json:"exp"`
	Sig string  `json:"sig"` // hex, optional "0x" prefix
}

// DecodeCert strictly decodes wire JSON into a Cert. It rejects unknown
// keys (anywhere), unknown verbs, and malformed actor ids — every
// unknown is a rejection, never a silent drop.
func DecodeCert(data []byte) (Cert, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w wireCert
	if err := dec.Decode(&w); err != nil {
		return Cert{}, fmt.Errorf("cert: strict decode: %w", err)
	}
	if dec.More() {
		return Cert{}, fmt.Errorf("cert: trailing data after cert")
	}

	if !ValidVerb(Verb(w.Can)) {
		return Cert{}, fmt.Errorf("%w: %q", ErrUnknownVerb, w.Can)
	}
	iss := ActorID(w.Iss)
	if err := iss.Validate(); err != nil {
		return Cert{}, fmt.Errorf("cert: iss: %w", err)
	}
	if err := validateAud(w.Aud); err != nil {
		return Cert{}, fmt.Errorf("cert: aud: %w", err)
	}
	target := make([]ActorID, len(w.Cav.Target))
	for i, t := range w.Cav.Target {
		id := ActorID(t)
		if id == TargetAny {
			// ADR-0004: the wildcard is the whole set or absent.
			if len(w.Cav.Target) != 1 {
				return Cert{}, fmt.Errorf("cert: cav.target: %w", ErrMixedTargetAny)
			}
			target[i] = id
			continue
		}
		if err := id.Validate(); err != nil {
			return Cert{}, fmt.Errorf("cert: cav.target[%d]: %w", i, err)
		}
		target[i] = id
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(w.Sig, "0x"))
	if err != nil {
		return Cert{}, fmt.Errorf("cert: sig not hex: %w", err)
	}
	// v4: the rotation caveats are legal only on rotate-root, and there
	// they are mandatory and well-formed (rotation.go). Any other verb
	// carrying one is rejected, not dropped (fail-closed, like an
	// unknown caveat key).
	var policy *Policy
	if w.Can == string(VerbRotateRoot) {
		if w.Cav.Policy == nil {
			return Cert{}, fmt.Errorf("%w: rotate-root without cav.policy", ErrRotationCaveat)
		}
		p := policyFromWire(*w.Cav.Policy)
		if err := p.Validate(); err != nil {
			return Cert{}, fmt.Errorf("cert: cav.policy: %w", err)
		}
		if w.Cav.Epoch < 1 {
			return Cert{}, fmt.Errorf("%w: cav.epoch %d < 1", ErrRotationCaveat, w.Cav.Epoch)
		}
		if !validPolicyHash(w.Cav.Prev) {
			return Cert{}, fmt.Errorf("%w: cav.prev is not a policy hash", ErrRotationCaveat)
		}
		policy = &p
	} else if w.Cav.Epoch != 0 || w.Cav.Prev != "" || w.Cav.Policy != nil {
		return Cert{}, fmt.Errorf("%w: on verb %q", ErrRotationCaveat, w.Can)
	}
	return Cert{
		Iss: iss,
		Aud: w.Aud,
		Can: Verb(w.Can),
		Cav: Caveats{
			Target:    target,
			Facet:     w.Cav.Facet,
			Groups:    w.Cav.Groups,
			Name:      w.Cav.Name,
			Delegable: w.Cav.Delegable,
			Verbs:     w.Cav.Verbs,
			Endpoints: w.Cav.Endpoints,
			Postage:   w.Cav.Postage,
			Epoch:     w.Cav.Epoch,
			Prev:      w.Cav.Prev,
			Policy:    policy,
		},
		Iat: w.Iat,
		Exp: w.Exp,
		Sig: sig,
	}, nil
}

func policyFromWire(w wirePolicy) Policy {
	g := make([]ActorID, len(w.Guardians))
	for i, s := range w.Guardians {
		g[i] = ActorID(s)
	}
	return Policy{Root: ActorID(w.Root), Guardians: g, K: w.K}
}

func policyToWire(p Policy) *wirePolicy {
	return &wirePolicy{Root: string(p.Root), Guardians: nonNil(targetsToStrings(p.Guardians)), K: p.K}
}

// Encode renders a cert as wire JSON with sig as lowercase hex. The
// authority-bearing fields go through canonicalBytes-compatible shaping
// (nil arrays as [], never null, matching canonicalBytes); this form is
// for transport, not for signing.
func Encode(c Cert) ([]byte, error) {
	w := wireCert{
		Iss: string(c.Iss),
		Aud: c.Aud,
		Can: string(c.Can),
		Cav: wireCav{
			Target:    targetsToStrings(c.Cav.Target),
			Facet:     nonNil(c.Cav.Facet),
			Groups:    nonNil(c.Cav.Groups),
			Name:      c.Cav.Name,
			Delegable: c.Cav.Delegable,
			Verbs:     nonNil(c.Cav.Verbs),
			Endpoints: nonNil(c.Cav.Endpoints),
			Postage:   c.Cav.Postage,
			Epoch:     c.Cav.Epoch,
			Prev:      c.Cav.Prev,
		},
		Iat: c.Iat,
		Exp: c.Exp,
		Sig: hex.EncodeToString(c.Sig),
	}
	if c.Cav.Policy != nil {
		w.Cav.Policy = policyToWire(*c.Cav.Policy)
	}
	return json.Marshal(w)
}

const groupPrefix = "group:"

// ErrMixedTargetAny: cav.target mixes the wildcard "*" with concrete ids
// (ADR-0004: the wildcard is the whole set or absent).
var ErrMixedTargetAny = errors.New("cert: cav.target mixes \"*\" with actor ids")

// AudAny is the wildcard audience: anyone may present the chain, but
// VerifyChain binds it only when the effective cert carries Postage
// (ADR-0001 reach-me-at / frontdoor records).
const AudAny = "*"

// validateAud accepts an actor id, "group:<name>", or the literal "*".
func validateAud(aud string) error {
	if aud == AudAny {
		return nil
	}
	if strings.HasPrefix(aud, groupPrefix) {
		if strings.TrimPrefix(aud, groupPrefix) == "" {
			return fmt.Errorf("%w: empty group name", ErrBadActorID)
		}
		return nil
	}
	return ActorID(aud).Validate()
}
