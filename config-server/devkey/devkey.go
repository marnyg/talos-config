// Package devkey is the device half of ADR-0012 enrollment: X25519
// identity generation and the pki.key splice that completes a
// hub-rendered config. Shared by nebup (CLI) and the mobile bind (the
// Android TV/phone app) so both clients have identical key semantics —
// the private key is device-born and never travels; the hub only ever
// sees the pubkey.
package devkey

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/curve25519"

	"github.com/marnyg/talos-config/config-server/nebderive"
)

// Generate returns a fresh X25519 keypair (RFC 7748 clamped).
func Generate() (priv, pub [32]byte, err error) {
	if _, err = rand.Read(priv[:]); err != nil {
		return priv, pub, err
	}
	// X25519 clamp: RFC 7748.
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	curve25519.ScalarBaseMult(&pub, &priv)
	return priv, pub, nil
}

// ParsePrivHex decodes a stored private key (raw 32 bytes hex, the
// format both clients persist) and derives its pubkey.
func ParsePrivHex(s string) (priv, pub [32]byte, err error) {
	raw, decErr := hex.DecodeString(strings.TrimSpace(s))
	if decErr != nil || len(raw) != 32 {
		return priv, pub, fmt.Errorf("not a 32-byte hex X25519 private key")
	}
	copy(priv[:], raw)
	curve25519.ScalarBaseMult(&pub, &priv)
	return priv, pub, nil
}

// SpliceKeyInline replaces the empty pki.key placeholder with the
// device key as an inline PEM block. By text rather than yaml
// round-trip because a full re-marshal would reflow the CA and cert
// PEM blocks the hub sent (yaml.v3 folds block scalars
// idiosyncratically). Indent-agnostic: matches whatever indentation
// the sibling ca:/cert: use.
func SpliceKeyInline(cfg []byte, priv [32]byte) ([]byte, error) {
	keyPEM := nebderive.HostKeyPEM(priv)
	lines := strings.Split(string(cfg), "\n")
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		if !strings.HasPrefix(trimmed, "key:") {
			continue
		}
		after := strings.TrimSpace(strings.TrimPrefix(trimmed, "key:"))
		if after != "" && after != "\"\"" && after != "''" {
			// Non-empty key already: nothing to splice, and refusing here
			// is safer than clobbering whatever it is.
			return nil, fmt.Errorf("pki.key already set in hub config; refuse to overwrite")
		}
		indent := l[:len(l)-len(trimmed)]
		var replacement strings.Builder
		replacement.WriteString(indent + "key: |\n")
		for _, pl := range strings.Split(strings.TrimRight(string(keyPEM), "\n"), "\n") {
			replacement.WriteString(indent + "    " + pl + "\n")
		}
		var out strings.Builder
		for _, prev := range lines[:i] {
			out.WriteString(prev)
			out.WriteByte('\n')
		}
		out.WriteString(replacement.String())
		out.WriteString(strings.Join(lines[i+1:], "\n"))
		return []byte(out.String()), nil
	}
	return nil, fmt.Errorf("pki.key placeholder not found in hub config (unexpected shape)")
}
