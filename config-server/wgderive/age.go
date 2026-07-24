package wgderive

// Wallet-derived age identity: the fleet master (itself derived from
// the admin wallet's unseal signature) yields an X25519 age identity
// via its own frozen HKDF domain. The server decrypts the cluster
// secrets at unseal time with this identity — no AGE_KEY secret at
// rest anywhere. The public recipient is committed to the repo
// (talos/age-recipient.txt) and used by the encrypt tooling; the ssh
// key stays as a second recipient for break-glass.

import "strings"

// ageInfo is the derivation domain for the age identity scalar.
// FROZEN — changing it orphans every .age file encrypted to the
// derived recipient.
const ageInfo = "talos-config/age/v1/identity"

// AgeIdentity derives the age X25519 identity ("AGE-SECRET-KEY-1...")
// and its public recipient ("age1..."). The scalar is clamped before
// encoding so identity bytes and recipient stay consistent regardless
// of the consumer's clamping behavior (clamping is idempotent).
func AgeIdentity(master []byte) (identity, recipient string) {
	scalar := clamp(derive(master, ageInfo, 32))
	identity = strings.ToUpper(bech32Encode("age-secret-key-", scalar[:]))
	pub := PublicKey(scalar)
	recipient = bech32Encode("age", pub[:])
	return identity, recipient
}

// bech32 (BIP-173) encoding, encode-only — the age formats are bech32
// with HRPs "age-secret-key-" (identity, uppercased whole) and "age"
// (recipient). Hand-rolled to keep this package stdlib-only; the
// config-server tests round-trip the output through filippo.io/age's
// parser, so a bad encoding cannot ship silently.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var bech32Generator = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

func bech32Polymod(values []byte) uint32 {
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if (top>>uint(i))&1 == 1 {
				chk ^= bech32Generator[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	ret := make([]byte, 0, 2*len(hrp)+1)
	for i := range len(hrp) {
		ret = append(ret, hrp[i]>>5)
	}
	ret = append(ret, 0)
	for i := range len(hrp) {
		ret = append(ret, hrp[i]&31)
	}
	return ret
}

func bech32Checksum(hrp string, data []byte) []byte {
	values := append(bech32HRPExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	mod := bech32Polymod(values) ^ 1
	ret := make([]byte, 6)
	for p := range ret {
		ret[p] = byte((mod >> uint(5*(5-p))) & 31)
	}
	return ret
}

// bech32Encode encodes data (8-bit bytes, converted to 5-bit groups
// with padding) under the given lowercase HRP.
func bech32Encode(hrp string, data []byte) string {
	// convert 8-bit → 5-bit, padded
	var five []byte
	var acc, bits uint
	for _, b := range data {
		acc = acc<<8 | uint(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			five = append(five, byte(acc>>bits)&31)
		}
	}
	if bits > 0 {
		five = append(five, byte(acc<<(5-bits))&31)
	}

	var sb strings.Builder
	sb.WriteString(hrp)
	sb.WriteByte('1')
	for _, p := range five {
		sb.WriteByte(bech32Charset[p])
	}
	for _, p := range bech32Checksum(hrp, five) {
		sb.WriteByte(bech32Charset[p])
	}
	return sb.String()
}
