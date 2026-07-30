package masterderive

import (
	"encoding/hex"
	"testing"
)

const testMaster = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
const testMAC = "b0:41:6f:15:3b:8f"

func ageIdentityForTest(master []byte) string  { id, _ := AgeIdentity(master); return id }
func ageRecipientForTest(master []byte) string { _, r := AgeIdentity(master); return r }

// TestDerivationStability pins the derivation contract. If this test
// breaks, the change RE-KEYS THE ENTIRE FLEET: sealed disk keys stop
// unsealing, recovery passphrases stop matching, and every .age file
// encrypted to the wallet-derived recipient is orphaned. Do not update
// the expected values casually.
//
// The expected values predate the phase-2 wg0 removal on purpose: the
// wg-era info strings are frozen (see the package comment), and this
// test is what makes an accidental "cleanup" of them loud.
func TestDerivationStability(t *testing.T) {
	master, err := MasterFromHex(testMaster)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{
		// KMS: uppercase input UUID must normalize to the same key.
		"kms seal key": hex.EncodeToString(KMSSealKey(master, "8C0D9A51-6E23-4BA1-A1D7-2D5D4C6B0F00")),
		"recovery":     RecoveryPassphrase(master, testMAC),
		"age id":       ageIdentityForTest(master),
		"age recip":    ageRecipientForTest(master),
	}
	want := map[string]string{
		"kms seal key": "a4925a58234469eedf9b8e8a76381683fd07adecdb1daa36c93be65617189121",
		"recovery":     "bhgafhpz-i5qbzl5j-csjuuhan-hxvacurb",
		// Changing these orphans every .age file encrypted to the
		// wallet-derived recipient.
		"age id":    "AGE-SECRET-KEY-1SPNS4ULQSAMVET6FS5NYRAJYQ09P75PT35AM062D52UL8YSMM9XQTSXA77",
		"age recip": "age19ftlkvzz0tseq0zayuzlwdnz3z99m7ewzfs45epcfd0am5l0kfhs0hschn",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s changed: got %s, want %s — this re-keys the fleet", k, got[k], w)
		}
	}
}

// TestMasterFromSignatureStability pins the signature→master KDF. Same
// fleet-re-keying warning as TestDerivationStability.
func TestMasterFromSignatureStability(t *testing.T) {
	sig := make([]byte, 65)
	for i := range sig {
		sig[i] = byte(i)
	}
	master, err := MasterFromSignature(sig)
	if err != nil {
		t.Fatal(err)
	}
	const want = "81de8479284e7b63ada15f449be3e3d9413180b52e70fee002ac1395203aca05"
	if got := hex.EncodeToString(master); got != want {
		t.Errorf("master changed: got %s, want %s — this re-keys the fleet", got, want)
	}
}

// TestMasterFromSignatureVInvariance: wallets encode the recovery byte
// differently (27/28 vs 0/1); it must not affect the master.
func TestMasterFromSignatureVInvariance(t *testing.T) {
	sigA := make([]byte, 65)
	sigB := make([]byte, 65)
	for i := range sigA {
		sigA[i] = byte(i + 1)
		sigB[i] = byte(i + 1)
	}
	sigA[64] = 27
	sigB[64] = 0
	mA, _ := MasterFromSignature(sigA)
	mB, _ := MasterFromSignature(sigB)
	if string(mA) != string(mB) {
		t.Error("recovery byte changed the derived master")
	}

	if _, err := MasterFromSignature(sigA[:64]); err == nil {
		t.Error("expected error for 64-byte signature")
	}
	if _, err := MasterFromSignatureHex("0xzz"); err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestMasterFromHex(t *testing.T) {
	for _, bad := range []string{"", "abcd", "zz", testMaster + "00"} {
		if _, err := MasterFromHex(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
