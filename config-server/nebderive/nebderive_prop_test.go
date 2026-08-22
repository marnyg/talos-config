package nebderive

// Property-based law suite for identity/address derivation (epic
// talos-config-7wg). Laws pinned:
//
//   L6 (invariant 2): every derivation is a pure function of
//       (master, name/mac, subnet) — re-running it yields the same
//       identity, address and certificate bytes. This is the checkable
//       core of "a wiped node rebuilds from nothing".
//   L7 (docs/desired-state/domain-model.md, addr : Name -> Addr):
//       derivation depends on the *normalized* name only — casing and
//       whitespace never move a member, and the keypair never feeds
//       the address at all (enforced by DeviceIP's signature).
//   L8: Normalize is idempotent — hub and offline tooling converge on
//       one canonical form.
//   L9: derived addresses land inside the subnet and never collide
//       with the reserved slots (network, hub, broadcast).

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func genMaster() *rapid.Generator[[]byte] {
	return rapid.SliceOfN(rapid.Byte(), 32, 32)
}

// genRawName: a plausible device/machine label wrapped in the case and
// whitespace noise Normalize exists to absorb.
func genRawName() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		core := rapid.StringMatching(`[A-Za-z0-9][A-Za-z0-9-]{0,14}`).Draw(t, "core")
		pre := rapid.SampledFrom([]string{"", " ", "  ", "\t"}).Draw(t, "pre")
		post := rapid.SampledFrom([]string{"", " ", "\n", " \t"}).Draw(t, "post")
		return pre + core + post
	})
}

func genMAC() *rapid.Generator[string] {
	return rapid.StringMatching(`([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}`)
}

func genSubnet() *rapid.Generator[netip.Prefix] {
	return rapid.Custom(func(t *rapid.T) netip.Prefix {
		bits := rapid.IntRange(8, 30).Draw(t, "bits")
		raw := rapid.Uint32().Draw(t, "base")
		var a [4]byte
		binary.BigEndian.PutUint32(a[:], raw)
		return netip.PrefixFrom(netip.AddrFrom4(a), bits).Masked()
	})
}

// TestPropNormalizeIdempotent: L8.
func TestPropNormalizeIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := genRawName().Draw(rt, "name")
		if Normalize(Normalize(s)) != Normalize(s) {
			rt.Fatalf("Normalize not idempotent on %q", s)
		}
	})
}

// TestPropAddrDependsOnNormalizedNameOnly: L7. The raw and normalized
// spellings of a name derive the same address and keypair, so
// re-enrolling with different casing never moves a device.
func TestPropAddrDependsOnNormalizedNameOnly(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		master := genMaster().Draw(rt, "master")
		name := genRawName().Draw(rt, "name")
		subnet := genSubnet().Draw(rt, "subnet")

		a1, err1 := DeviceIP(master, name, subnet)
		a2, err2 := DeviceIP(master, Normalize(name), subnet)
		if err1 != nil || err2 != nil {
			rt.Fatalf("DeviceIP errors: %v / %v", err1, err2)
		}
		if a1 != a2 {
			rt.Fatalf("address moved under normalization: %s vs %s (name %q)", a1, a2, name)
		}

		mac := genMAC().Draw(rt, "mac")
		m1, _ := MachineIP(master, mac, subnet)
		m2, _ := MachineIP(master, Normalize(mac), subnet)
		if m1 != m2 {
			rt.Fatalf("machine address moved under normalization: %s vs %s (mac %q)", m1, m2, mac)
		}
		k1priv, k1pub := MachineKey(master, mac)
		k2priv, k2pub := MachineKey(master, Normalize(mac))
		if k1priv != k2priv || k1pub != k2pub {
			rt.Fatalf("machine keypair moved under normalization (mac %q)", mac)
		}
	})
}

// TestPropDerivationDeterministicAndInSubnet: L6 + L9 for addresses.
func TestPropDerivationDeterministicAndInSubnet(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		master := genMaster().Draw(rt, "master")
		mac := genMAC().Draw(rt, "mac")
		subnet := genSubnet().Draw(rt, "subnet")

		ip1, err := MachineIP(master, mac, subnet)
		if err != nil {
			rt.Fatalf("MachineIP: %v", err)
		}
		ip2, _ := MachineIP(master, mac, subnet)
		if ip1 != ip2 {
			rt.Fatalf("MachineIP nondeterministic: %s vs %s", ip1, ip2)
		}
		if !subnet.Contains(ip1) {
			rt.Fatalf("derived address %s outside subnet %s", ip1, subnet)
		}

		hub, err := HubIP(subnet)
		if err != nil {
			rt.Fatalf("HubIP: %v", err)
		}
		network := subnet.Masked().Addr()
		span := uint32(1) << (32 - subnet.Bits())
		n4 := network.As4()
		var b4 [4]byte
		binary.BigEndian.PutUint32(b4[:], binary.BigEndian.Uint32(n4[:])+span-1)
		broadcast := netip.AddrFrom4(b4)

		for _, reserved := range []netip.Addr{network, hub, broadcast} {
			if ip1 == reserved {
				rt.Fatalf("derived address %s collides with reserved slot in %s", ip1, subnet)
			}
		}
	})
}

// TestPropCertBytesRederivable: L6 at the certificate level. Minting
// with identical inputs yields byte-identical certs (ed25519 signing
// is deterministic), so a redeployed hub re-derives, not re-issues.
func TestPropCertBytesRederivable(t *testing.T) {
	notBefore := CANotBefore.Add(24 * time.Hour) // inside the CA window
	notAfter := notBefore.Add(90 * 24 * time.Hour)
	rapid.Check(t, func(rt *rapid.T) {
		master := genMaster().Draw(rt, "master")
		name := Normalize(genRawName().Draw(rt, "name"))
		subnet := genSubnet().Draw(rt, "subnet")

		caFP1, err := CAFingerprint(master)
		if err != nil {
			rt.Fatalf("CAFingerprint: %v", err)
		}
		caFP2, _ := CAFingerprint(master)
		if caFP1 != caFP2 {
			rt.Fatalf("CA not byte-stable: %s vs %s", caFP1, caFP2)
		}

		addr, err := DeviceIP(master, name, subnet)
		if err != nil {
			rt.Fatalf("DeviceIP: %v", err)
		}
		_, pub := HubKey(master) // any derived pubkey works as leaf key material
		groups := []string{"admins"}

		c1, err := HostCert(master, name, pub, addr, subnet, groups, notBefore, notAfter)
		if err != nil {
			rt.Fatalf("HostCert: %v", err)
		}
		c2, err := HostCert(master, name, pub, addr, subnet, groups, notBefore, notAfter)
		if err != nil {
			rt.Fatalf("HostCert (second mint): %v", err)
		}
		fp1, _ := c1.Fingerprint()
		fp2, _ := c2.Fingerprint()
		if fp1 != fp2 {
			rt.Fatalf("leaf cert not byte-stable: %s vs %s", fp1, fp2)
		}
	})
}
