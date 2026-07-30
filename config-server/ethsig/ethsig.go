// Package ethsig verifies Ethereum EIP-191 personal_sign signatures:
// pure offline signature recovery — no OIDC provider, no chain RPC (EOA
// only, by design; see the repo decision log). It is the hub-side
// counterpart of walletsign (the client half): every wallet-gated flow
// — device-flow approval, /status login, hub unseal, mesh enrollment —
// reduces to RecoverPersonalSign plus an address allowlist check.
package ethsig

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	secpecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// Keccak256 is Ethereum's hash: the legacy (pre-FIPS) Keccak-256, used
// for both message hashing and address derivation.
func Keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// ErrBadSignature is returned for signatures that are not 65-byte
// r||s||v hex at all — as opposed to well-formed ones that recover to
// the wrong address.
var ErrBadSignature = errors.New("malformed signature")

// RecoverPersonalSign recovers the lowercase 0x address that produced an
// Ethereum personal_sign signature (r||s||v hex) over message.
func RecoverPersonalSign(message, sigHex string) (string, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(sigHex), "0x"))
	if err != nil || len(sig) != 65 {
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

	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := Keccak256([]byte(prefixed))

	pub, _, err := secpecdsa.RecoverCompact(compact, hash)
	if err != nil {
		return "", fmt.Errorf("recovering public key: %w", err)
	}

	uncompressed := pub.SerializeUncompressed() // 0x04 || X || Y
	addr := Keccak256(uncompressed[1:])[12:]
	return "0x" + hex.EncodeToString(addr), nil
}

// NormalizeAddress lowercases and validates an 0x address.
func NormalizeAddress(addr string) (string, error) {
	a := strings.ToLower(strings.TrimSpace(addr))
	if !strings.HasPrefix(a, "0x") || len(a) != 42 {
		return "", fmt.Errorf("invalid ethereum address %q", addr)
	}
	if _, err := hex.DecodeString(a[2:]); err != nil {
		return "", fmt.Errorf("invalid ethereum address %q", addr)
	}
	return a, nil
}
