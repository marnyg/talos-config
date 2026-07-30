package deviceflow

import (
	"testing"
	"time"
)

// fakeClock lets tests step time.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore() (*Store, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := NewStore()
	s.Now = clock.now
	return s, clock
}

func TestDeviceFlowHappyPath(t *testing.T) {
	s, clock := newTestStore()

	da := s.Begin(KindMachine, "talos-pxe", map[string]string{"mac": "b0-41-6f-15-3b-8f", "uuid": "abc"})

	if _, errCode := s.Poll(da.DeviceCode); errCode != ErrCodeAuthorizationPending {
		t.Fatalf("expected authorization_pending, got %q", errCode)
	}

	if err := s.Approve(da.UserCode); err != nil {
		t.Fatalf("approve: %v", err)
	}

	clock.advance(PollInterval)
	token, errCode := s.Poll(da.DeviceCode)
	if errCode != "" || token == "" {
		t.Fatalf("expected token, got err=%q", errCode)
	}

	// Token is bound to the MAC (normalization: dashes vs colons).
	if err := s.Validate(token, "b0:41:6f:15:3b:8f"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := s.Validate(token, "de:ad:be:ef:00:00"); err == nil {
		t.Fatal("expected mac mismatch error")
	}

	// Single use.
	s.Consume(token)
	if err := s.Validate(token, "b0:41:6f:15:3b:8f"); err == nil {
		t.Fatal("expected consumed token to be rejected")
	}

	// Device code is gone after minting.
	if _, errCode := s.Poll(da.DeviceCode); errCode != ErrCodeInvalidGrant {
		t.Fatalf("expected invalid_grant after redemption, got %q", errCode)
	}
}

func TestDeviceFlowDenyAndSlowDown(t *testing.T) {
	s, clock := newTestStore()
	da := s.Begin(KindMachine, "talos-pxe", nil)

	if _, errCode := s.Poll(da.DeviceCode); errCode != ErrCodeAuthorizationPending {
		t.Fatalf("expected authorization_pending, got %q", errCode)
	}
	// Immediate re-poll violates the interval.
	if _, errCode := s.Poll(da.DeviceCode); errCode != ErrCodeSlowDown {
		t.Fatalf("expected slow_down, got %q", errCode)
	}

	if err := s.Deny(da.UserCode); err != nil {
		t.Fatalf("deny: %v", err)
	}
	clock.advance(PollInterval)
	if _, errCode := s.Poll(da.DeviceCode); errCode != ErrCodeAccessDenied {
		t.Fatalf("expected access_denied, got %q", errCode)
	}
}

func TestDeviceAuthExpiry(t *testing.T) {
	s, clock := newTestStore()
	da := s.Begin(KindMachine, "talos-pxe", nil)

	clock.advance(AuthTTL + time.Second)
	if _, errCode := s.Poll(da.DeviceCode); errCode != ErrCodeInvalidGrant {
		t.Fatalf("expected invalid_grant for expired auth, got %q", errCode)
	}
	if err := s.Approve(da.UserCode); err == nil {
		t.Fatal("expected approve of expired code to fail")
	}
}

func TestTokenNotBoundWithoutMAC(t *testing.T) {
	s, clock := newTestStore()
	da := s.Begin(KindMachine, "talos-pxe", map[string]string{"uuid": "abc"}) // no mac sent
	if err := s.Approve(da.UserCode); err != nil {
		t.Fatal(err)
	}
	clock.advance(PollInterval)
	token, _ := s.Poll(da.DeviceCode)
	// Without a mac identity there is nothing to bind against.
	if err := s.Validate(token, "any:mac:at:all:00:00"); err != nil {
		t.Fatalf("expected unbound token to validate, got %v", err)
	}
}
