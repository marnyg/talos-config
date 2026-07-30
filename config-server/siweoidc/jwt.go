package siweoidc

// Minimal RS256 JWT signing and JWKS publication. Hand-rolled rather
// than a JWT dependency: the provider only ever *signs* (never parses
// untrusted JWTs, which is where JWT libraries earn their complexity),
// and RS256 is SHA-256 + PKCS#1 v1.5 over two base64url segments —
// small enough to own. RS256 because every relying party in the fleet
// (ArgoCD, oauth2-proxy, jellyfin-plugin-sso) verifies it by default.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
)

// signer holds the per-boot RSA key. A restart rotates it: outstanding
// ID tokens fail verification and relying parties re-authenticate —
// the deliberate cost of never persisting key material (invariant 1).
type signer struct {
	key *rsa.PrivateKey
	kid string
}

func newSigner() (*signer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	// kid = truncated hash of the public modulus: stable for the key's
	// lifetime, different every boot.
	sum := sha256.Sum256(key.PublicKey.N.Bytes())
	return &signer{key: key, kid: hex.EncodeToString(sum[:8])}, nil
}

func b64(v any) (string, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(j), nil
}

// signJWT produces a compact RS256 JWT over claims.
func (s *signer) signJWT(claims map[string]any) (string, error) {
	header, err := b64(map[string]string{"alg": "RS256", "typ": "JWT", "kid": s.kid})
	if err != nil {
		return "", err
	}
	payload, err := b64(claims)
	if err != nil {
		return "", err
	}
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// jwks renders the RFC 7517 key set for the discovery document's
// jwks_uri. One key, rotated per boot.
func (s *signer) jwks() map[string]any {
	e := make([]byte, 8)
	binary.BigEndian.PutUint64(e, uint64(s.key.PublicKey.E))
	// strip leading zeros per RFC 7518 §6.3.1 (big-endian minimal)
	i := 0
	for i < 7 && e[i] == 0 {
		i++
	}
	return map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": s.kid,
			"n":   base64.RawURLEncoding.EncodeToString(s.key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(e[i:]),
		}},
	}
}
