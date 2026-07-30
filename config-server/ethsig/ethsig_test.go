package ethsig

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// wellKnownAddr is the Ethereum address of private key 0x...01 — an
// independent vector for keccak + address derivation.
const wellKnownAddr = "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"

func testKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	b := make([]byte, 32)
	b[31] = 1
	return secp256k1.PrivKeyFromBytes(b)
}

// personalSign produces an Ethereum personal_sign signature (r||s||v hex).
func personalSign(t *testing.T, priv *secp256k1.PrivateKey, message string) string {
	t.Helper()
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := Keccak256([]byte(prefixed))
	compact := secpecdsa.SignCompact(priv, hash, false) // [v+27 || r || s]
	sig := make([]byte, 65)
	copy(sig[:64], compact[1:])
	sig[64] = compact[0] // keep 27/28; RecoverPersonalSign handles both
	return "0x" + hex.EncodeToString(sig)
}

func TestRecoverPersonalSignKnownKey(t *testing.T) {
	priv := testKey(t)
	msg := "talos config-server machine approval\naction: approve\nuser_code: AAAA-BBBB\nnonce: d34db33f"

	addr, err := RecoverPersonalSign(msg, personalSign(t, priv, msg))
	if err != nil {
		t.Fatal(err)
	}
	if addr != wellKnownAddr {
		t.Fatalf("recovered %s, want %s", addr, wellKnownAddr)
	}

	// A different message must not recover to the same address.
	other, err := RecoverPersonalSign("tampered", personalSign(t, priv, msg))
	if err == nil && other == wellKnownAddr {
		t.Fatal("tampered message recovered to the signing address")
	}
}

func TestRecoverPersonalSignMalformed(t *testing.T) {
	for _, sig := range []string{"", "0x", "0xdeadbeef", "not-hex"} {
		if _, err := RecoverPersonalSign("msg", sig); err == nil {
			t.Fatalf("expected error for signature %q", sig)
		}
	}
}

func TestNormalizeAddress(t *testing.T) {
	got, err := NormalizeAddress(" 0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf ")
	if err != nil || got != wellKnownAddr {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "0x123", "7e5f4552091a69125d5dfcb7b8c2659029395bdf", "0xzz5f4552091a69125d5dfcb7b8c2659029395bdf"} {
		if _, err := NormalizeAddress(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
