package cert

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// Signer produces a cert's signature and names the issuing actor. Both
// implementations sign the same bytes (canonicalBytes).
type Signer interface {
	// ActorID is the issuer id this signer authenticates as.
	ActorID() ActorID
	// Sign signs the canonical bytes of a cert.
	Sign(canon []byte) ([]byte, error)
}

// EdSigner signs as an ed:<hex> actor with an Ed25519 private key.
type EdSigner struct {
	priv ed25519.PrivateKey
	id   ActorID
}

// NewEdSigner wraps an Ed25519 private key.
func NewEdSigner(priv ed25519.PrivateKey) EdSigner {
	pub := priv.Public().(ed25519.PublicKey)
	return EdSigner{priv: priv, id: ActorID(schemeEd + hex.EncodeToString(pub))}
}

func (s EdSigner) ActorID() ActorID { return s.id }

func (s EdSigner) Sign(canon []byte) ([]byte, error) {
	return ed25519.Sign(s.priv, canon), nil
}

// EthSigner signs as an eth:0x<addr> actor with a secp256k1 private key
// via EIP-191 personal_sign.
type EthSigner struct {
	priv *secp.PrivateKey
	id   ActorID
}

// NewEthSigner wraps a secp256k1 private key.
func NewEthSigner(priv *secp.PrivateKey) EthSigner {
	addr := ethAddressOf(priv.PubKey())
	return EthSigner{priv: priv, id: ActorID(schemeEth + addr)}
}

func (s EthSigner) ActorID() ActorID { return s.id }

func (s EthSigner) Sign(canon []byte) ([]byte, error) {
	return signPersonalSign(s.priv, canon), nil
}

// Sign sets c.Iss to the signer's actor id, canonicalizes, and returns
// the signed cert.
func Sign(c Cert, s Signer) (Cert, error) {
	c.Iss = s.ActorID()
	canon, err := canonicalBytes(c)
	if err != nil {
		return Cert{}, err
	}
	sig, err := s.Sign(canon)
	if err != nil {
		return Cert{}, fmt.Errorf("sign: %w", err)
	}
	c.Sig = sig
	return c, nil
}

// ErrSigMismatch marks a signature that does not verify under c.Iss.
var ErrSigMismatch = errors.New("cert: signature does not verify under iss")

// Verify checks c.Sig over c's canonical bytes under the algorithm the
// c.Iss scheme selects. It is authority verification only: expiry,
// caveats, chain resolution and the low-water mark are Authorize's job.
func Verify(c Cert) error {
	canon, err := canonicalBytes(c)
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(string(c.Iss), schemeEth):
		want, err := c.Iss.ethAddress()
		if err != nil {
			return err
		}
		got, err := recoverPersonalSign(canon, c.Sig)
		if err != nil {
			return err
		}
		if got != want {
			return ErrSigMismatch
		}
		return nil
	case strings.HasPrefix(string(c.Iss), schemeEd):
		pub, err := c.Iss.edPublicKey()
		if err != nil {
			return err
		}
		if len(c.Sig) != ed25519.SignatureSize || !ed25519.Verify(pub, canon, c.Sig) {
			return ErrSigMismatch
		}
		return nil
	default:
		return ErrUnknownScheme
	}
}

// verifies reports whether c's signature is valid under c.Iss (a
// boolean convenience for the pure Authorize path).
func verifies(c Cert) bool { return Verify(c) == nil }
