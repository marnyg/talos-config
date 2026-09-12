package cert

import (
	"crypto/ed25519"
	"strings"
)

// VerifyBytes checks sig over msg under the algorithm id's scheme
// selects. It is the scheme dispatch behind Verify, exposed so other
// self-authenticating records (envelope, reply) can be signed by a
// Signer and checked under the same two schemes without re-implementing
// EIP-191 recovery. msg is whatever canonical byte string the caller
// signed; this function attaches no meaning to it.
func VerifyBytes(id ActorID, msg, sig []byte) error {
	switch {
	case strings.HasPrefix(string(id), schemeEth):
		want, err := id.ethAddress()
		if err != nil {
			return err
		}
		got, err := recoverPersonalSign(msg, sig)
		if err != nil {
			return err
		}
		if got != want {
			return ErrSigMismatch
		}
		return nil
	case strings.HasPrefix(string(id), schemeEd):
		pub, err := id.edPublicKey()
		if err != nil {
			return err
		}
		if len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, msg, sig) {
			return ErrSigMismatch
		}
		return nil
	default:
		return ErrUnknownScheme
	}
}
