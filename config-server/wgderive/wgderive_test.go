package wgderive

import (
	"encoding/hex"
	"net/netip"
	"testing"
)

const testMaster = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
const testMAC = "b0:41:6f:15:3b:8f"

func ageIdentityForTest(master []byte) string  { id, _ := AgeIdentity(master); return id }
func ageRecipientForTest(master []byte) string { _, r := AgeIdentity(master); return r }

// TestDerivationStability pins the derivation contract. If this test
// breaks, the change RE-KEYS THE ENTIRE FLEET: every provisioned
// machine's tunnel config becomes invalid until re-provisioned. Do not
// update the expected values casually.
func TestDerivationStability(t *testing.T) {
	master, err := MasterFromHex(testMaster)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{
		"server priv":  KeyHex(ServerKey(master)),
		"server pub":   KeyHex(PublicKey(ServerKey(master))),
		"machine priv": KeyHex(MachineKey(master, testMAC)),
		"machine pub":  KeyHex(PublicKey(MachineKey(master, testMAC))),
		"machine b64":  KeyBase64(MachineKey(master, testMAC)),
		// KMS: uppercase input UUID must normalize to the same key.
		"kms seal key": hex.EncodeToString(KMSSealKey(master, "8C0D9A51-6E23-4BA1-A1D7-2D5D4C6B0F00")),
		"recovery":     RecoveryPassphrase(master, testMAC),
		// Admin peers: uppercase input name must normalize to the same key.
		"admin priv": KeyHex(AdminKey(master, "Laptop")),
		"admin pub":  KeyHex(PublicKey(AdminKey(master, "laptop"))),
		"age id":     ageIdentityForTest(master),
		"age recip":  ageRecipientForTest(master),
	}
	want := map[string]string{
		"server priv":  "800b5d44d8d92c6de62e8c25b6d191b1fdbf7dac120ca2582be51c53f4566d78",
		"server pub":   "b9ece5d6e02d55873844538106f17ff106ae4f5e44279f03b5719b88dff84368",
		"machine priv": "605231cd460e5b50262f7985b04f4db8639a256c07e10385a4250ef36e40ce55",
		"machine pub":  "85dd72a62d628075c6e7b4f302acf9d2550da6dba5f63c0154008f9c0ce3b57d",
		"machine b64":  "YFIxzUYOW1AmL3mFsE9NuGOaJWwH4QOFpCUO825AzlU=",
		"kms seal key": "a4925a58234469eedf9b8e8a76381683fd07adecdb1daa36c93be65617189121",
		"recovery":     "bhgafhpz-i5qbzl5j-csjuuhan-hxvacurb",
		"admin priv":   "885a7a32c8bc86908bbf1567bffc637dc7c8aaf6e53ef536d16a487e5a55a660",
		"admin pub":    "595c3842b5e72bad4b19d94ffd664cdddeda6ad61614626c9cd54c2e8f7ef222",
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

	ip, err := TunnelIP(master, testMAC, netip.MustParsePrefix("10.99.0.0/24"))
	if err != nil {
		t.Fatal(err)
	}
	if ip.String() != "10.99.0.16" {
		t.Errorf("tunnel ip changed: got %s, want 10.99.0.16 — this renumbers the fleet", ip)
	}

	adminIP, err := AdminTunnelIP(master, "laptop", netip.MustParsePrefix("10.99.0.0/24"))
	if err != nil {
		t.Fatal(err)
	}
	if adminIP.String() != "10.99.0.79" {
		t.Errorf("admin tunnel ip changed: got %s, want 10.99.0.79 — this renumbers admin peers", adminIP)
	}
}

func TestClamping(t *testing.T) {
	master, _ := MasterFromHex(testMaster)
	for _, k := range [][32]byte{ServerKey(master), MachineKey(master, testMAC)} {
		if k[0]&7 != 0 {
			t.Errorf("low bits not cleared: %x", k[0])
		}
		if k[31]&128 != 0 || k[31]&64 == 0 {
			t.Errorf("high byte not clamped: %x", k[31])
		}
	}
}

func TestDistinctPerMachine(t *testing.T) {
	master, _ := MasterFromHex(testMaster)
	if MachineKey(master, "aa:bb:cc:dd:ee:01") == MachineKey(master, "aa:bb:cc:dd:ee:02") {
		t.Error("different MACs derived the same key")
	}
	if MachineKey(master, testMAC) == ServerKey(master) {
		t.Error("machine key collides with server key")
	}
}

func TestTunnelIPRange(t *testing.T) {
	master, _ := MasterFromHex(testMaster)
	subnet := netip.MustParsePrefix("10.99.0.0/24")
	for _, mac := range []string{testMAC, "aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02", "00:00:00:00:00:00"} {
		ip, err := TunnelIP(master, mac, subnet)
		if err != nil {
			t.Fatal(err)
		}
		last := ip.As4()[3]
		if last < 2 || last > 254 {
			t.Errorf("mac %s: host %d outside [2,254]", mac, last)
		}
		if !subnet.Contains(ip) {
			t.Errorf("mac %s: %s outside subnet", mac, ip)
		}
	}

	if _, err := TunnelIP(master, testMAC, netip.MustParsePrefix("10.99.0.0/16")); err == nil {
		t.Error("expected error for non-/24 subnet")
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
	if got := KeyHex([32]byte(master)); got != want {
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
