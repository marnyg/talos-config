package main

// SIWE session login for the read-only /status page. The admin signs a
// server-issued single-use nonce (EIP-191 personal_sign, an ordinary
// auth message — NOT the master-key message) and gets an HttpOnly
// session cookie. All state is in-memory: a restart logs everyone out,
// consistent with the rest of the server.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is not recoverable
	}
	return hex.EncodeToString(b)
}

const (
	loginNonceTTL = 5 * time.Minute
	sessionTTL    = 12 * time.Hour
)

// loginMessage is the canonical text the admin signs to open a status
// session. Distinct prefix from both the approval message and the
// master-key message — signing it grants a session, nothing more.
func loginMessage(nonce string) string {
	return fmt.Sprintf("talos config-server status login\nnonce: %s", nonce)
}

type session struct {
	addr    string // lowercase 0x wallet address
	expires time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	nonces   map[string]time.Time // nonce -> expiry
	sessions map[string]session   // token -> session
	now      func() time.Time     // injectable for tests
}

func newSessionStore() *sessionStore {
	return &sessionStore{
		nonces:   map[string]time.Time{},
		sessions: map[string]session{},
		now:      time.Now,
	}
}

// issueNonce mints a login challenge nonce.
func (s *sessionStore) issueNonce() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	n := randomHex(16)
	s.nonces[n] = s.now().Add(loginNonceTTL)
	return n
}

// redeemNonce consumes an outstanding nonce. Single-use: a replayed
// login signature fails here.
func (s *sessionStore) redeemNonce(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	if _, ok := s.nonces[n]; !ok {
		return false
	}
	delete(s.nonces, n)
	return true
}

// create opens a session for addr and returns the cookie token.
func (s *sessionStore) create(addr string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := randomHex(32)
	s.sessions[t] = session{addr: addr, expires: s.now().Add(sessionTTL)}
	return t
}

// addrFor resolves a session token to its wallet address.
func (s *sessionStore) addrFor(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	sess, ok := s.sessions[token]
	if !ok {
		return "", false
	}
	return sess.addr, true
}

// drop ends a session (logout).
func (s *sessionStore) drop(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// expireLocked prunes expired nonces and sessions. Caller holds mu.
func (s *sessionStore) expireLocked() {
	now := s.now()
	for n, exp := range s.nonces {
		if now.After(exp) {
			delete(s.nonces, n)
		}
	}
	for t, sess := range s.sessions {
		if now.After(sess.expires) {
			delete(s.sessions, t)
		}
	}
}
