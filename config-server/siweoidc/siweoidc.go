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
//
// Two gates (ADR-0032). The person gate is the wallet login above. The
// group gate is /authz: nginx's auth_request subrequest presents the
// gateway-signed per-request identity token (meshtoken) and the
// required group; the bridge verifies the token against the gateway
// ids pinned in git and answers 200/401/403 — no cookie, no session,
// no fallback: a request without a valid token is a pod-network
// caller, and a member device outside the group has nothing to sign
// in as. The same token also logs a device in at /authorize without
// the wallet prompt, for clients that opt in (Client.Token): the
// identity minted is the device — sub is its actor id, the username
// its member name — never a person inferred from it.
package siweoidc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/config-server/meshtoken"
	"github.com/marnyg/talos-config/protocol/cert"
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
// secret, exact-match redirect URIs, PKCE required. Token opts the
// client into device login: a valid identity token on /authorize
// mints a code for the device without the wallet page. Off for apps
// whose per-user state should follow the person (Jellyfin: one user
// per device would split watch state); on where the device is the
// right principal (ArgoCD: the audit log names the device).
type Client struct {
	ID           string
	RedirectURIs []string
	Token        bool
}

// Admin is one git-declared wallet: the username and groups its
// signature resolves to. Groups are declared per wallet, never
// inferred — "a wallet that may sign in is an admin" was an N=1
// inference that broke on the second wallet (`5kh`). The names are
// the closed group vocabulary (policy.Groups); the bridge carries them
// as a claim and attaches no meaning of its own.
type Admin struct {
	Username string
	Groups   []string
}

// Identity is what a login resolves to: the claims minted into ID
// tokens and served from /userinfo. For a wallet login Sub is the
// lowercase 0x address and Username/Groups come from the git-declared
// addr→Admin map; for a device login Sub is the device's actor id
// (`ed:…`) and Username/Groups are its member name and groups, as the
// gateway attested them. Email is fabricated from the username because
// several relying parties (Jellyfin) refuse identities without one —
// it is an identifier, not a mailbox.
type Identity struct {
	Sub      string
	Username string
	Groups   []string
}

func (id Identity) claims() map[string]any {
	groups := id.Groups
	if groups == nil {
		groups = []string{} // `[]`, never `null`: relying parties range over it
	}
	return map[string]any{
		"sub":                id.Sub,
		"preferred_username": id.Username,
		"name":               id.Username,
		"email":              id.Username + "@mesh.internal",
		"email_verified":     true,
		"groups":             slices.Clone(groups),
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
	admins  map[string]Admin    // lowercase 0x addr -> username + groups
	signer  *signer             // per-boot RS256 key
	tokens  *meshtoken.Verifier // nil: no gateway pinned, token paths refuse

	mu     sync.Mutex
	nonces map[string]time.Time
	codes  map[string]*authCode
	access map[string]*accessToken
	now    func() time.Time
}

// New constructs a provider. issuer is the externally visible base URL
// (no trailing slash); admins maps allowlisted wallet addresses
// (lowercase 0x) to their username and groups; gateways are the
// identity-token issuers to trust (the gateway member ids pinned in
// git) — none means /authz always refuses and Client.Token is inert.
func New(issuer string, clients []Client, admins map[string]Admin, gateways []cert.ActorID) (*Provider, error) {
	if issuer == "" || strings.HasSuffix(issuer, "/") {
		return nil, fmt.Errorf("issuer must be a base URL without trailing slash, got %q", issuer)
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("no clients declared")
	}
	if len(admins) == 0 {
		return nil, fmt.Errorf("no admin addresses declared")
	}
	for addr, a := range admins {
		if a.Username == "" {
			return nil, fmt.Errorf("admin %s needs a username", addr)
		}
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
	var verifier *meshtoken.Verifier
	if len(gateways) > 0 {
		if verifier, err = meshtoken.NewVerifier(gateways...); err != nil {
			return nil, fmt.Errorf("pinning gateways: %w", err)
		}
	}
	return &Provider{
		issuer:  issuer,
		clients: byID,
		admins:  admins,
		signer:  sig,
		tokens:  verifier,
		nonces:  map[string]time.Time{},
		codes:   map[string]*authCode{},
		access:  map[string]*accessToken{},
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
	a, ok := p.admins[addr]
	if !ok {
		return Identity{}, false
	}
	return Identity{Sub: addr, Username: a.Username, Groups: a.Groups}, true
}

// errNoToken: the request carried no token at all (the common case on
// /authorize from a browser off the mesh path; not worth a log line).
var errNoToken = errors.New("no token")

// deviceIdentity verifies a gateway-signed identity token presented
// for host and resolves it to the device's identity. Every refusal (no
// gateway pinned, absent, malformed, foreign, expired, wrong audience)
// is an error; the reason is for the log, the caller's answer is the
// same.
func (p *Provider) deviceIdentity(token, host string) (Identity, meshtoken.Claims, error) {
	if p.tokens == nil {
		return Identity{}, meshtoken.Claims{}, fmt.Errorf("no gateway pinned")
	}
	if token == "" {
		return Identity{}, meshtoken.Claims{}, errNoToken
	}
	c, err := p.tokens.Verify(token, host, p.now())
	if err != nil {
		return Identity{}, meshtoken.Claims{}, err
	}
	return Identity{Sub: string(c.Subject), Username: c.Name, Groups: c.Groups}, c, nil
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
	p.access[at] = &accessToken{identity: ac.identity, expires: now.Add(tokenTTL)}

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
	at, ok := p.access[token]
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
	for t, at := range p.access {
		if now.After(at.expires) {
			delete(p.access, t)
		}
	}
}
