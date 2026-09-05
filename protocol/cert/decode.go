package cert

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
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
		if err := id.Validate(); err != nil {
			return Cert{}, fmt.Errorf("cert: cav.target[%d]: %w", i, err)
		}
		target[i] = id
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(w.Sig, "0x"))
	if err != nil {
		return Cert{}, fmt.Errorf("cert: sig not hex: %w", err)
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
		},
		Iat: w.Iat,
		Exp: w.Exp,
		Sig: sig,
	}, nil
}

// Encode renders a cert as wire JSON with sig as lowercase hex. The
// authority-bearing fields go through canonicalBytes-compatible shaping;
// this form is for transport, not for signing.
func Encode(c Cert) ([]byte, error) {
	w := wireCert{
		Iss: string(c.Iss),
		Aud: c.Aud,
		Can: string(c.Can),
		Cav: wireCav{
			Target:    targetsToStrings(c.Cav.Target),
			Facet:     c.Cav.Facet,
			Groups:    c.Cav.Groups,
			Name:      c.Cav.Name,
			Delegable: c.Cav.Delegable,
			Verbs:     c.Cav.Verbs,
		},
		Iat: c.Iat,
		Exp: c.Exp,
		Sig: hex.EncodeToString(c.Sig),
	}
	return json.Marshal(w)
}

const groupPrefix = "group:"

// validateAud accepts either an actor id or "group:<name>".
func validateAud(aud string) error {
	if strings.HasPrefix(aud, groupPrefix) {
		if strings.TrimPrefix(aud, groupPrefix) == "" {
			return fmt.Errorf("%w: empty group name", ErrBadActorID)
		}
		return nil
	}
	return ActorID(aud).Validate()
}
