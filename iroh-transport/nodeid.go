package irohtransport

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/marnyg/talos-config/iroh-go/iroh"
	"github.com/marnyg/talos-config/protocol/cert"
)

// edScheme is the cert.ActorID scheme this transport can carry.
const edScheme = "ed:"

// ErrNotEdActor marks an actor id that is not an Ed25519 key and so has
// no iroh EndpointId.
var ErrNotEdActor = errors.New("irohtransport: actor id is not an ed: key")

// ActorIDOf maps an iroh EndpointId to its "ed:<hex>" actor id. The
// EndpointId's 32 bytes are the Ed25519 public key.
func ActorIDOf(id *iroh.EndpointId) (cert.ActorID, error) {
	if id == nil {
		return "", fmt.Errorf("%w: nil endpoint id", cert.ErrBadActorID)
	}
	return ActorIDFromPublicKey(id.ToBytes())
}

// ActorIDFromPublicKey maps a raw 32-byte Ed25519 public key to its
// "ed:<hex>" actor id.
func ActorIDFromPublicKey(pub []byte) (cert.ActorID, error) {
	if len(pub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("%w: public key is %d bytes, want %d", cert.ErrBadActorID, len(pub), ed25519.PublicKeySize)
	}
	a := cert.ActorID(edScheme + hex.EncodeToString(pub))
	if err := a.Validate(); err != nil {
		return "", err
	}
	return a, nil
}

// PublicKeyOf returns the 32-byte Ed25519 public key of an ed: actor id.
func PublicKeyOf(id cert.ActorID) ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	h, ok := strings.CutPrefix(string(id), edScheme)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotEdActor, id)
	}
	return hex.DecodeString(h)
}

// EndpointIDOf maps an "ed:<hex>" actor id to the iroh EndpointId that
// authenticates as that key. Non-ed ids fail with ErrNotEdActor; a
// syntactically valid hex that is not a curve point fails from iroh.
func EndpointIDOf(id cert.ActorID) (*iroh.EndpointId, error) {
	pub, err := PublicKeyOf(id)
	if err != nil {
		return nil, err
	}
	eid, err := iroh.EndpointIdFromBytes(pub)
	if err != nil {
		return nil, fmt.Errorf("irohtransport: %s: %w", id, err)
	}
	return eid, nil
}
