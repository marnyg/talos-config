package siweoidc

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/meshtoken"
	"github.com/marnyg/talos-config/protocol/cert"
)

// wellKnownAddr is the Ethereum address of private key 0x…01.
const wellKnownAddr = "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"

func testKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	b := make([]byte, 32)
	b[31] = 1
	return secp256k1.PrivKeyFromBytes(b)
}

// personalSign produces an Ethereum personal_sign signature (r||s||v hex).
func personalSign(t *testing.T, priv *secp256k1.PrivateKey, message string) string {
	t.Helper()
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := ethsig.Keccak256([]byte(prefixed))
	compact := secpecdsa.SignCompact(priv, hash, false) // [v+27 || r || s]
	sig := make([]byte, 65)
	copy(sig[:64], compact[1:])
	sig[64] = compact[0]
	return "0x" + hex.EncodeToString(sig)
}

const (
	testIssuer   = "http://auth.cp1.mesh.internal"
	testClient   = "argocd"
	testRedirect = "http://argocd.cp1.mesh.internal/auth/callback"
)

func testProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(testIssuer,
		[]Client{{ID: testClient, RedirectURIs: []string{testRedirect}}},
		map[string]Admin{wellKnownAddr: {Username: "mar", Groups: []string{"admins"}}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// pkce returns a verifier and its S256 challenge.
func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

var loginNonceRe = regexp.MustCompile(`name="login_nonce" value="([0-9a-f]+)"`)

// authorize drives GET /authorize → wallet sign → POST /authorize and
// returns the redirect back to the relying party.
func authorize(t *testing.T, ts *httptest.Server, challenge, state, oidcNonce string) *url.URL {
	t.Helper()
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {testClient},
		"redirect_uri":          {testRedirect},
		"scope":                 {"openid profile email groups"},
		"state":                 {state},
		"nonce":                 {oidcNonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	resp, err := ts.Client().Get(ts.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /authorize: %d: %s", resp.StatusCode, body)
	}
	m := loginNonceRe.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no login_nonce in sign-in page:\n%s", body)
	}
	nonce := string(m[1])

	form := url.Values{
		"client_id":             {testClient},
		"redirect_uri":          {testRedirect},
		"response_type":         {"code"},
		"state":                 {state},
		"nonce":                 {oidcNonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"login_nonce":           {nonce},
		"signature":             {personalSign(t, testKey(t), loginMessage(testClient, nonce))},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp2, err := client.PostForm(ts.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		body2, _ := io.ReadAll(resp2.Body)
		t.Fatalf("POST /authorize: %d: %s", resp2.StatusCode, body2)
	}
	loc, err := url.Parse(resp2.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func exchange(t *testing.T, ts *httptest.Server, code, verifier string) (tokenResponse, int) {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {testClient},
		"redirect_uri":  {testRedirect},
		"code_verifier": {verifier},
	}
	resp, err := ts.Client().PostForm(ts.URL+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tr tokenResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
			t.Fatal(err)
		}
	}
	return tr, resp.StatusCode
}

// verifyJWT checks an RS256 JWT against pub and returns its claims.
// This is the relying party's job in production; the test plays that
// role here.
func verifyJWT(t *testing.T, pub *rsa.PublicKey, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d segments, want 3", len(parts))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("ID token signature: %v", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestFullFlow(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	verifier, challenge := pkce()
	loc := authorize(t, ts, challenge, "st4te", "n0nce")

	if got := loc.Query().Get("state"); got != "st4te" {
		t.Fatalf("state = %q, want st4te", got)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect %s", loc)
	}
	if !strings.HasPrefix(loc.String(), testRedirect) {
		t.Fatalf("redirected to %s, want %s", loc, testRedirect)
	}

	tr, status := exchange(t, ts, code, verifier)
	if status != http.StatusOK {
		t.Fatalf("token exchange: %d", status)
	}

	claims := verifyJWT(t, p.PublicKey(), tr.IDToken)
	for k, want := range map[string]any{
		"iss":                testIssuer,
		"aud":                testClient,
		"sub":                wellKnownAddr,
		"nonce":              "n0nce",
		"preferred_username": "mar",
		"email":              "mar@mesh.internal",
	} {
		if claims[k] != want {
			t.Errorf("claim %s = %v, want %v", k, claims[k], want)
		}
	}
	groups, ok := claims["groups"].([]any)
	if !ok || len(groups) != 1 || groups[0] != "admins" {
		t.Errorf("groups = %v, want [admins]", claims["groups"])
	}

	// /userinfo answers for the access token.
	req, _ := http.NewRequest("GET", ts.URL+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tr.AccessToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/userinfo: %d", resp.StatusCode)
	}
	var ui map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&ui); err != nil {
		t.Fatal(err)
	}
	if ui["sub"] != wellKnownAddr {
		t.Errorf("userinfo sub = %v, want %s", ui["sub"], wellKnownAddr)
	}
}

// TestExchangeWithBasicAuthClientID: golang.org/x/oauth2 (ArgoCD,
// oauth2-proxy) sends client_id as HTTP Basic auth with an empty
// password before falling back to form params — and the fallback
// arrives after the code is burned, so the Basic style must succeed
// on the first attempt.
func TestExchangeWithBasicAuthClientID(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	verifier, challenge := pkce()
	code := authorize(t, ts, challenge, "", "").Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {testRedirect},
		"code_verifier": {verifier},
	}
	req, _ := http.NewRequest("POST", ts.URL+"/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(testClient, "")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("basic-auth exchange: %d: %s", resp.StatusCode, body)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		t.Fatal(err)
	}
	if tr.IDToken == "" {
		t.Fatal("no id_token in basic-auth exchange")
	}
}

func TestCodeSingleUse(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	verifier, challenge := pkce()
	code := authorize(t, ts, challenge, "", "").Query().Get("code")

	if _, status := exchange(t, ts, code, verifier); status != http.StatusOK {
		t.Fatalf("first exchange: %d", status)
	}
	if _, status := exchange(t, ts, code, verifier); status != http.StatusBadRequest {
		t.Fatalf("replayed code: %d, want 400", status)
	}
}

func TestPKCERequired(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	// Wrong verifier must fail — and burn the code.
	_, challenge := pkce()
	code := authorize(t, ts, challenge, "", "").Query().Get("code")
	if _, status := exchange(t, ts, code, strings.Repeat("w", 43)); status != http.StatusBadRequest {
		t.Fatalf("wrong verifier: %d, want 400", status)
	}
	if _, status := exchange(t, ts, code, strings.Repeat("v", 43)); status != http.StatusBadRequest {
		t.Fatalf("code must be burned after failed exchange: %d, want 400", status)
	}

	// An authorization request without PKCE is bounced to the client.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {testClient},
		"redirect_uri":  {testRedirect},
	}
	resp, err := client.Get(ts.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("no-PKCE authorize: %d, want 302", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("no-PKCE authorize error = %q, want invalid_request", loc.Query().Get("error"))
	}
}

func TestUnknownClientAndRedirectNotRedirected(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	for name, q := range map[string]url.Values{
		"unknown client": {
			"response_type": {"code"},
			"client_id":     {"evil"},
			"redirect_uri":  {testRedirect},
		},
		"unregistered redirect": {
			"response_type": {"code"},
			"client_id":     {testClient},
			"redirect_uri":  {"http://evil.example/callback"},
		},
	} {
		resp, err := ts.Client().Get(ts.URL + "/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (must never redirect)", name, resp.StatusCode)
		}
	}
}

// Groups are per wallet from the declared map, not a literal shared by
// every wallet that may sign in (`5kh`).
func TestGroupsComeFromTheAdminMap(t *testing.T) {
	const tv = "0x2b5ad5c4795c026514f8317c7a215e218dccd6cf" // key 0x…02
	p, err := New(testIssuer,
		[]Client{{ID: testClient, RedirectURIs: []string{testRedirect}}},
		map[string]Admin{
			wellKnownAddr: {Username: "mar", Groups: []string{"admins", "media"}},
			tv:            {Username: "tv", Groups: []string{"media"}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string][]string{
		wellKnownAddr: {"admins", "media"},
		tv:            {"media"},
	} {
		id, ok := p.identityFor(addr)
		if !ok {
			t.Fatalf("%s not resolved", addr)
		}
		got, _ := id.claims()["groups"].([]string)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s groups = %v, want %v", id.Username, got, want)
		}
	}

	// A wallet with no groups still mints a `groups` array, not null.
	if got := (Identity{Sub: tv, Username: "tv"}).claims()["groups"]; fmt.Sprint(got) != "[]" {
		t.Errorf("empty groups = %#v, want []", got)
	}

	// A username is mandatory per admin.
	if _, err := New(testIssuer,
		[]Client{{ID: testClient, RedirectURIs: []string{testRedirect}}},
		map[string]Admin{wellKnownAddr: {Groups: []string{"admins"}}},
		nil,
	); err == nil {
		t.Error("admin without username accepted")
	}
}

func TestNonAdminWalletRejected(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	// Key 0x…02 is a valid wallet but not on the allowlist.
	b := make([]byte, 32)
	b[31] = 2
	stranger := secp256k1.PrivKeyFromBytes(b)

	nonce := p.issueNonce()
	_, challenge := pkce()
	form := url.Values{
		"client_id":             {testClient},
		"redirect_uri":          {testRedirect},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"login_nonce":           {nonce},
		"signature":             {personalSign(t, stranger, loginMessage(testClient, nonce))},
	}

	resp, err := ts.Client().PostForm(ts.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stranger wallet: %d, want 403", resp.StatusCode)
	}
}

func TestLoginNonceSingleUse(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	_, challenge := pkce()
	nonce := p.issueNonce()
	form := url.Values{
		"client_id":             {testClient},
		"redirect_uri":          {testRedirect},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"login_nonce":           {nonce},
		"signature":             {personalSign(t, testKey(t), loginMessage(testClient, nonce))},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first submit: %d, want 303", resp.StatusCode)
	}

	// Replaying the same signed form must not mint a second code.
	resp2, err := client.PostForm(ts.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || !strings.Contains(string(body), "expired or already used") {
		t.Fatalf("replayed submit: %d %s — want re-rendered login with expiry message", resp2.StatusCode, body)
	}
}

func TestExpiredCode(t *testing.T) {
	p := testProvider(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	verifier, challenge := pkce()
	code := authorize(t, ts, challenge, "", "").Query().Get("code")

	now = now.Add(codeTTL + time.Second)
	if _, status := exchange(t, ts, code, verifier); status != http.StatusBadRequest {
		t.Fatalf("expired code: %d, want 400", status)
	}
}

func TestDiscoveryAndJWKS(t *testing.T) {
	p := testProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc["issuer"] != testIssuer {
		t.Errorf("issuer = %v, want %s", doc["issuer"], testIssuer)
	}
	if doc["jwks_uri"] != testIssuer+"/jwks" {
		t.Errorf("jwks_uri = %v", doc["jwks_uri"])
	}

	resp2, err := ts.Client().Get(ts.URL + "/jwks")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&jwks); err != nil {
		t.Fatal(err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("JWKS has %d keys, want 1", len(jwks.Keys))
	}
	k := jwks.Keys[0]
	if k["kty"] != "RSA" || k["alg"] != "RS256" || k["n"] == "" || k["e"] == "" {
		t.Errorf("malformed JWK: %v", k)
	}
	// e must decode to the actual public exponent (65537 → AQAB).
	if k["e"] != "AQAB" {
		t.Errorf("e = %v, want AQAB", k["e"])
	}
}

func TestCodeBoundToClientAndRedirect(t *testing.T) {
	p, err := New(testIssuer,
		[]Client{
			{ID: testClient, RedirectURIs: []string{testRedirect}},
			{ID: "other", RedirectURIs: []string{"http://other.cp1.mesh.internal/cb"}},
		},
		map[string]Admin{wellKnownAddr: {Username: "mar", Groups: []string{"admins"}}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()

	verifier, challenge := pkce()
	code := authorize(t, ts, challenge, "", "").Query().Get("code")

	// Another client cannot redeem the code.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {"other"},
		"redirect_uri":  {testRedirect},
		"code_verifier": {verifier},
	}
	resp, err := ts.Client().PostForm(ts.URL+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-client redemption: %d, want 400", resp.StatusCode)
	}
}

// The group gate and device login (ADR-0032). gw is the pinned gateway;
// rogue is a key nobody pinned (a pod that minted its own token).
func tokenProvider(t *testing.T) (*Provider, *meshtoken.Signer, *meshtoken.Signer) {
	t.Helper()
	_, gwKey, _ := ed25519.GenerateKey(rand.Reader)
	_, rogueKey, _ := ed25519.GenerateKey(rand.Reader)
	gw, rogue := meshtoken.NewSigner(gwKey), meshtoken.NewSigner(rogueKey)
	p, err := New(testIssuer,
		[]Client{
			{ID: testClient, RedirectURIs: []string{testRedirect}, Token: true},
			{ID: "jellyfin", RedirectURIs: []string{"http://jellyfin.gw.mesh.internal/sso/OID/redirect/wallet"}},
		},
		map[string]Admin{wellKnownAddr: {Username: "mar", Groups: []string{"admins"}}},
		[]cert.ActorID{gw.ID()},
	)
	if err != nil {
		t.Fatal(err)
	}
	return p, gw, rogue
}

var (
	laptop = cert.Identity{Key: cert.ActorID("ed:" + strings.Repeat("ab", 32)), Name: "marius-mac", Groups: []string{"admins"}}
	tvDev  = cert.Identity{Key: cert.ActorID("ed:" + strings.Repeat("cd", 32)), Name: "tv", Groups: []string{"media"}}
)

func authz(t *testing.T, ts *httptest.Server, group, origHost, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+"/authz?group="+group, nil)
	// What ingress-nginx's auth_request subrequest looks like: Host is
	// the bridge Service, the app host rides X-Original-URL.
	req.Host = "siwe-oidc.sso.svc.cluster.local"
	if origHost != "" {
		req.Header.Set("X-Original-URL", "http://"+origHost+"/some/path?x=1")
	}
	if token != "" {
		req.Header.Set(meshtoken.Header, token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestAuthzGroupGate(t *testing.T) {
	p, gw, rogue := tokenProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()
	now := time.Now()
	mint := func(s *meshtoken.Signer, id cert.Identity, host string, at time.Time) string {
		tok, err := s.Mint(id, host, at)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	const sonarr = "sonarr.gw.mesh.internal"
	cases := []struct {
		name   string
		group  string
		host   string
		token  string
		status int
	}{
		{"admin device, admins gate", "admins", sonarr, mint(gw, laptop, sonarr, now), 200},
		{"media device, admins gate", "admins", sonarr, mint(gw, tvDev, sonarr, now), 403},
		{"media device, media gate", "media", sonarr, mint(gw, tvDev, sonarr, now), 200},
		{"no token (pod network)", "admins", sonarr, "", 401},
		{"rogue issuer", "admins", sonarr, mint(rogue, laptop, sonarr, now), 401},
		{"replayed at another host", "admins", "argocd.gw.mesh.internal", mint(gw, laptop, sonarr, now), 401},
		{"expired", "admins", sonarr, mint(gw, laptop, sonarr, now.Add(-meshtoken.TTL-meshtoken.Leeway-time.Second)), 401},
		{"host with port in X-Original-URL", "admins", sonarr + ":80", mint(gw, laptop, sonarr, now), 200},
		{"no group: misconfigured Ingress", "", sonarr, mint(gw, laptop, sonarr, now), 400},
	}
	for _, tc := range cases {
		resp := authz(t, ts, tc.group, tc.host, tc.token)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
		if tc.status == 200 && (resp.Header.Get("X-Auth-Request-Preferred-Username") == "" || resp.Header.Get("X-Auth-Request-Groups") == "") {
			t.Errorf("%s: identity response headers missing: %v", tc.name, resp.Header)
		}
	}

	// Without X-Original-URL (a direct call) the request Host is the
	// audience; the bridge Service name is not what any token names.
	if resp := authz(t, ts, "admins", "", mint(gw, laptop, sonarr, now)); resp.StatusCode != 401 {
		t.Errorf("direct call without X-Original-URL: %d, want 401", resp.StatusCode)
	}

	// No gateway pinned: the gate is closed, not open.
	p2 := testProvider(t)
	ts2 := httptest.NewServer(p2.Handler())
	defer ts2.Close()
	if resp := authz(t, ts2, "admins", sonarr, mint(gw, laptop, sonarr, now)); resp.StatusCode != 401 {
		t.Errorf("unpinned provider: %d, want 401", resp.StatusCode)
	}
}

// A client that opted in gets a code for the device without the wallet
// page; the identity is the device, not a person. A client that did
// not, or a refused token, sees the wallet page as before.
func TestAuthorizeDeviceLogin(t *testing.T) {
	p, gw, rogue := tokenProvider(t)
	ts := httptest.NewServer(p.Handler())
	defer ts.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	verifier, challenge := pkce()
	get := func(clientID, redirect, token string) *http.Response {
		q := url.Values{
			"client_id": {clientID}, "redirect_uri": {redirect}, "response_type": {"code"},
			"state": {"s"}, "nonce": {"n"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
		}
		req, _ := http.NewRequest("GET", ts.URL+"/authorize?"+q.Encode(), nil)
		req.Host = "auth.gw.mesh.internal"
		if token != "" {
			req.Header.Set(meshtoken.Header, token)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	const auth = "auth.gw.mesh.internal"
	tok, _ := gw.Mint(laptop, auth, time.Now())

	// Opted-in client + valid token: straight to the redirect with a code.
	resp := get(testClient, testRedirect, tok)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("device login: %d, want 303", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" || loc.Query().Get("state") != "s" {
		t.Fatalf("redirect %s", loc)
	}
	tr, status := exchange(t, ts, code, verifier)
	if status != 200 {
		t.Fatalf("exchange: %d", status)
	}
	claims := verifyJWT(t, p.PublicKey(), tr.IDToken)
	if claims["sub"] != string(laptop.Key) || claims["preferred_username"] != "marius-mac" || claims["nonce"] != "n" {
		t.Errorf("claims: %v", claims)
	}
	if g, _ := claims["groups"].([]any); len(g) != 1 || g[0] != "admins" {
		t.Errorf("groups: %v", claims["groups"])
	}

	// Everything else renders the wallet page (200, HTML).
	for name, resp := range map[string]*http.Response{
		"client not opted in":  get("jellyfin", "http://jellyfin.gw.mesh.internal/sso/OID/redirect/wallet", tok),
		"no token":             get(testClient, testRedirect, ""),
		"rogue issuer":         get(testClient, testRedirect, func() string { s, _ := rogue.Mint(laptop, auth, time.Now()); return s }()),
		"token for other host": get(testClient, testRedirect, func() string { s, _ := gw.Mint(laptop, "sonarr.gw.mesh.internal", time.Now()); return s }()),
	} {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), "login_nonce") {
			t.Errorf("%s: %d, want the wallet page", name, resp.StatusCode)
		}
	}
}
