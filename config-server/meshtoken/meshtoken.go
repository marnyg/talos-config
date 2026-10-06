// Package meshtoken is the gateway-signed per-request identity token
// (ADR-0032): the app seam between the gateway, which terminates the
// identity stream and knows the caller, and the SIWE→OIDC bridge,
// which decides for the apps. The bare X-Mesh-* headers are ambient —
// any pod can forge them toward ingress-nginx or the bridge — so the
// gateway also carries the same facts as a compact EdDSA JWT under its
// own member key, and the bridge verifies that against a key pinned
// in git. Forging it needs the gateway's key; `aud` (the request Host)
// stops replay across hosts; `exp` bounds the rest.
//
// It is a presentation-layer artifact, not a protocol cert class: no
// verb, no chain, no caveats. Hand-rolled like siweoidc/jwt.go — the
// shape is two base64url segments and an Ed25519 signature over them,
// and the verifier accepts exactly one algorithm from exactly the keys
// it was given.
package meshtoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// Header carries the token on the proxied request. Stripped from the
// inbound request like the other X-Mesh-* headers before being set.
const Header = "X-Mesh-Token"

// TTL is the token lifetime: one request's worth. A verifier's clock
// within Leeway of the gateway's is enough.
const TTL = 60 * time.Second

// Leeway absorbs clock skew between gateway and verifier on both
// bounds (iat in the future, exp just past).
const Leeway = 10 * time.Second

// Claims is the token body: who the gateway is, who it admitted, and
// for which Host. Groups are the member cert's, verbatim.
type Claims struct {
	Issuer   cert.ActorID `json:"iss"`
	Subject  cert.ActorID `json:"sub"`
	Name     string       `json:"name"`
	Groups   []string     `json:"groups"`
	Audience string       `json:"aud"`
	IssuedAt int64        `json:"iat"`
	Expires  int64        `json:"exp"`
}

// HasGroup reports whether g is among the token's groups.
func (c Claims) HasGroup(g string) bool {
	for _, x := range c.Groups {
		if x == g {
			return true
		}
	}
	return false
}

var header = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))

// Signer mints tokens under one member key.
type Signer struct {
	priv ed25519.PrivateKey
	id   cert.ActorID
}

// NewSigner wraps the gateway's member key.
func NewSigner(priv ed25519.PrivateKey) *Signer {
	return &Signer{priv: priv, id: cert.NewEdSigner(priv).ActorID()}
}

// ID is the signer's actor id — what a verifier pins.
func (s *Signer) ID() cert.ActorID { return s.id }

// Mint signs a token for id, addressed to aud (the request Host,
// normalised with NormalizeAudience), valid TTL from now.
func (s *Signer) Mint(id cert.Identity, aud string, now time.Time) (string, error) {
	groups := id.Groups
	if groups == nil {
		groups = []string{}
	}
	body, err := json.Marshal(Claims{
		Issuer:   s.id,
		Subject:  id.Key,
		Name:     id.Name,
		Groups:   groups,
		Audience: NormalizeAudience(aud),
		IssuedAt: now.Unix(),
		Expires:  now.Add(TTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	input := header + "." + base64.RawURLEncoding.EncodeToString(body)
	sig := ed25519.Sign(s.priv, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// NormalizeAudience maps a request Host to the audience string:
// lowercase, default-port-free. Both sides apply it so a Host written
// `Sonarr.gw.mesh.internal:80` and `sonarr.gw.mesh.internal` agree.
func NormalizeAudience(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ":80")
	host = strings.TrimSuffix(host, ":443")
	return host
}

// Verification errors. All are refusals; the distinction is for logs
// and for the bridge's 401-vs-403 split (ErrGroup is the latter, and
// is raised by the caller via HasGroup, not here).
var (
	ErrMalformed = errors.New("meshtoken: malformed token")
	ErrIssuer    = errors.New("meshtoken: issuer not pinned")
	ErrSignature = errors.New("meshtoken: bad signature")
	ErrExpired   = errors.New("meshtoken: expired or not yet valid")
	ErrAudience  = errors.New("meshtoken: audience mismatch")
)

// Verifier accepts tokens from a closed set of issuers (the gateway
// keys pinned in git).
type Verifier struct {
	keys map[cert.ActorID]ed25519.PublicKey
}

// NewVerifier pins issuers. Each must be an ed:<hex> actor id.
func NewVerifier(issuers ...cert.ActorID) (*Verifier, error) {
	if len(issuers) == 0 {
		return nil, errors.New("meshtoken: no issuers pinned")
	}
	v := &Verifier{keys: make(map[cert.ActorID]ed25519.PublicKey, len(issuers))}
	for _, id := range issuers {
		if err := id.Validate(); err != nil {
			return nil, err
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(string(id), "ed:"))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("meshtoken: %s is not an ed25519 actor id", id)
		}
		v.keys[id] = ed25519.PublicKey(raw)
	}
	return v, nil
}

// Verify checks token against the pinned issuers, the audience (a
// request Host; normalised here) and the clock. Signature is verified
// against the key the body's iss names, so an iss outside the pinned
// set is refused before any crypto.
func (v *Verifier) Verify(token, aud string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != header {
		return Claims{}, ErrMalformed
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Claims{}, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(body, &c); err != nil {
		return Claims{}, ErrMalformed
	}
	pub, ok := v.keys[c.Issuer]
	if !ok {
		return Claims{}, ErrIssuer
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return Claims{}, ErrSignature
	}
	t := now.Unix()
	if c.IssuedAt > t+int64(Leeway.Seconds()) || c.Expires < t-int64(Leeway.Seconds()) {
		return Claims{}, ErrExpired
	}
	if c.Audience == "" || c.Audience != NormalizeAudience(aud) {
		return Claims{}, ErrAudience
	}
	if err := c.Subject.Validate(); err != nil {
		return Claims{}, ErrMalformed
	}
	return c, nil
}
