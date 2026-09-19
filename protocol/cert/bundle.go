package cert

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// wireBundle is Bundle on the wire: the caller's "bundle on connect"
// (ADR-0017) for a stream facet, where the connection is the
// invocation and the bundle is checked once. Every cert is its own
// strict wire JSON (DecodeCert), so an unknown caveat anywhere in the
// bundle is a decode error before Authorize sees it.
type wireBundle struct {
	Member  json.RawMessage   `json:"member"`
	Grants  []json.RawMessage `json:"grants"`
	SpeakAs []json.RawMessage `json:"speak_as"`
}

// EncodeBundle renders a Bundle as wire JSON.
func EncodeBundle(b Bundle) ([]byte, error) {
	m, err := Encode(b.Member)
	if err != nil {
		return nil, fmt.Errorf("bundle: member: %w", err)
	}
	w := wireBundle{Member: m, Grants: []json.RawMessage{}, SpeakAs: []json.RawMessage{}}
	for i, g := range b.Grants {
		raw, err := Encode(g)
		if err != nil {
			return nil, fmt.Errorf("bundle: grant[%d]: %w", i, err)
		}
		w.Grants = append(w.Grants, raw)
	}
	for i, s := range b.SpeakAs {
		raw, err := Encode(s)
		if err != nil {
			return nil, fmt.Errorf("bundle: speak_as[%d]: %w", i, err)
		}
		w.SpeakAs = append(w.SpeakAs, raw)
	}
	return json.Marshal(w)
}

// DecodeBundle strictly decodes wire JSON into a Bundle. Shape only:
// signatures and authority are Authorize's job. A bundle without a
// member cert is malformed (a stream facet has no stranger mode).
func DecodeBundle(data []byte) (Bundle, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w wireBundle
	if err := dec.Decode(&w); err != nil {
		return Bundle{}, fmt.Errorf("bundle: strict decode: %w", err)
	}
	if dec.More() {
		return Bundle{}, fmt.Errorf("bundle: trailing data")
	}
	if len(w.Member) == 0 {
		return Bundle{}, fmt.Errorf("bundle: no member cert")
	}
	var b Bundle
	var err error
	if b.Member, err = DecodeCert(w.Member); err != nil {
		return Bundle{}, fmt.Errorf("bundle: member: %w", err)
	}
	if b.Member.Can != VerbMember {
		return Bundle{}, fmt.Errorf("bundle: member cert has verb %q", b.Member.Can)
	}
	for i, raw := range w.Grants {
		g, err := DecodeCert(raw)
		if err != nil {
			return Bundle{}, fmt.Errorf("bundle: grant[%d]: %w", i, err)
		}
		b.Grants = append(b.Grants, g)
	}
	for i, raw := range w.SpeakAs {
		s, err := DecodeCert(raw)
		if err != nil {
			return Bundle{}, fmt.Errorf("bundle: speak_as[%d]: %w", i, err)
		}
		b.SpeakAs = append(b.SpeakAs, s)
	}
	return b, nil
}
