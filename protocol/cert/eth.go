package cert

import (
	"encoding/hex"
	"errors"
	"fmt"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// This file borrows the APPROACH of config-server/ethsig (decred
// secp256k1 + legacy keccak, EIP-191 personal_sign) without importing
// it — the protocol module must not depend on the hub.

// ErrBadSignature marks a signature that is not well-formed at all (as
// opposed to a well-formed one that recovers to the wrong key).
var ErrBadSignature = errors.New("cert: malformed signature")

func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// eip191Hash returns the keccak hash of the EIP-191 personal_sign
// prefix over message.
func eip191Hash(message []byte) []byte {
	prefixed := fmt.Appendf(nil, "\x19Ethereum Signed Message:\n%d%s", len(message), message)
	return keccak256(prefixed)
}

// recoverPersonalSign recovers the lowercase 0x address that produced an
// EIP-191 personal_sign signature (65-byte r||s||v) over message.
func recoverPersonalSign(message, sig []byte) (string, error) {
	if len(sig) != 65 {
		return "", ErrBadSignature
	}
	v := sig[64]
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return "", ErrBadSignature
	}
	// decred's RecoverCompact wants [recoveryCode || r || s] with
	// recoveryCode = 27 + recid; Ethereum uses [r || s || v].
	compact := make([]byte, 65)
	compact[0] = v + 27
	copy(compact[1:], sig[:64])

	pub, _, err := secpecdsa.RecoverCompact(compact, eip191Hash(message))
	if err != nil {
		return "", fmt.Errorf("recovering public key: %w", err)
	}
	uncompressed := pub.SerializeUncompressed() // 0x04 || X || Y
	addr := keccak256(uncompressed[1:])[12:]
	return "0x" + hex.EncodeToString(addr), nil
}

// signPersonalSign produces a 65-byte r||s||v EIP-191 signature over
// message with a secp256k1 private key.
func signPersonalSign(priv *secp.PrivateKey, message []byte) []byte {
	// SignCompact returns [recoveryCode || r || s] with recoveryCode =
	// 27 + recid; Ethereum wants [r || s || v] with v = recid (+27).
	compact := secpecdsa.SignCompact(priv, eip191Hash(message), false)
	out := make([]byte, 65)
	copy(out, compact[1:]) // r || s
	out[64] = compact[0] - 27
	return out
}

// ethAddressOf derives the lowercase 0x address of a secp256k1 public
// key (used to build an eth: actor id for a signer).
func ethAddressOf(pub *secp.PublicKey) string {
	uncompressed := pub.SerializeUncompressed()
	addr := keccak256(uncompressed[1:])[12:]
	return "0x" + hex.EncodeToString(addr)
}
