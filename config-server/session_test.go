package main

import (
	"testing"
	"time"
)

// fakeClock lets tests step time.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestSessionStore() (*sessionStore, *fakeClock) {
	c := &fakeClock{t: time.Now()}
	s := newSessionStore()
	s.now = c.now
	return s, c
}

func TestNonceSingleUse(t *testing.T) {
	s, _ := newTestSessionStore()
	n := s.issueNonce()
	if !s.redeemNonce(n) {
		t.Fatal("fresh nonce must redeem")
	}
	if s.redeemNonce(n) {
		t.Fatal("nonce redeemed twice")
	}
	if s.redeemNonce("never-issued") {
		t.Fatal("unknown nonce redeemed")
	}
}

func TestNonceExpiry(t *testing.T) {
	s, c := newTestSessionStore()
	n := s.issueNonce()
	c.advance(loginNonceTTL + time.Second)
	if s.redeemNonce(n) {
		t.Fatal("expired nonce redeemed")
	}
}

func TestSessionLifecycle(t *testing.T) {
	s, c := newTestSessionStore()
	tok := s.create(wellKnownAddr)

	addr, ok := s.addrFor(tok)
	if !ok || addr != wellKnownAddr {
		t.Fatalf("addrFor: got %q, %v", addr, ok)
	}
	if _, ok := s.addrFor("bogus"); ok {
		t.Fatal("unknown token resolved")
	}

	// Logout drops the session.
	s.drop(tok)
	if _, ok := s.addrFor(tok); ok {
		t.Fatal("dropped session still valid")
	}

	// Expiry drops the session.
	tok = s.create(wellKnownAddr)
	c.advance(sessionTTL + time.Second)
	if _, ok := s.addrFor(tok); ok {
		t.Fatal("expired session still valid")
	}
}
