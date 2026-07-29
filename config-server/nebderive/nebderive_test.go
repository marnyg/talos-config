package nebderive

import (
	"bytes"
	"crypto/ed25519"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/slackhq/nebula/cert"
)

// testMaster is a fixed master secret so the golden values below are
// stable. It is not a real key.
var testMaster = bytes.Repeat([]byte{0x2a}, 32)

// meshSubnet is the decided overlay CIDR (see the +decision task).
var meshSubnet = netip.MustParsePrefix("10.42.0.0/16")

// TestGoldenIdentities pins the derivation contract. These values are
// baked into certificates that live on nodes and phones, so a change
// here silently re-keys or renumbers the mesh — if this test fails, the
// contract moved and every member needs re-enrollment.
func TestGoldenIdentities(t *testing.T) {
	// The CA fingerprint is every leaf's issuer field and the value the
	// ENS commitment layer would publish. If it moves, the mesh re-roots.
	const wantFP = "ef266d0540567b38af541569df5a25bf0ddea038415d34cc7b0c12b6b4269783"
	fp, err := CAFingerprint(testMaster)
	if err != nil {
		t.Fatal(err)
	}
	if fp != wantFP {
		t.Errorf("CA fingerprint changed: got %s, want %s — this re-roots the mesh", fp, wantFP)
	}

	hubIP, err := HubIP(meshSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if hubIP.String() != "10.42.0.1" {
		t.Errorf("hub ip: got %s, want 10.42.0.1", hubIP)
	}

	// Golden addresses: changing these renumbers the mesh.
	for _, tc := range []struct {
		kind, id, want string
	}{
		{"machine", "b0:41:6f:15:3b:8f", "10.42.131.58"},
		{"device", "laptop", "10.42.212.127"},
		{"device", "androidtv", "10.42.142.11"},
	} {
		var got netip.Addr
		var err error
		if tc.kind == "machine" {
			got, err = MachineIP(testMaster, tc.id, meshSubnet)
		} else {
			got, err = DeviceIP(testMaster, tc.id, meshSubnet)
		}
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != tc.want {
			t.Errorf("%s %s ip changed: got %s, want %s — this renumbers the mesh",
				tc.kind, tc.id, got, tc.want)
		}
	}
}

// TestCACertIsByteStable is the load-bearing determinism test: leaves
// record the CA fingerprint as their issuer and clients pin ca.crt, so
// a CA that drifted per derivation would invalidate the whole mesh.
func TestCACertIsByteStable(t *testing.T) {
	a, err := CACertPEM(testMaster)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CACertPEM(testMaster)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("CA certificate is not byte-stable across derivations")
	}
	if !bytes.Equal(CAKeyPEM(testMaster), CAKeyPEM(testMaster)) {
		t.Fatal("CA key is not stable across derivations")
	}

	c, _, err := cert.UnmarshalCertificateFromPEM(a)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version() != cert.Version2 {
		t.Errorf("CA cert version: got %d, want 2", c.Version())
	}
	if !c.IsCA() {
		t.Error("CA cert does not have IsCA set")
	}
	if len(c.Networks()) != 0 {
		t.Errorf("CA cert should be network-unconstrained, got %v", c.Networks())
	}
	if !c.NotBefore().Equal(CANotBefore) || !c.NotAfter().Equal(CANotAfter) {
		t.Errorf("CA validity drifted: %s..%s", c.NotBefore(), c.NotAfter())
	}
}

// TestDerivedKeysAreDistinct guards the separate-HKDF-domain property: a
// device named like a MAC must not collide with that machine's key.
func TestDerivedKeysAreDistinct(t *testing.T) {
	const id = "b0:41:6f:15:3b:8f"
	mPriv, _ := MachineKey(testMaster, id)
	dPriv, _ := DeviceKey(testMaster, id)
	hPriv, _ := HubKey(testMaster)

	if mPriv == dPriv {
		t.Error("machine and device keys collide for the same identifier")
	}
	if mPriv == hPriv || dPriv == hPriv {
		t.Error("hub key collides with a host key")
	}
	if bytes.Equal(mPriv[:], CAKey(testMaster).Seed()) {
		t.Error("host key collides with the CA seed")
	}

	// Normalization is part of the contract.
	up, _ := MachineKey(testMaster, "  B0:41:6F:15:3B:8F ")
	if up != mPriv {
		t.Error("MAC normalization is not applied to key derivation")
	}
	upIP, err := DeviceIP(testMaster, " LAPTOP ", meshSubnet)
	if err != nil {
		t.Fatal(err)
	}
	loIP, err := DeviceIP(testMaster, "laptop", meshSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if upIP != loIP {
		t.Error("name normalization is not applied to address derivation")
	}
}

// TestVerifyInProcess checks leaves against the derived CA using
// nebula's own CAPool — the same code path a peer runs at handshake.
func TestVerifyInProcess(t *testing.T) {
	caPEM, err := CACertPEM(testMaster)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := cert.NewCAPoolFromPEM(caPEM)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	_, pub := DeviceKey(testMaster, "laptop")
	addr, err := DeviceIP(testMaster, "laptop", meshSubnet)
	if err != nil {
		t.Fatal(err)
	}
	c, err := HostCert(testMaster, "laptop", pub, addr, meshSubnet,
		[]string{"admin"}, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.VerifyCertificate(now, c); err != nil {
		t.Fatalf("derived cert does not verify against derived CA: %v", err)
	}

	// The cert must carry the subnet prefix length, not /32, or the
	// host cannot route to the rest of the mesh.
	if got := c.Networks(); len(got) != 1 || got[0].Bits() != meshSubnet.Bits() {
		t.Errorf("cert networks: got %v, want one /%d", got, meshSubnet.Bits())
	}

	// The private key must pair with the cert's public key.
	priv, _ := DeviceKey(testMaster, "laptop")
	if err := c.VerifyPrivateKey(Curve, priv[:]); err != nil {
		t.Errorf("derived key does not pair with derived cert: %v", err)
	}

	// A cert signed by a different master must not verify.
	other := bytes.Repeat([]byte{0x99}, 32)
	_, otherPub := DeviceKey(other, "laptop")
	rogue, err := HostCert(other, "laptop", otherPub, addr, meshSubnet, nil, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.VerifyCertificate(now, rogue); err == nil {
		t.Error("cert from a foreign CA verified against our pool")
	}
}

func TestHostCertRejectsOffSubnetAddress(t *testing.T) {
	_, pub := DeviceKey(testMaster, "laptop")
	now := time.Now()
	_, err := HostCert(testMaster, "laptop", pub, netip.MustParseAddr("10.99.0.7"), meshSubnet, nil, now, now.Add(time.Hour))
	if err == nil {
		t.Fatal("expected rejection of an address outside the mesh subnet")
	}
}

// TestHostIPStaysInUsableRange checks the address derivation never
// hands out the network address, the hub, or the broadcast address, at
// every supported prefix width.
func TestHostIPStaysInUsableRange(t *testing.T) {
	for _, bits := range []int{8, 16, 24, 30} {
		subnet := netip.PrefixFrom(netip.MustParseAddr("10.42.0.0"), bits).Masked()
		network := subnet.Addr()
		hub, err := HubIP(subnet)
		if err != nil {
			t.Fatal(err)
		}
		span := 1 << (32 - bits)
		last := network
		for i := 0; i < span-1; i++ {
			last = last.Next()
		}
		for i := 0; i < 500; i++ {
			addr, err := DeviceIP(testMaster, string(rune('a'+i%26))+string(rune('a'+i/26)), subnet)
			if err != nil {
				t.Fatal(err)
			}
			if !subnet.Contains(addr) {
				t.Fatalf("/%d: %s escaped subnet %s", bits, addr, subnet)
			}
			if addr == network || addr == hub || addr == last {
				t.Fatalf("/%d: %s is reserved (network %s, hub %s, broadcast %s)", bits, addr, network, hub, last)
			}
		}
	}
}

func TestSubnetValidation(t *testing.T) {
	for _, s := range []string{"10.42.0.0/31", "10.42.0.0/7", "fd00::/64"} {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DeviceIP(testMaster, "laptop", p); err == nil {
			t.Errorf("%s: expected rejection", s)
		}
	}
}

// TestStockNebulaCertVerify is the golden interop test: the certs we
// mint in-process must satisfy the stock `nebula-cert verify` binary,
// which is what the Talos extension and the mobile apps embed. Skips
// when nebula-cert is not on PATH.
func TestStockNebulaCertVerify(t *testing.T) {
	bin, err := exec.LookPath("nebula-cert")
	if err != nil {
		t.Skip("nebula-cert not on PATH; run in the nix devshell")
	}

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	caPEM, err := CACertPEM(testMaster)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	type hostCase struct {
		name   string
		pub    [32]byte
		addr   netip.Addr
		groups []string
	}

	must := func(a netip.Addr, err error) netip.Addr {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	const mac = "b0:41:6f:15:3b:8f"
	_, hubPub := HubKey(testMaster)
	_, machinePub := MachineKey(testMaster, mac)
	_, devicePub := DeviceKey(testMaster, "laptop")

	// One of each role — the three cert shapes that phase 1 actually
	// injects: hub (lighthouse+relay), Talos node, admin device.
	cases := []hostCase{
		{HubName, hubPub, must(HubIP(meshSubnet)), []string{"lighthouse"}},
		{"cp1", machinePub, must(MachineIP(testMaster, mac, meshSubnet)), []string{"node"}},
		{"laptop", devicePub, must(DeviceIP(testMaster, "laptop", meshSubnet)), []string{"admin"}},
	}

	now := time.Now()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := HostCert(testMaster, tc.name, tc.pub, tc.addr, meshSubnet, tc.groups,
				now.Add(-time.Hour), now.Add(24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			pem, err := c.MarshalPEM()
			if err != nil {
				t.Fatal(err)
			}
			crtPath := filepath.Join(dir, tc.name+".crt")
			if err := os.WriteFile(crtPath, pem, 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(bin, "verify", "-ca", caPath, "-crt", crtPath).CombinedOutput()
			if err != nil {
				t.Fatalf("stock nebula-cert verify rejected our cert: %v\n%s", err, out)
			}
			// print also parses with the stock ASN.1 decoder.
			out, err = exec.Command(bin, "print", "-path", crtPath).CombinedOutput()
			if err != nil {
				t.Fatalf("stock nebula-cert print failed: %v\n%s", err, out)
			}
			t.Logf("%s verified by stock nebula-cert:\n%s", tc.name, out)
		})
	}
}

// TestCAKeyPEMRoundTripsThroughStockParser checks the CA key we hand to
// nebula-cert-compatible tooling is in the expected on-disk encoding.
func TestCAKeyPEMRoundTrips(t *testing.T) {
	raw, rest, curve, err := cert.UnmarshalSigningPrivateKeyFromPEM(CAKeyPEM(testMaster))
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Errorf("trailing bytes after CA key PEM: %d", len(rest))
	}
	if curve != Curve {
		t.Errorf("curve: got %v, want %v", curve, Curve)
	}
	if !bytes.Equal(raw, CAKey(testMaster)) {
		t.Error("CA key did not round-trip through PEM")
	}
	if len(raw) != ed25519.PrivateKeySize {
		t.Errorf("CA key size: got %d, want %d", len(raw), ed25519.PrivateKeySize)
	}

	hPriv, _ := HubKey(testMaster)
	hRaw, _, hCurve, err := cert.UnmarshalPrivateKeyFromPEM(HostKeyPEM(hPriv))
	if err != nil {
		t.Fatal(err)
	}
	if hCurve != Curve || !bytes.Equal(hRaw, hPriv[:]) {
		t.Error("host key did not round-trip through PEM")
	}
}
