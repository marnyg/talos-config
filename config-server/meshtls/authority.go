// Package meshtls is the device-local HTTPS presentation of the
// identity plane (ADR-0033, talos-config-eu0m): the daemon that already
// fakes DNS and IPs for *.mesh.internal also fakes the TLS server. A
// flow to <fake IP>:443 is terminated here with a leaf minted for the
// name, and the request goes on as plain HTTP over the identity stream
// exactly as a flow to :80 does, plus X-Forwarded-Proto: https so the
// app sees the origin the browser sees.
//
// The root that signs the leaves is a per-device presentation artifact,
// never authority: it is minted on the device, never in transit, name-
// constrained to the zone, and it asserts nothing to anyone but this
// device's own browsers — and only after the daemon has resolved the
// name from the plane's name map and dialed the member by key. Losing
// it costs one re-trust click. The protocol gains no X.509; no mesh CA
// exists (ADR-0018: the seed never signs; ADR-0033 rules out every
// cluster-side root). http:// keeps being served beside it for clients
// that cannot trust a user CA (appliances, Android apps on API ≥ 24).
//
// Browser facts the shape rests on (verified in source 2026-10-18, see
// docs/spikes/tls-over-mesh.md): Safari/trustd and Chrome on macOS and
// Firefox everywhere enforce nameConstraints on a locally trusted root;
// Chrome on Android (TrustStoreAndroid → AddTrustAnchor, no anchor
// constraints) does NOT, so there the root's reach is bounded by the
// device's custody alone, the same radius as its member key.
package meshtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Zone is the only DNS subtree the root may sign for, and so the only
// suffix Leaf accepts. The constraint is spelled without a leading dot
// (RFC 5280: the domain and every subdomain), the form every verifier
// reads the same way.
const Zone = "mesh.internal"

// Files under the authority's directory.
const (
	KeyFile  = "ca.key" // PKCS#8 PEM, 0600: the presentation key
	CertFile = "ca.crt" // the root, PEM, 0644: what the trust store wants
)

// Validity of what the authority mints.
const (
	rootValidity = 10 * 365 * 24 * time.Hour
	leafValidity = 30 * 24 * time.Hour
	// leafRenewBefore: a cached leaf is re-minted this close to expiry,
	// so a long-running daemon never serves a stale one.
	leafRenewBefore = 24 * time.Hour
	// backdate absorbs a client clock a little behind ours.
	backdate = time.Hour
)

// Authority is the per-device root plus its leaf cache.
type Authority struct {
	dir  string
	root *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
	now    func() time.Time
}

// Load reads the root under dir, or mints one (label names this device
// in the certificate's subject, so a keychain full of these can be told
// apart; "" is allowed). dir is created 0700 if missing; the root's
// PEM is written 0644 beside the 0600 key so a trust-store install can
// read it without the key.
func Load(dir, label string) (*Authority, error) {
	if dir == "" {
		return nil, errors.New("meshtls: dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	a := &Authority{dir: dir, leaves: map[string]*tls.Certificate{}, now: time.Now}
	keyPEM, kerr := os.ReadFile(filepath.Join(dir, KeyFile))
	certPEM, cerr := os.ReadFile(filepath.Join(dir, CertFile))
	switch {
	case kerr == nil && cerr == nil:
		if err := a.load(keyPEM, certPEM); err != nil {
			return nil, fmt.Errorf("meshtls: %s: %w (remove %s and %s to re-mint; every browser on this device then re-trusts)", dir, err, KeyFile, CertFile)
		}
	case errors.Is(kerr, os.ErrNotExist) && errors.Is(cerr, os.ErrNotExist):
		if err := a.mint(label); err != nil {
			return nil, err
		}
	case kerr != nil && !errors.Is(kerr, os.ErrNotExist):
		return nil, kerr
	case cerr != nil && !errors.Is(cerr, os.ErrNotExist):
		return nil, cerr
	default:
		// One of the two is missing: never mint a root over a stale
		// half — a key whose cert the trust store still holds, or a
		// trusted cert whose key is gone — refuse and say so.
		return nil, fmt.Errorf("meshtls: %s has one of %s/%s but not both; remove the survivor to re-mint (every browser on this device then re-trusts)", dir, KeyFile, CertFile)
	}
	return a, nil
}

func (a *Authority) load(keyPEM, certPEM []byte) error {
	kb, _ := pem.Decode(keyPEM)
	if kb == nil || kb.Type != "PRIVATE KEY" {
		return errors.New("ca.key: not a PEM PRIVATE KEY")
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return fmt.Errorf("ca.key: %w", err)
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("ca.key: %T, want ECDSA", k)
	}
	cb, _ := pem.Decode(certPEM)
	if cb == nil || cb.Type != "CERTIFICATE" {
		return errors.New("ca.crt: not a PEM CERTIFICATE")
	}
	c, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return fmt.Errorf("ca.crt: %w", err)
	}
	if !c.IsCA || !ek.PublicKey.Equal(c.PublicKey) {
		return errors.New("ca.crt does not match ca.key, or is not a CA")
	}
	a.root, a.key, a.pem = c, ek, certPEM
	return nil
}

// mint creates the root: ECDSA P-256 (Ed25519 is not a browser TLS
// key), CA with pathLen 0, keyCertSign only, nameConstraints
// permitting Zone and nothing else — critical, as RFC 5280 wants, so a
// verifier that cannot process it refuses rather than ignores.
func (a *Authority) mint(label string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := serial()
	if err != nil {
		return err
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(spki)
	cn := "talos-mesh device CA " + hex.EncodeToString(sum[:4])
	if label = strings.TrimSpace(label); label != "" {
		cn += " (" + label + ")"
	}
	now := a.now()
	tmpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: cn, Organization: []string{"talos-mesh"}},
		NotBefore:                   now.Add(-backdate),
		NotAfter:                    now.Add(rootValidity),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{Zone},
		SubjectKeyId:                sum[:20],
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	pk8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk8})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	// Key first, then cert: a crash between the two leaves the "one of
	// two" state Load refuses, never a cert without its key on disk.
	if err := writeFile(filepath.Join(a.dir, KeyFile), keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(a.dir, CertFile), certPEM, 0o644); err != nil {
		return err
	}
	a.root, a.key, a.pem = root, key, certPEM
	return nil
}

// Root is the root certificate.
func (a *Authority) Root() *x509.Certificate { return a.root }

// RootPEM is the root as the trust store wants it.
func (a *Authority) RootPEM() []byte { return a.pem }

// RootPath is where RootPEM lives on disk (for `security
// add-trusted-cert`, or an export to the phone's Downloads).
func (a *Authority) RootPath() string { return filepath.Join(a.dir, CertFile) }

// Pool is a cert pool holding only the root: a client that wants to
// verify what this authority serves (tests, curl --cacert).
func (a *Authority) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(a.root)
	return p
}

// Leaf returns a server certificate for name (a bare FQDN under Zone,
// no trailing dot), minting one on first use and re-minting near
// expiry. A name outside Zone is refused here regardless of what the
// browser would do with the constraint: the daemon never signs for a
// name it did not resolve from the plane.
func (a *Authority) Leaf(name string) (*tls.Certificate, error) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if !inZone(name) {
		return nil, fmt.Errorf("meshtls: %q is not under %s", name, Zone)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if c := a.leaves[name]; c != nil && now.Before(c.Leaf.NotAfter.Add(-leafRenewBefore)) {
		return c, nil
	}
	c, err := a.mintLeaf(name, now)
	if err != nil {
		return nil, err
	}
	a.leaves[name] = c
	return c, nil
}

func (a *Authority) mintLeaf(name string, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := serial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(leafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{name},
		AuthorityKeyId:        a.root.SubjectKeyId,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.root, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.root.Raw}, PrivateKey: key, Leaf: leaf}, nil
}

// Config is a server tls.Config that always presents the leaf for name,
// whatever SNI says: the flow reached this name's fake IP, which the
// resolver minted for exactly this name, so the name is the flow's,
// not the ClientHello's. HTTP/1.1 only — one browser connection stays
// one stream, as on :80.
func (a *Authority) Config(name string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return a.Leaf(name)
		},
	}
}

// inZone: name is a proper subdomain of Zone with at least one label.
func inZone(name string) bool {
	if !strings.HasSuffix(name, "."+Zone) {
		return false
	}
	head := strings.TrimSuffix(name, "."+Zone)
	if head == "" || strings.HasPrefix(head, ".") || strings.HasSuffix(head, ".") || strings.Contains(head, "..") {
		return false
	}
	return !strings.ContainsAny(head, " /\\@:")
}

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// writeFile writes atomically (temp + rename) with mode.
func writeFile(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile honours umask
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
