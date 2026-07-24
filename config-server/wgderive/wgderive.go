// Package wgderive deterministically derives WireGuard key material
// and tunnel addresses from a single 32-byte master secret using
// HKDF-SHA256. The server key and every machine key are re-derivable
// from the master key alone — there is no peer registry and no key
// state outside the master secret.
//
// The info strings below are part of the derivation contract: changing
// them (or the master key) re-keys the entire fleet.
package wgderive

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// Derivation domains — frozen, see package comment.
const (
	serverInfo     = "talos-config/wg/v1/server-key"
	machineInfoPfx = "talos-config/wg/v1/machine-key/" // + normalized MAC
	addrInfoPfx    = "talos-config/wg/v1/tunnel-addr/" // + normalized MAC
	masterSigInfo  = "talos-config/wg/v1/master-from-sig"
)

// MasterMessage is the canonical text the admin wallet signs (EIP-191
// personal_sign) to produce the fleet master key. FROZEN. A signature
// over this exact message IS the master key — it must never be signed
// anywhere except the config server's unseal page (or offline via
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

// clamp applies standard Curve25519 private-key clamping.
func clamp(b []byte) [32]byte {
	var k [32]byte
	copy(k[:], b)
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k
}

// ServerKey derives the server's WG private key.
func ServerKey(master []byte) [32]byte {
	return clamp(derive(master, serverInfo, 32))
}

// MachineKey derives the WG private key for the machine with the given
// normalized MAC (lowercase, colon-separated).
func MachineKey(master []byte, mac string) [32]byte {
	return clamp(derive(master, machineInfoPfx+mac, 32))
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

// KeyHex renders a key for WG UAPI (IpcSet) use.
func KeyHex(k [32]byte) string { return hex.EncodeToString(k[:]) }

// KeyBase64 renders a key in the standard WireGuard encoding used by
// Talos machine configs.
func KeyBase64(k [32]byte) string { return base64.StdEncoding.EncodeToString(k[:]) }

// TunnelIP deterministically assigns the machine a host address in
// subnet (must be an IPv4 /24): hosts 2–254, .1 is reserved for the
// server. Collisions between machines are possible and must be checked
// by the caller.
func TunnelIP(master []byte, mac string, subnet netip.Prefix) (netip.Addr, error) {
	subnet = subnet.Masked()
	if !subnet.Addr().Is4() || subnet.Bits() != 24 {
		return netip.Addr{}, fmt.Errorf("tunnel subnet must be an IPv4 /24, got %s", subnet)
	}
	b := derive(master, addrInfoPfx+mac, 1)
	a := subnet.Addr().As4()
	a[3] = byte(2 + int(b[0])%253)
	return netip.AddrFrom4(a), nil
}
