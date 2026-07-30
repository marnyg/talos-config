// Package deviceflow is the OAuth2 device-flow (RFC 8628) state
// machine: in-flight device authorizations and the single-use,
// identity-bound tokens they mint on approval. All state is in-memory
// by design: if the server restarts mid-approval, Talos restarts the
// flow and a new user code appears on the machine console.
//
// The package knows nothing about HTTP or about what a config *is* —
// the handlers in the main package own the wire; this owns the grants.
package deviceflow

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// AuthTTL is how long a pending device authorization stays
	// approvable before the machine must restart the flow.
	AuthTTL = 10 * time.Minute
	// TokenTTL is how long a minted (unused) token stays redeemable.
	TokenTTL = 10 * time.Minute
	// PollInterval is the minimum spacing between token polls; faster
	// polling gets slow_down (RFC 8628 §3.5).
	PollInterval = 5 * time.Second
)

// OAuth error codes returned from the token endpoint (RFC 8628 §3.5).
const (
	ErrCodeAuthorizationPending = "authorization_pending"
	ErrCodeSlowDown             = "slow_down"
	ErrCodeExpiredToken         = "expired_token"
	ErrCodeAccessDenied         = "access_denied"
	ErrCodeInvalidGrant         = "invalid_grant"
)

// Kind separates what a grant is *for*. Set server-side at flow start,
// never from client input: a machine token must never redeem a mesh
// device config and vice versa — the machine config carries the node's
// mesh identity and disk keys, the device config carries a device mesh
// identity, and the only thing keeping them apart is this field.
type Kind string

const (
	KindMachine Kind = "machine"
	KindTV      Kind = "tv"
)

type authStatus int

const (
	statusPending authStatus = iota
	statusApproved
	statusDenied
)

// Auth is one in-flight device authorization.
type Auth struct {
	DeviceCode string
	UserCode   string
	Nonce      string // uniquifies the SIWE approval message
	ClientID   string
	Kind       Kind
	// Identity holds the extra variables Talos sent in the device auth
	// request (talos.config.oauth.extra_variable=uuid,mac,serial).
	Identity  map[string]string
	CreatedAt time.Time
	ExpiresAt time.Time

	lastPoll time.Time
	status   authStatus
}

// grant is a minted access token: single-use, bound to the identity
// captured at device-auth time.
type grant struct {
	Kind      Kind
	Identity  map[string]string
	ExpiresAt time.Time
	used      bool
}

// Store holds every in-flight authorization and minted token.
type Store struct {
	mu           sync.Mutex
	byDeviceCode map[string]*Auth
	byUserCode   map[string]*Auth
	tokens       map[string]*grant

	// Now is the store's clock; injectable so tests can step time and
	// skip the poll interval.
	Now func() time.Time
}

// NewStore returns an empty store on the real clock.
func NewStore() *Store {
	return &Store{
		byDeviceCode: make(map[string]*Auth),
		byUserCode:   make(map[string]*Auth),
		tokens:       make(map[string]*grant),
		Now:          time.Now,
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

// normalizeMAC lowercases and converts dashes to colons, so the MAC a
// grant was bound to and the MAC a request names compare equal
// regardless of which spelling either side used.
func normalizeMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(mac, "-", ":"))
}

// Begin registers a new device authorization and returns it.
func (s *Store) Begin(kind Kind, clientID string, identity map[string]string) *Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	now := s.Now()
	da := &Auth{
		DeviceCode: randomHex(32),
		UserCode:   newUserCode(),
		Nonce:      randomHex(16),
		ClientID:   clientID,
		Kind:       kind,
		Identity:   identity,
		CreatedAt:  now,
		ExpiresAt:  now.Add(AuthTTL),
		status:     statusPending,
	}
	s.byDeviceCode[da.DeviceCode] = da
	s.byUserCode[da.UserCode] = da
	return da
}

// Pending returns all pending authorizations, for the verification page.
func (s *Store) Pending() []*Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	var out []*Auth
	for _, da := range s.byUserCode {
		if da.status == statusPending {
			out = append(out, da)
		}
	}
	return out
}

// ErrUnknownUserCode is returned when a user code names no pending
// authorization — unknown, expired, or already decided.
var ErrUnknownUserCode = errors.New("unknown or expired user code")

// NonceFor returns the nonce of a pending authorization, for rebuilding
// the canonical approval message server-side.
func (s *Store) NonceFor(userCode string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	da, ok := s.byUserCode[userCode]
	if !ok || da.status != statusPending {
		return "", ErrUnknownUserCode
	}
	return da.Nonce, nil
}

// Approve marks a pending authorization approved.
func (s *Store) Approve(userCode string) error { return s.decide(userCode, statusApproved) }

// Deny marks a pending authorization denied.
func (s *Store) Deny(userCode string) error { return s.decide(userCode, statusDenied) }

func (s *Store) decide(userCode string, status authStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	da, ok := s.byUserCode[userCode]
	if !ok || da.status != statusPending {
		return ErrUnknownUserCode
	}
	da.status = status
	return nil
}

// Poll implements the token endpoint's device_code grant. On success it
// mints a single-use token and forgets the device authorization.
func (s *Store) Poll(deviceCode string) (token string, errCode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()

	da, ok := s.byDeviceCode[deviceCode]
	if !ok {
		return "", ErrCodeInvalidGrant
	}

	now := s.Now()
	if !da.lastPoll.IsZero() && now.Sub(da.lastPoll) < PollInterval {
		da.lastPoll = now
		return "", ErrCodeSlowDown
	}
	da.lastPoll = now

	switch da.status {
	case statusPending:
		return "", ErrCodeAuthorizationPending
	case statusDenied:
		s.removeLocked(da)
		return "", ErrCodeAccessDenied
	}

	// Approved: mint token, retire the device auth.
	s.removeLocked(da)
	token = randomHex(32)
	s.tokens[token] = &grant{
		Kind:      da.Kind,
		Identity:  da.Identity,
		ExpiresAt: now.Add(TokenTTL),
	}
	return token, ""
}

var (
	// ErrBadToken is returned for tokens that are invalid, expired, or
	// already used.
	ErrBadToken = errors.New("invalid, expired, or already-used token")
	// ErrWrongTarget is returned when a valid token is presented for
	// something it was not granted for (wrong kind, wrong machine).
	ErrWrongTarget = errors.New("token not valid for requested machine")
)

// Validate checks that token exists, is unused, unexpired, is a
// *machine* grant, and — if the grant captured a mac identity — that it
// matches the requested MAC. It does not consume the token; call
// Consume after successfully serving.
func (s *Store) Validate(token, mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.tokens[token]
	if !ok || g.used || s.Now().After(g.ExpiresAt) {
		return ErrBadToken
	}
	if g.Kind != KindMachine {
		return ErrWrongTarget
	}
	if bound, ok := g.Identity["mac"]; ok && bound != "" {
		if normalizeMAC(bound) != normalizeMAC(mac) {
			return ErrWrongTarget
		}
	}
	return nil
}

// MeshDeviceFor validates a TV-kind token and returns the mesh device
// name it was bound to at flow start. It does not consume the token;
// call Consume after successfully serving the config.
func (s *Store) MeshDeviceFor(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.tokens[token]
	if !ok || g.used || s.Now().After(g.ExpiresAt) {
		return "", ErrBadToken
	}
	if g.Kind != KindTV {
		return "", ErrWrongTarget
	}
	name := g.Identity["mesh_device"]
	if name == "" {
		return "", ErrBadToken
	}
	return name, nil
}

// Consume marks the token used. Single-use: subsequent Validates fail.
func (s *Store) Consume(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.tokens[token]; ok {
		g.used = true
	}
}

// expireLocked drops expired device auths and tokens. Caller holds mu.
func (s *Store) expireLocked() {
	now := s.Now()
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

func (s *Store) removeLocked(da *Auth) {
	delete(s.byDeviceCode, da.DeviceCode)
	delete(s.byUserCode, da.UserCode)
}
