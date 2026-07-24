package main

// Sign-In-with-Ethereum-style approval: the admin proves control of an
// allowlisted address by signing a canonical message (EIP-191
// personal_sign) over the approval action. Verification is pure offline
// signature recovery — no OIDC provider, no chain RPC (EOA only, by
// design; see the repo decision log).

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	secpecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// approvalMessage is the canonical text the admin signs. The nonce is
// generated per device authorization, making every message unique.
func approvalMessage(action, userCode, nonce string) string {
	return fmt.Sprintf(
		"talos config-server machine approval\naction: %s\nuser_code: %s\nnonce: %s",
		action, userCode, nonce,
	)
}

var errBadSignature = errors.New("malformed signature")

// recoverPersonalSign recovers the lowercase 0x address that produced an
// Ethereum personal_sign signature (r||s||v hex) over message.
func recoverPersonalSign(message, sigHex string) (string, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(sigHex), "0x"))
	if err != nil || len(sig) != 65 {
		return "", errBadSignature
	}

	v := sig[64]
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return "", errBadSignature
	}

	// decred's RecoverCompact wants [recoveryCode || r || s] with
	// recoveryCode = 27 + recid; Ethereum uses [r || s || v].
	compact := make([]byte, 65)
	compact[0] = v + 27
	copy(compact[1:], sig[:64])

	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := keccak256([]byte(prefixed))

	pub, _, err := secpecdsa.RecoverCompact(compact, hash)
	if err != nil {
		return "", fmt.Errorf("recovering public key: %w", err)
	}

	uncompressed := pub.SerializeUncompressed() // 0x04 || X || Y
	addr := keccak256(uncompressed[1:])[12:]
	return "0x" + hex.EncodeToString(addr), nil
}

// normalizeAddress lowercases and validates an 0x address.
func normalizeAddress(addr string) (string, error) {
	a := strings.ToLower(strings.TrimSpace(addr))
	if !strings.HasPrefix(a, "0x") || len(a) != 42 {
		return "", fmt.Errorf("invalid ethereum address %q", addr)
	}
	if _, err := hex.DecodeString(a[2:]); err != nil {
		return "", fmt.Errorf("invalid ethereum address %q", addr)
	}
	return a, nil
}
