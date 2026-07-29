package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// OAuth2 device flow (RFC 8628) state. All state is in-memory by design:
// if the server restarts mid-approval, Talos restarts the flow and a new
// user code appears on the machine console.

const (
	deviceAuthTTL = 10 * time.Minute
	tokenTTL      = 10 * time.Minute
	pollInterval  = 5 * time.Second
)

// OAuth error codes returned from the token endpoint (RFC 8628 §3.5).
const (
	errAuthorizationPending = "authorization_pending"
	errSlowDown             = "slow_down"
	errExpiredToken         = "expired_token"
	errAccessDenied         = "access_denied"
	errInvalidGrant         = "invalid_grant"
)

// authKind separates what a grant is *for*. Set server-side at flow
// start, never from client input: a machine token must never redeem a
// mesh device config and vice versa — the machine config carries wg
// keys and disk passphrases, the device config carries a mesh identity,
// and the only thing keeping them apart is this field.
type authKind string

const (
	authKindMachine authKind = "machine"
	authKindTV      authKind = "tv"
)

type authStatus int

const (
	statusPending authStatus = iota
	statusApproved
	statusDenied
)

// deviceAuth is one in-flight device authorization.
type deviceAuth struct {
	DeviceCode string
	UserCode   string
	Nonce      string // uniquifies the SIWE approval message
	ClientID   string
	Kind       authKind
	// Identity holds the extra variables Talos sent in the device auth
	// request (talos.config.oauth.extra_variable=uuid,mac,serial).
	Identity  map[string]string
	CreatedAt time.Time
	ExpiresAt time.Time

	lastPoll time.Time
	status   authStatus
}

// tokenGrant is a minted access token: single-use, bound to the identity
// captured at device-auth time.
type tokenGrant struct {
	Kind      authKind
	Identity  map[string]string
	ExpiresAt time.Time
	used      bool
}

type authStore struct {
	mu           sync.Mutex
	byDeviceCode map[string]*deviceAuth
	byUserCode   map[string]*deviceAuth
	tokens       map[string]*tokenGrant
	now          func() time.Time // injectable for tests
}

func newAuthStore() *authStore {
	return &authStore{
		byDeviceCode: make(map[string]*deviceAuth),
		byUserCode:   make(map[string]*deviceAuth),
		tokens:       make(map[string]*tokenGrant),
		now:          time.Now,
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is not recoverable
	}
	return hex.EncodeToString(b)
}

// userCodeCharset avoids vowels and ambiguous characters (RFC 8628 §6.1).
const userCodeCharset = "BCDFGHJKLMNPQRSTVWXZ"

func newUserCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, userCodeCharset[int(c)%len(userCodeCharset)])
	}
	return string(out)
}

// begin registers a new device authorization and returns it.
func (s *authStore) begin(kind authKind, clientID string, identity map[string]string) *deviceAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	now := s.now()
	da := &deviceAuth{
		DeviceCode: randomHex(32),
		UserCode:   newUserCode(),
		Nonce:      randomHex(16),
		ClientID:   clientID,
		Kind:       kind,
		Identity:   identity,
		CreatedAt:  now,
		ExpiresAt:  now.Add(deviceAuthTTL),
		status:     statusPending,
	}
	s.byDeviceCode[da.DeviceCode] = da
	s.byUserCode[da.UserCode] = da
	return da
}

// pending returns all pending authorizations, for the verification page.
func (s *authStore) pending() []*deviceAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	var out []*deviceAuth
	for _, da := range s.byUserCode {
		if da.status == statusPending {
			out = append(out, da)
		}
	}
	return out
}

var errUnknownUserCode = errors.New("unknown or expired user code")

// nonceFor returns the nonce of a pending authorization, for rebuilding
// the canonical approval message server-side.
func (s *authStore) nonceFor(userCode string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	da, ok := s.byUserCode[userCode]
	if !ok || da.status != statusPending {
		return "", errUnknownUserCode
	}
	return da.Nonce, nil
}

func (s *authStore) approve(userCode string) error { return s.decide(userCode, statusApproved) }
func (s *authStore) deny(userCode string) error    { return s.decide(userCode, statusDenied) }

func (s *authStore) decide(userCode string, status authStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	da, ok := s.byUserCode[userCode]
	if !ok || da.status != statusPending {
		return errUnknownUserCode
	}
	da.status = status
	return nil
}

// poll implements the token endpoint's device_code grant. On success it
// mints a single-use token and forgets the device authorization.
func (s *authStore) poll(deviceCode string) (token string, errCode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	da, ok := s.byDeviceCode[deviceCode]
	if !ok {
		return "", errInvalidGrant
	}

	now := s.now()
	if !da.lastPoll.IsZero() && now.Sub(da.lastPoll) < pollInterval {
		da.lastPoll = now
		return "", errSlowDown
	}
	da.lastPoll = now

	switch da.status {
	case statusPending:
		return "", errAuthorizationPending
	case statusDenied:
		s.removeLocked(da)
		return "", errAccessDenied
	}

	// Approved: mint token, retire the device auth.
	s.removeLocked(da)
	token = randomHex(32)
	s.tokens[token] = &tokenGrant{
		Kind:      da.Kind,
		Identity:  da.Identity,
		ExpiresAt: now.Add(tokenTTL),
	}
	return token, ""
}

var (
	errBadToken    = errors.New("invalid, expired, or already-used token")
	errWrongTarget = errors.New("token not valid for requested machine")
)

// validate checks that token exists, is unused, unexpired, is a
// *machine* grant, and — if the grant captured a mac identity — that it
// matches the requested MAC. It does not consume the token; call
// consume after successfully serving.
func (s *authStore) validate(token, mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.tokens[token]
	if !ok || g.used || s.now().After(g.ExpiresAt) {
		return errBadToken
	}
	if g.Kind != authKindMachine {
		return errWrongTarget
	}
	if bound, ok := g.Identity["mac"]; ok && bound != "" {
		if normalizeMAC(bound) != normalizeMAC(mac) {
			return errWrongTarget
		}
	}
	return nil
}

// meshDeviceFor validates a TV-kind token and returns the mesh device
// name it was bound to at flow start. It does not consume the token;
// call consume after successfully serving the config.
func (s *authStore) meshDeviceFor(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.tokens[token]
	if !ok || g.used || s.now().After(g.ExpiresAt) {
		return "", errBadToken
	}
	if g.Kind != authKindTV {
		return "", errWrongTarget
	}
	name := g.Identity["mesh_device"]
	if name == "" {
		return "", errBadToken
	}
	return name, nil
}

// consume marks the token used. Single-use: subsequent validates fail.
func (s *authStore) consume(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.tokens[token]; ok {
		g.used = true
	}
}

// expireLocked drops expired device auths and tokens. Caller holds mu.
func (s *authStore) expireLocked() {
	now := s.now()
	for _, da := range s.byDeviceCode {
		if now.After(da.ExpiresAt) {
			s.removeLocked(da)
		}
	}
	for t, g := range s.tokens {
		if now.After(g.ExpiresAt) {
			delete(s.tokens, t)
		}
	}
}

func (s *authStore) removeLocked(da *deviceAuth) {
	delete(s.byDeviceCode, da.DeviceCode)
	delete(s.byUserCode, da.UserCode)
}

// constantTimeEqual compares two strings without leaking length timing
// beyond equality of lengths.
func constantTimeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
