// Package masterderive holds the root of the fleet's derivation tree:
// the master key (HKDF-derived from a wallet signature) and the
// derivations that hang directly off it — per-node KMS disk-seal keys,
// break-glass recovery passphrases, and the age identity that decrypts
// the repo's secrets (age.go). Mesh identities derive from the same
// master in nebderive.
//
// There is no key registry and no key state outside the master secret:
// everything here is a pure function of (master, stable identifier).
//
// The info strings below are part of the derivation contract: changing
// them (or the master key) re-keys the entire fleet. Several still say
// "wg" — they were minted when wg0 was the control channel, and they
// are FROZEN because the derived values (disk seal keys, recovery
// passphrases, the master itself) outlive the transport they were
// named after.
package masterderive

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// Derivation domains — frozen, see package comment.
const (
	masterSigInfo  = "talos-config/wg/v1/master-from-sig"
	kmsSealInfoPfx = "talos-config/kms/v1/seal-key/" // + lowercase node UUID
	recoveryPfx    = "talos-config/kms/v1/recovery/" // + normalized MAC
)

// MasterMessage is the canonical text the admin wallet signs (EIP-191
// personal_sign) to produce the fleet master key. FROZEN — the "wg" in
// it is historical, see the package comment. A signature over this
// exact message IS the master key — it must never be signed anywhere
// except the config server's unseal page (or offline via
// `cast wallet sign`), and never published.
const MasterMessage = "talos-config/wg/master/v1"

// MasterFromHex parses a 64-hex-char master secret.
func MasterFromHex(s string) ([]byte, error) {
	key, err := hex.DecodeString(s)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("master key must be 64 hex chars (32 bytes)")
	}
	return key, nil
}

// MasterFromSignature derives the 32-byte master key from a 65-byte
// EIP-191 signature (r||s||v) over MasterMessage. Only r||s feed the
// KDF: the recovery byte v is redundant and its encoding differs
// between signers (27/28 vs 0/1), so it must not influence the master.
func MasterFromSignature(sig []byte) ([]byte, error) {
	if len(sig) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes (r||s||v), got %d", len(sig))
	}
	return derive(sig[:64], masterSigInfo, 32), nil
}

// MasterFromSignatureHex is MasterFromSignature over a hex signature
// with optional 0x prefix.
func MasterFromSignatureHex(sigHex string) ([]byte, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(sigHex), "0x"))
	if err != nil {
		return nil, fmt.Errorf("signature is not valid hex")
	}
	return MasterFromSignature(sig)
}

func derive(master []byte, info string, n int) []byte {
	out, err := hkdf.Key(sha256.New, master, nil, info, n)
	if err != nil {
		panic(err) // only fails on invalid hash/length parameters
	}
	return out
}

// clamp applies standard Curve25519 private-key clamping (used by the
// age identity derivation in age.go).
func clamp(b []byte) [32]byte {
	var k [32]byte
	copy(k[:], b)
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k
}

// PublicKey returns the Curve25519 public key for priv.
func PublicKey(priv [32]byte) [32]byte {
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		panic(err) // clamped keys cannot be low-order
	}
	var p [32]byte
	copy(p[:], pub)
	return p
}

// KMSSealKey derives the AES-256 key that seals/unseals disk
// encryption keys for the node with the given SMBIOS UUID. Sealed
// blobs are AES-GCM, so possession of a blob that authenticates under
// this key proves it was sealed by this fleet's master for this UUID.
func KMSSealKey(master []byte, uuid string) []byte {
	return derive(master, kmsSealInfoPfx+NormalizeUUID(uuid), 32)
}

// NormalizeUUID lowercases and trims a node UUID. Part of the KMS
// derivation contract: Seal and Unseal must agree on the form.
func NormalizeUUID(uuid string) string {
	return strings.ToLower(strings.TrimSpace(uuid))
}

// RecoveryPassphrase derives the break-glass LUKS passphrase for the
// machine with the given normalized MAC: 160 bits, base32, grouped for
// console typing. Re-derivable offline from the master signature alone
// (cast wallet sign + `recover -recovery -mac <mac>`) — stored nowhere.
func RecoveryPassphrase(master []byte, mac string) string {
	raw := derive(master, recoveryPfx+mac, 20)
	s := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	parts := make([]string, 0, 4)
	for i := 0; i < len(s); i += 8 {
		parts = append(parts, s[i:i+8])
	}
	return strings.Join(parts, "-")
}
