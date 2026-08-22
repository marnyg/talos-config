package masterderive

// Property-based law suite for master derivation (epic
// talos-config-7wg). Laws pinned:
//
//   L3 (invariant 2, "the signature IS the key"): MasterFromSignature
//       is a pure deterministic function of r||s.
//   L4: the recovery byte v never influences the master — signers
//       disagree on its encoding (27/28 vs 0/1), so two wallets
//       signing the same message must still derive the same master.
//   L5: hex/whitespace/0x-prefix presentation never changes the key.

import (
	"bytes"
	"encoding/hex"
	"testing"

	"pgregory.net/rapid"
)

func genRS() *rapid.Generator[[]byte] {
	return rapid.SliceOfN(rapid.Byte(), 64, 64)
}

// TestPropMasterDeterministic: L3.
func TestPropMasterDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		sig := append(genRS().Draw(rt, "rs"), rapid.Byte().Draw(rt, "v"))
		a, errA := MasterFromSignature(sig)
		b, errB := MasterFromSignature(sig)
		if errA != nil || errB != nil {
			rt.Fatalf("unexpected error: %v / %v", errA, errB)
		}
		if !bytes.Equal(a, b) {
			rt.Fatalf("nondeterministic master: %x vs %x", a, b)
		}
	})
}

// TestPropMasterIgnoresV: L4.
func TestPropMasterIgnoresV(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		rs := genRS().Draw(rt, "rs")
		v1 := rapid.Byte().Draw(rt, "v1")
		v2 := rapid.Byte().Draw(rt, "v2")
		a, err := MasterFromSignature(append(append([]byte{}, rs...), v1))
		if err != nil {
			rt.Fatal(err)
		}
		b, err := MasterFromSignature(append(append([]byte{}, rs...), v2))
		if err != nil {
			rt.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			rt.Fatalf("recovery byte leaked into master: v=%d -> %x, v=%d -> %x", v1, a, v2, b)
		}
	})
}

// TestPropMasterHexPresentation: L5.
func TestPropMasterHexPresentation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		sig := append(genRS().Draw(rt, "rs"), rapid.Byte().Draw(rt, "v"))
		want, err := MasterFromSignature(sig)
		if err != nil {
			rt.Fatal(err)
		}
		h := hex.EncodeToString(sig)
		for _, form := range []string{h, "0x" + h, "  " + h + "\n", "\t0x" + h + " "} {
			got, err := MasterFromSignatureHex(form)
			if err != nil {
				rt.Fatalf("form %q rejected: %v", form, err)
			}
			if !bytes.Equal(got, want) {
				rt.Fatalf("form %q derived %x, want %x", form, got, want)
			}
		}
	})
}

// TestPropDistinctSignaturesDistinctMasters: collision smoke — two
// different r||s values must not map to the same master (HKDF would
// have to collide; if this ever fires, the KDF wiring is broken).
func TestPropDistinctSignaturesDistinctMasters(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		rs1 := genRS().Draw(rt, "rs1")
		rs2 := genRS().Draw(rt, "rs2")
		if bytes.Equal(rs1, rs2) {
			rt.Skip("generated identical signatures")
		}
		a, _ := MasterFromSignature(append(append([]byte{}, rs1...), 27))
		b, _ := MasterFromSignature(append(append([]byte{}, rs2...), 27))
		if bytes.Equal(a, b) {
			rt.Fatalf("distinct signatures derived identical master %x", a)
		}
	})
}
