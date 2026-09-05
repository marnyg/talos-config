package cert

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ActorID is a scheme-prefixed actor identifier. The scheme selects the
// signature algorithm (see package doc): "eth:0x…" or "ed:…".
type ActorID string

const (
	schemeEth = "eth:"
	schemeEd  = "ed:"
)

var (
	// ErrBadActorID marks a syntactically invalid actor id.
	ErrBadActorID = errors.New("cert: malformed actor id")
	// ErrUnknownScheme marks an id whose scheme selects no algorithm.
	ErrUnknownScheme = errors.New("cert: unknown actor id scheme")
)

// Scheme returns the scheme prefix ("eth:" or "ed:") of a validated id.
func (a ActorID) Scheme() (string, error) {
	switch {
	case strings.HasPrefix(string(a), schemeEth):
		return schemeEth, nil
	case strings.HasPrefix(string(a), schemeEd):
		return schemeEd, nil
	default:
		return "", ErrUnknownScheme
	}
}

// Validate checks the scheme and the length/charset of the key material.
// It does NOT confirm the key is on-curve; verification does that.
func (a ActorID) Validate() error {
	switch {
	case strings.HasPrefix(string(a), schemeEth):
		hexPart := strings.TrimPrefix(string(a), schemeEth+"0x")
		if len(string(a)) != len(schemeEth)+2+40 {
			return fmt.Errorf("%w: eth id must be eth:0x + 40 hex", ErrBadActorID)
		}
		if !isLowerHex(hexPart) || len(hexPart) != 40 {
			return fmt.Errorf("%w: eth address not 40 lowercase hex", ErrBadActorID)
		}
		return nil
	case strings.HasPrefix(string(a), schemeEd):
		hexPart := strings.TrimPrefix(string(a), schemeEd)
		if !isLowerHex(hexPart) || len(hexPart) != 64 {
			return fmt.Errorf("%w: ed key not 64 lowercase hex", ErrBadActorID)
		}
		return nil
	default:
		return ErrUnknownScheme
	}
}

// ethAddress returns the lowercase 0x address of an eth actor id.
func (a ActorID) ethAddress() (string, error) {
	if !strings.HasPrefix(string(a), schemeEth) {
		return "", ErrUnknownScheme
	}
	if err := a.Validate(); err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(a), schemeEth), nil
}

// edPublicKey returns the 32-byte Ed25519 public key of an ed actor id.
func (a ActorID) edPublicKey() ([]byte, error) {
	if !strings.HasPrefix(string(a), schemeEd) {
		return nil, ErrUnknownScheme
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimPrefix(string(a), schemeEd))
}

func isLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
