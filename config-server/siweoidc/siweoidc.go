// Package siweoidc is a stateless SIWE→OIDC bridge: a minimal OpenID
// Connect provider whose only authentication method is an Ethereum
// EIP-191 personal_sign signature checked against a git-declared admin
// allowlist (the same trust reduction as every other wallet-gated flow
// in this repo — see ethsig). It exists so off-the-shelf OIDC relying
// parties (ArgoCD, oauth2-proxy, jellyfin-plugin-sso) can authenticate
// against the wallet without any hosted identity provider.
//
// Statelessness by construction (invariants 1–2): clients, admins, and
// usernames are declared in git and arrive as flags; auth codes,
// login nonces, and access tokens live in memory; the token-signing
// key is generated per boot. A restart logs everyone out and rotates
// the JWKS — relying parties just send the user back through the
// wallet sign-in. Nothing is ever persisted.
//
// Public clients only: PKCE S256 is mandatory, client secrets do not
// exist (an in-cluster redirect URI cannot keep one anyway), and the
// authorization code is single-use and bound to client_id +
// redirect_uri + code_challenge at mint time.
package siweoidc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// loginNonceTTL bounds how long a rendered sign-in page stays
	// submittable.
	loginNonceTTL = 5 * time.Minute
	// codeTTL is the authorization-code lifetime (RFC 6749 §4.1.2
	// recommends ≤10 minutes; the redeem round-trip is immediate).
	codeTTL = 2 * time.Minute
	// tokenTTL is the ID/access token lifetime. Matches the /status
	// session posture: signing once a day is acceptable; anything
	// longer-lived belongs in the relying party's own cookie.
	tokenTTL = 12 * time.Hour
)

// Client is one git-declared OIDC relying party. Public client: no
// secret, exact-match redirect URIs, PKCE required.
type Client struct {
	ID           string
	RedirectURIs []string
}

// Identity is what a wallet signature resolves to: the claims minted
// into ID tokens and served from /userinfo. Username comes from the
// git-declared addr→name map; Email is fabricated from it because
// several relying parties (oauth2-proxy, Jellyfin) refuse identities
// without one — it is an identifier, not a mailbox.
type Identity struct {
	Addr     string // lowercase 0x — the `sub` claim
	Username string
}

func (id Identity) claims() map[string]any {
	return map[string]any{
		"sub":                id.Addr,
		"preferred_username": id.Username,
		"name":               id.Username,
		"email":              id.Username + "@mesh.internal",
		"email_verified":     true,
		"groups":             []string{"admins"},
	}
}

// authCode is one outstanding authorization code: single-use, bound at
// mint time to everything the token exchange must re-present.
type authCode struct {
	clientID      string
	redirectURI   string
	codeChallenge string
	oidcNonce     string // relying party's nonce, echoed into the ID token
	identity      Identity
	expires       time.Time
}

// accessToken is one outstanding opaque bearer token, resolvable at
// /userinfo.
type accessToken struct {
	identity Identity
	expires  time.Time
}

// Provider is the OIDC provider state: git-declared configuration plus
// in-memory, per-boot protocol state.
type Provider struct {
	issuer  string
	clients map[string]Client
	admins  map[string]string // lowercase 0x addr -> username
	signer  *signer           // per-boot RS256 key

	mu     sync.Mutex
	nonces map[string]time.Time
	codes  map[string]*authCode
	tokens map[string]*accessToken
	now    func() time.Time
}

// New constructs a provider. issuer is the externally visible base URL
// (no trailing slash); admins maps allowlisted wallet addresses
// (lowercase 0x) to usernames.
func New(issuer string, clients []Client, admins map[string]string) (*Provider, error) {
	if issuer == "" || strings.HasSuffix(issuer, "/") {
		return nil, fmt.Errorf("issuer must be a base URL without trailing slash, got %q", issuer)
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("no clients declared")
	}
	if len(admins) == 0 {
		return nil, fmt.Errorf("no admin addresses declared")
	}
	byID := make(map[string]Client, len(clients))
	for _, c := range clients {
		if c.ID == "" || len(c.RedirectURIs) == 0 {
			return nil, fmt.Errorf("client %q needs an id and at least one redirect URI", c.ID)
		}
		if _, dup := byID[c.ID]; dup {
			return nil, fmt.Errorf("duplicate client id %q", c.ID)
		}
		byID[c.ID] = c
	}
	sig, err := newSigner()
	if err != nil {
		return nil, fmt.Errorf("generating per-boot signing key: %w", err)
	}
	return &Provider{
		issuer:  issuer,
		clients: byID,
		admins:  admins,
		signer:  sig,
		nonces:  map[string]time.Time{},
		codes:   map[string]*authCode{},
		tokens:  map[string]*accessToken{},
		now:     time.Now,
	}, nil
}

// Issuer returns the provider's issuer base URL.
func (p *Provider) Issuer() string { return p.issuer }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is not recoverable
	}
	return hex.EncodeToString(b)
}

// loginMessage is the canonical text the admin signs to authenticate.
// Distinct prefix from every other wallet message in the fleet; names
// the relying party so the human sees what they are signing into.
// Signing it authorizes one authorization code for that client,
// nothing more.
func loginMessage(clientID, nonce string) string {
	return fmt.Sprintf("siwe-oidc sign-in\nclient: %s\nnonce: %s", clientID, nonce)
}

// issueNonce mints a sign-in challenge nonce.
func (p *Provider) issueNonce() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	n := randomHex(16)
	p.nonces[n] = p.now().Add(loginNonceTTL)
	return n
}

// redeemNonce consumes an outstanding nonce. Single-use: a replayed
// sign-in submission fails here.
func (p *Provider) redeemNonce(n string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	if _, ok := p.nonces[n]; !ok {
		return false
	}
	delete(p.nonces, n)
	return true
}

// identityFor resolves a recovered wallet address against the
// allowlist.
func (p *Provider) identityFor(addr string) (Identity, bool) {
	name, ok := p.admins[addr]
	if !ok {
		return Identity{}, false
	}
	return Identity{Addr: addr, Username: name}, true
}

// authRequest is the validated shape of an /authorize request.
type authRequest struct {
	clientID      string
	redirectURI   string
	state         string
	oidcNonce     string
	codeChallenge string
}

// validateAuthRequest checks an authorization request. The first error
// class (unknown client / unregistered redirect URI) must be rendered,
// never redirected — redirecting would make the provider an open
// redirector. The second class (bad response_type, missing PKCE) is
// safe to report to the registered redirect URI per RFC 6749 §4.1.2.1.
func (p *Provider) validateAuthRequest(q map[string]string) (authRequest, string, error) {
	req := authRequest{
		clientID:      q["client_id"],
		redirectURI:   q["redirect_uri"],
		state:         q["state"],
		oidcNonce:     q["nonce"],
		codeChallenge: q["code_challenge"],
	}
	c, ok := p.clients[req.clientID]
	if !ok {
		return req, "", fmt.Errorf("unknown client_id %q", req.clientID)
	}
	if !slices.Contains(c.RedirectURIs, req.redirectURI) {
		return req, "", fmt.Errorf("redirect_uri %q is not registered for client %q", req.redirectURI, req.clientID)
	}
	switch {
	case q["response_type"] != "code":
		return req, "unsupported_response_type", nil
	case req.codeChallenge == "":
		return req, "invalid_request", nil // PKCE is mandatory
	case q["code_challenge_method"] != "S256":
		return req, "invalid_request", nil // plain would defeat PKCE
	}
	return req, "", nil
}

// mintCode issues a single-use authorization code bound to the request
// and the authenticated identity.
func (p *Provider) mintCode(req authRequest, id Identity) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	code := randomHex(32)
	p.codes[code] = &authCode{
		clientID:      req.clientID,
		redirectURI:   req.redirectURI,
		codeChallenge: req.codeChallenge,
		oidcNonce:     req.oidcNonce,
		identity:      id,
		expires:       p.now().Add(codeTTL),
	}
	return code
}

// tokenResponse is the successful /token payload.
type tokenResponse struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// redeemCode performs the authorization-code exchange: single-use code,
// exact client/redirect match, PKCE S256 verification. On success it
// mints the ID token and an opaque access token for /userinfo.
func (p *Provider) redeemCode(code, clientID, redirectURI, verifier string) (tokenResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()

	ac, ok := p.codes[code]
	if !ok {
		return tokenResponse{}, fmt.Errorf("unknown, expired, or already-used code")
	}
	delete(p.codes, code) // single-use, burned even on a failed exchange

	if ac.clientID != clientID {
		return tokenResponse{}, fmt.Errorf("code was issued to a different client")
	}
	if ac.redirectURI != redirectURI {
		return tokenResponse{}, fmt.Errorf("redirect_uri does not match the authorization request")
	}
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != ac.codeChallenge {
		return tokenResponse{}, fmt.Errorf("PKCE verification failed")
	}

	now := p.now()
	claims := ac.identity.claims()
	claims["iss"] = p.issuer
	claims["aud"] = ac.clientID
	claims["iat"] = now.Unix()
	claims["exp"] = now.Add(tokenTTL).Unix()
	if ac.oidcNonce != "" {
		claims["nonce"] = ac.oidcNonce
	}
	idToken, err := p.signer.signJWT(claims)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("signing ID token: %w", err)
	}

	at := randomHex(32)
	p.tokens[at] = &accessToken{identity: ac.identity, expires: now.Add(tokenTTL)}

	return tokenResponse{
		IDToken:     idToken,
		AccessToken: at,
		TokenType:   "Bearer",
		ExpiresIn:   int(tokenTTL.Seconds()),
	}, nil
}

// userinfoFor resolves an opaque access token to its claims.
func (p *Provider) userinfoFor(token string) (map[string]any, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	at, ok := p.tokens[token]
	if !ok {
		return nil, false
	}
	return at.identity.claims(), true
}

// PublicKey exposes the per-boot verification key (for tests).
func (p *Provider) PublicKey() *rsa.PublicKey { return &p.signer.key.PublicKey }

// expireLocked prunes expired nonces, codes, and tokens. Caller holds mu.
func (p *Provider) expireLocked() {
	now := p.now()
	for n, exp := range p.nonces {
		if now.After(exp) {
			delete(p.nonces, n)
		}
	}
	for c, ac := range p.codes {
		if now.After(ac.expires) {
			delete(p.codes, c)
		}
	}
	for t, at := range p.tokens {
		if now.After(at.expires) {
			delete(p.tokens, t)
		}
	}
}
