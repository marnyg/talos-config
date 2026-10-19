package parent

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The relying-party half of the person gate (ADR-0032, ADR-0037): the
// parent's UI is an OIDC client of siwe-oidc — authorization code with
// PKCE S256 (the only thing siwe-oidc accepts: public clients, no
// secret), the ID token verified RS256 against the issuer's JWKS, and
// what it says (sub = the wallet, groups) carried in the parent's own
// HMAC-signed cookie for Login.TTL. Hand-rolled against our own
// provider rather than a library: discovery + code exchange + one JWT
// verify is smaller than the dependency.

// Login configures the relying party.
type Login struct {
	// Issuer is siwe-oidc's issuer URL, as the browser reaches it AND
	// as this process does (the manifest pins the name; the `iss`
	// claim must equal it).
	Issuer string
	// ClientID is the -client name siwe-oidc knows this UI by.
	ClientID string
	// RedirectURL is this UI's /callback as the browser reaches it.
	RedirectURL string
	// Key signs the session cookie (32 bytes, keyfile on the volume).
	Key []byte
	// TTL is the cookie's life; 0 ⇒ 7 days.
	TTL time.Duration
	// HTTPClient reaches the issuer; nil ⇒ a 10 s default.
	HTTPClient *http.Client

	mu     sync.Mutex
	disc   *discovery
	discAt time.Time
	keys   map[string]*rsa.PublicKey
	keysAt time.Time
	flows  map[string]flow // state → in-flight login
}

type discovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type flow struct {
	verifier string
	nonce    string
	next     string
	expires  time.Time
}

// Principal is who the cookie says is asking.
type Principal struct {
	Wallet   string   `json:"wallet"`
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
	Exp      int64    `json:"exp"`
}

// Viewer is the store's view of the principal.
func (p Principal) Viewer() Viewer { return Viewer{Wallet: p.Wallet, Groups: p.Groups} }

const cookieName = "sap_parent"

func (l *Login) client() *http.Client {
	if l.HTTPClient != nil {
		return l.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (l *Login) ttl() time.Duration {
	if l.TTL > 0 {
		return l.TTL
	}
	return 7 * 24 * time.Hour
}

func (l *Login) discover(ctx context.Context) (*discovery, error) {
	l.mu.Lock()
	if l.disc != nil && time.Since(l.discAt) < time.Hour {
		d := l.disc
		l.mu.Unlock()
		return d, nil
	}
	l.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(l.Issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery: %s", resp.Status)
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&d); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errors.New("oidc discovery: incomplete document")
	}
	l.mu.Lock()
	l.disc, l.discAt = &d, time.Now()
	l.mu.Unlock()
	return &d, nil
}

func (l *Login) jwks(ctx context.Context, force bool) (map[string]*rsa.PublicKey, error) {
	l.mu.Lock()
	if !force && l.keys != nil && time.Since(l.keysAt) < time.Hour {
		k := l.keys
		l.mu.Unlock()
		return k, nil
	}
	l.mu.Unlock()
	d, err := l.discover(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.JWKSURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(n) == 0 || len(e) == 0 || len(e) > 4 {
			continue
		}
		var eb [8]byte
		copy(eb[8-len(e):], e)
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(binary.BigEndian.Uint64(eb[:]))}
	}
	l.mu.Lock()
	l.keys, l.keysAt = keys, time.Now()
	l.mu.Unlock()
	return keys, nil
}

// Start begins a login: returns the issuer URL to send the browser to,
// remembering the PKCE verifier and nonce under a state for Finish.
// next is where to land after.
func (l *Login) Start(ctx context.Context, next string) (string, error) {
	d, err := l.discover(ctx)
	if err != nil {
		return "", err
	}
	state, verifier, nonce := randomURL(16), randomURL(32), randomURL(16)
	sum := sha256.Sum256([]byte(verifier))
	l.mu.Lock()
	if l.flows == nil {
		l.flows = make(map[string]flow)
	}
	now := time.Now()
	for s, f := range l.flows {
		if f.expires.Before(now) {
			delete(l.flows, s)
		}
	}
	l.flows[state] = flow{verifier: verifier, nonce: nonce, next: next, expires: now.Add(10 * time.Minute)}
	l.mu.Unlock()
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {l.ClientID},
		"redirect_uri":          {l.RedirectURL},
		"scope":                 {"openid profile groups"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode(), nil
}

// Finish redeems the callback's code: the token exchange with the
// PKCE verifier, the ID token verified, the principal returned with
// the flow's next URL.
func (l *Login) Finish(ctx context.Context, state, code string) (Principal, string, error) {
	l.mu.Lock()
	f, ok := l.flows[state]
	delete(l.flows, state)
	l.mu.Unlock()
	if !ok || f.expires.Before(time.Now()) {
		return Principal{}, "", errors.New("login: unknown or expired state")
	}
	d, err := l.discover(ctx)
	if err != nil {
		return Principal{}, "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {l.RedirectURL},
		"client_id":     {l.ClientID},
		"code_verifier": {f.verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Principal{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := l.client().Do(req)
	if err != nil {
		return Principal{}, "", fmt.Errorf("token: %w", err)
	}
	defer resp.Body.Close()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tok); err != nil {
		return Principal{}, "", fmt.Errorf("token: %w", err)
	}
	if resp.StatusCode != http.StatusOK || tok.IDToken == "" {
		return Principal{}, "", fmt.Errorf("token: %s %s", resp.Status, tok.Error)
	}
	claims, err := l.verify(ctx, tok.IDToken)
	if err != nil {
		return Principal{}, "", err
	}
	if claims.Nonce != f.nonce {
		return Principal{}, "", errors.New("id token: nonce mismatch")
	}
	p := Principal{
		Wallet:   strings.ToLower(claims.Sub),
		Username: claims.Username,
		Groups:   claims.Groups,
		Exp:      time.Now().Add(l.ttl()).Unix(),
	}
	if !wallet.MatchString(p.Wallet) {
		return Principal{}, "", fmt.Errorf("id token: sub %q is not a wallet", claims.Sub)
	}
	return p, f.next, nil
}

type idClaims struct {
	Iss      string   `json:"iss"`
	Sub      string   `json:"sub"`
	Aud      any      `json:"aud"`
	Exp      int64    `json:"exp"`
	Iat      int64    `json:"iat"`
	Nonce    string   `json:"nonce"`
	Username string   `json:"preferred_username"`
	Groups   []string `json:"groups"`
}

// verify checks a compact RS256 JWT: header kid → JWKS key (refetched
// once on an unknown kid), signature, iss, aud, exp.
func (l *Login) verify(ctx context.Context, token string) (idClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return idClaims{}, errors.New("id token: not a JWT")
	}
	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return idClaims{}, errors.New("id token: header")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(hdrRaw, &hdr) != nil || hdr.Alg != "RS256" {
		return idClaims{}, errors.New("id token: alg")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return idClaims{}, errors.New("id token: signature encoding")
	}
	keys, err := l.jwks(ctx, false)
	if err != nil {
		return idClaims{}, err
	}
	key, ok := keys[hdr.Kid]
	if !ok {
		if keys, err = l.jwks(ctx, true); err != nil {
			return idClaims{}, err
		}
		if key, ok = keys[hdr.Kid]; !ok {
			return idClaims{}, errors.New("id token: unknown kid")
		}
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return idClaims{}, errors.New("id token: bad signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idClaims{}, errors.New("id token: payload")
	}
	var c idClaims
	if err := json.Unmarshal(body, &c); err != nil {
		return idClaims{}, errors.New("id token: claims")
	}
	if c.Iss != l.Issuer {
		return idClaims{}, fmt.Errorf("id token: iss %q", c.Iss)
	}
	if !audHas(c.Aud, l.ClientID) {
		return idClaims{}, errors.New("id token: aud")
	}
	if c.Exp <= time.Now().Unix() {
		return idClaims{}, errors.New("id token: expired")
	}
	return c, nil
}

func audHas(aud any, want string) bool {
	switch a := aud.(type) {
	case string:
		return a == want
	case []any:
		for _, x := range a {
			if s, ok := x.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// ---- the cookie -------------------------------------------------------------

// Token is the signed principal as a string: the cookie's value, and
// what a non-browser client (cmd/sap) presents as a bearer. One
// signer, one format, one TTL — a token is a cookie you carry yourself.
func (l *Login) Token(p Principal) string {
	body, _ := json.Marshal(p)
	enc := base64.RawURLEncoding.EncodeToString(body)
	return enc + "." + l.sign(enc)
}

// SetCookie writes the signed principal.
func (l *Login) SetCookie(w http.ResponseWriter, p Principal) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: l.Token(p), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Unix(p.Exp, 0),
	})
}

// ClearCookie logs out.
func (l *Login) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

// FromRequest reads the principal off the cookie; ok is false when
// absent, forged or expired.
func (l *Login) FromRequest(r *http.Request) (Principal, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return Principal{}, false
	}
	return l.parse(c.Value)
}

// FromBearer reads the principal off an `Authorization: Bearer` header
// carrying a Token; ok is false when absent, forged or expired. A
// bearer request carries no cookie, so it needs no CSRF rule.
func (l *Login) FromBearer(r *http.Request) (Principal, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return Principal{}, false
	}
	return l.parse(strings.TrimSpace(tok))
}

func (l *Login) parse(value string) (Principal, bool) {
	enc, mac, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(l.sign(enc))) {
		return Principal{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return Principal{}, false
	}
	var p Principal
	if json.Unmarshal(body, &p) != nil || p.Exp <= time.Now().Unix() || !wallet.MatchString(p.Wallet) {
		return Principal{}, false
	}
	return p, true
}

func (l *Login) sign(s string) string {
	m := hmac.New(sha256.New, l.Key)
	m.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func randomURL(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
