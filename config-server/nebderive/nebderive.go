// Package nebderive deterministically derives the entire nebula mesh —
// CA, per-machine, per-device and hub identities, plus overlay
// addresses — from the same 32-byte HKDF master that wgderive uses.
// It mirrors wgderive: no peer registry, no cert database, no key
// state outside the master secret. Given the master, the whole mesh is
// recomputable offline (invariant 1: membership is a pure function of
// git + wallet).
//
// Two things make that literally true rather than approximately true:
//
//  1. The CA certificate is byte-stable. Its validity window is a
//     frozen constant (not "now") and Ed25519 signing is deterministic
//     per RFC 8032, so re-deriving the CA yields identical bytes and
//     therefore an identical fingerprint. This matters because every
//     leaf certificate records the CA fingerprint as its issuer, and
//     clients pin ca.crt: a CA that drifted on each derivation would
//     invalidate every cert ever issued.
//
//  2. Leaf identities (key, name, overlay address) are byte-stable, but
//     leaf *certificates* are not required to be — they carry a caller
//     supplied validity window so that short-lived certs remain
//     available as a revocation strategy. Re-minting a leaf never
//     changes who it is.
//
// The info strings below are part of the derivation contract: changing
// them (or the master key) re-keys the entire mesh.
package nebderive

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/slackhq/nebula/cert"
	"golang.org/x/crypto/curve25519"
)

// Derivation domains — frozen, see package comment. Separate domains
// per role mean a device name can never collide with a machine's key
// material, and keys never collide with addresses.
const (
	caInfo         = "talos-config/nebula/v1/ca"
	hubInfo        = "talos-config/nebula/v1/hub"
	machineInfoPfx = "talos-config/nebula/v1/machine/"      // + normalized MAC
	deviceInfoPfx  = "talos-config/nebula/v1/device/"       // + normalized device name
	machineAddrPfx = "talos-config/nebula/v1/machine-addr/" // + normalized MAC
	deviceAddrPfx  = "talos-config/nebula/v1/device-addr/"  // + normalized device name
)

// CertVersion is the nebula certificate format this package mints.
// Pinned to V2: the Sidero nebula extension ships nebula 1.10.3,
// Mobile Nebula and the hub embed 1.11.0, and nebula-cert 1.10.3
// already defaults to version 2. Nothing in the toolchain predates
// 1.10, so V1 would be the deviation. V2 also unlocks IPv6 and
// multiple overlay addresses per cert, which V1 cannot express.
const CertVersion = cert.Version2

// Curve is the only curve this package uses: Ed25519 for the CA's
// signatures, X25519 for host Noise handshakes. cert.Curve_CURVE25519
// names that pair.
const Curve = cert.Curve_CURVE25519

// CA validity — FROZEN. These constants are load-bearing: they are
// what makes the CA certificate byte-stable across derivations (see
// package comment). Changing either one mints a different CA, which
// re-roots the mesh and invalidates every issued certificate.
//
// The window is deliberately long. There is no CRL in nebula, so CA
// rotation is a fleet-wide re-enrollment either way; a short CA
// lifetime would buy nothing and add a cliff.
var (
	CANotBefore = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	CANotAfter  = time.Date(2046, time.January, 1, 0, 0, 0, 0, time.UTC)
)

// CAName is the human-readable CA name baked into the CA certificate.
// Part of the byte-stability contract — changing it re-roots the mesh.
const CAName = "talos-config"

// HubName is the reserved nebula name of the hub (lighthouse + relay).
const HubName = "hub"

// DNSZone is the on-mesh DNS zone. `.internal` is ICANN-reserved for
// private use, so it can never collide with a real delegation — unlike
// an ENS name, which Brave and MetaMask intercept in the URL bar on
// exactly the admin devices meant to use the mesh. It lives here rather
// than beside the hub's DNS server so mesh clients (cmd/nebup) share
// the constant without importing the server.
const DNSZone = "mesh.internal"

func derive(master []byte, info string, n int) []byte {
	out, err := hkdf.Key(sha256.New, master, nil, info, n)
	if err != nil {
		panic(err) // only fails on invalid hash/length parameters
	}
	return out
}

// Normalize lowercases and trims a machine MAC or device name. Part of
// the derivation contract: the hub and the offline tooling must agree
// on the form or they derive different identities.
func Normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// clamp applies standard Curve25519 private-key clamping.
func clamp(b []byte) [32]byte {
	var k [32]byte
	copy(k[:], b)
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k
}

// CAKey derives the mesh CA's Ed25519 private key. This key signs
// every certificate in the mesh; it is the wallet-derived issuer key
// that invariant 1 permits.
func CAKey(master []byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(derive(master, caInfo, ed25519.SeedSize))
}

// HostKey derives an X25519 keypair for one of the host roles. Callers
// should prefer HubKey, MachineKey or DeviceKey.
func hostKey(master []byte, info string) (priv, pub [32]byte) {
	priv = clamp(derive(master, info, 32))
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		panic(err) // clamped keys cannot be low-order
	}
	copy(pub[:], p)
	return priv, pub
}

// HubKey derives the hub's (lighthouse + relay) X25519 keypair.
func HubKey(master []byte) (priv, pub [32]byte) {
	return hostKey(master, hubInfo)
}

// MachineKey derives the X25519 keypair for the Talos node with the
// given MAC. Minted into the node's config at compose time, the same
// trust chain as today's wg0 key injection.
func MachineKey(master []byte, mac string) (priv, pub [32]byte) {
	return hostKey(master, machineInfoPfx+Normalize(mac))
}

// DeviceKey derives the X25519 keypair for a named admin/appliance
// device (laptop, phone, androidtv). Re-running enrollment after a
// device wipe re-derives the same identity.
func DeviceKey(master []byte, name string) (priv, pub [32]byte) {
	return hostKey(master, deviceInfoPfx+Normalize(name))
}

// HubIP is the hub's overlay address: always the first host in the
// mesh subnet. Reserved, never handed to a derived peer, so that
// static_host_map entries and the DNS resolver address are knowable
// from the subnet alone.
func HubIP(subnet netip.Prefix) (netip.Addr, error) {
	if err := checkSubnet(subnet); err != nil {
		return netip.Addr{}, err
	}
	return subnet.Masked().Addr().Next(), nil
}

// MachineIP deterministically assigns the machine an overlay address in
// subnet. Collisions between peers are possible (see hostIP) and must
// be checked by the caller, which can also override the derived value.
func MachineIP(master []byte, mac string, subnet netip.Prefix) (netip.Addr, error) {
	return hostIP(master, machineAddrPfx+Normalize(mac), subnet)
}

// DeviceIP deterministically assigns a named device an overlay address
// in subnet, from its own derivation domain.
func DeviceIP(master []byte, name string, subnet netip.Prefix) (netip.Addr, error) {
	return hostIP(master, deviceAddrPfx+Normalize(name), subnet)
}

func checkSubnet(subnet netip.Prefix) error {
	if !subnet.IsValid() || !subnet.Addr().Is4() {
		return fmt.Errorf("mesh subnet must be a valid IPv4 prefix, got %s", subnet)
	}
	// /30 is the smallest prefix with a usable host after reserving the
	// network address, the hub, and the broadcast address.
	if subnet.Bits() < 8 || subnet.Bits() > 30 {
		return fmt.Errorf("mesh subnet must be between /8 and /30, got %s", subnet)
	}
	return nil
}

// hostIP derives a host address inside subnet, skipping the network
// address, the hub (first host) and the broadcast address.
//
// Unlike wgderive's /24-only single-byte version, this takes the prefix
// width as a parameter and derives across the whole host space. That is
// what makes a roomy mesh subnet worth choosing: certificates bake the
// overlay address, so a collision is expensive to resolve, and the
// birthday bound over a /24's 253 slots reaches ~50% at only ~19 peers.
// At /16 the same 19 peers collide with probability ~0.3%.
//
// Collisions remain possible in principle, so the caller must still
// check derived addresses against each other.
func hostIP(master []byte, info string, subnet netip.Prefix) (netip.Addr, error) {
	if err := checkSubnet(subnet); err != nil {
		return netip.Addr{}, err
	}
	subnet = subnet.Masked()

	hostBits := 32 - subnet.Bits()
	span := uint32(1) << hostBits // total addresses in the subnet
	usable := span - 3            // minus network, hub, broadcast

	// 4 bytes of keystream reduced modulo the host space. The modulo
	// bias is bounded by usable/2^32 and is irrelevant here.
	offset := 2 + binary.BigEndian.Uint32(derive(master, info, 4))%usable

	base4 := subnet.Addr().As4()
	base := binary.BigEndian.Uint32(base4[:])
	var a [4]byte
	binary.BigEndian.PutUint32(a[:], base+offset)
	return netip.AddrFrom4(a), nil
}

// CACert mints the mesh CA certificate: self-signed, byte-stable, and
// deliberately *unconstrained* in networks.
//
// Nebula lets a CA restrict which overlay networks its subordinate
// certs may claim. We omit that on purpose. Baking the mesh CIDR into
// the CA would make the CIDR as permanent as the trust root — a later
// renumber would force a new CA and a full re-enrollment. With the
// constraint omitted, a renumber only re-mints leaves. The constraint
// would only defend against a leaked CA key issuing off-subnet certs,
// and a leaked CA key means re-rooting regardless, so the trade is
// one-sided.
func CACert(master []byte) (cert.Certificate, error) {
	key := CAKey(master)
	tbs := &cert.TBSCertificate{
		Version:   CertVersion,
		Name:      CAName,
		IsCA:      true,
		NotBefore: CANotBefore,
		NotAfter:  CANotAfter,
		PublicKey: key.Public().(ed25519.PublicKey),
		Curve:     Curve,
	}
	c, err := tbs.Sign(nil, Curve, key)
	if err != nil {
		return nil, fmt.Errorf("signing mesh CA: %w", err)
	}
	return c, nil
}

// HostCert mints a leaf certificate for name at addr, signed by the
// derived CA. groups feed nebula firewall rules; notBefore/notAfter are
// caller-supplied so short-lived certs stay available as a revocation
// strategy (nebula has no CRL).
//
// The certificate's network is addr with the *subnet's* prefix length,
// not /32: nebula uses that to size the overlay route, so a /32 would
// leave the host unable to reach the rest of the mesh.
func HostCert(master []byte, name string, pub [32]byte, addr netip.Addr, subnet netip.Prefix, groups []string, notBefore, notAfter time.Time) (cert.Certificate, error) {
	if err := checkSubnet(subnet); err != nil {
		return nil, err
	}
	if !subnet.Contains(addr) {
		return nil, fmt.Errorf("address %s is outside mesh subnet %s", addr, subnet)
	}
	ca, err := CACert(master)
	if err != nil {
		return nil, err
	}
	tbs := &cert.TBSCertificate{
		Version:   CertVersion,
		Name:      name,
		Networks:  []netip.Prefix{netip.PrefixFrom(addr, subnet.Bits())},
		Groups:    groups,
		NotBefore: notBefore,
		NotAfter:  notAfter,
		PublicKey: pub[:],
		Curve:     Curve,
	}
	c, err := tbs.Sign(ca, Curve, CAKey(master))
	if err != nil {
		return nil, fmt.Errorf("signing cert for %q: %w", name, err)
	}
	return c, nil
}

// CACertPEM renders the CA certificate in the on-disk PEM form that
// every mesh member pins as its ca.crt.
func CACertPEM(master []byte) ([]byte, error) {
	c, err := CACert(master)
	if err != nil {
		return nil, err
	}
	return c.MarshalPEM()
}

// CAKeyPEM renders the CA signing key in nebula's on-disk PEM form.
// This is the mesh's root secret: it exists in hub memory only and must
// never be written to git, an image, or a registry (invariant 8).
func CAKeyPEM(master []byte) []byte {
	return cert.MarshalSigningPrivateKeyToPEM(Curve, CAKey(master))
}

// HostKeyPEM renders a host's X25519 private key in nebula's on-disk
// PEM form.
func HostKeyPEM(priv [32]byte) []byte {
	return cert.MarshalPrivateKeyToPEM(Curve, priv[:])
}

// CAFingerprint is the CA certificate's SHA-256 fingerprint: the value
// every leaf records as its issuer, and the value the ENS commitment
// layer would publish for out-of-band verification.
func CAFingerprint(master []byte) (string, error) {
	c, err := CACert(master)
	if err != nil {
		return "", err
	}
	return c.Fingerprint()
}
