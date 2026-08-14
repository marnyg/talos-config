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

// TestMeshEnrollPayloadRoundTrip: the ADR-0012 device-flow contract —
// approval stashes the minted config on the grant, the token redeems
// it, Consume makes it single-use.
func TestMeshEnrollPayloadRoundTrip(t *testing.T) {
	s, _ := newTestStore()
	da := s.Begin(KindMeshEnroll, "mesh-enroll", map[string]string{
		"pubkey": "aa", "pubkey_fp": "fp", "proposed_name": "tv", "proposed_group": "media",
	})

	// Operator edits land on the pending auth (rendered on /status).
	if err := s.UpdateIdentity(da.UserCode, map[string]string{"name": "livingroom", "group": "media"}); err != nil {
		t.Fatalf("update identity: %v", err)
	}
	pending := s.Pending()
	if len(pending) != 1 || pending[0].Identity["name"] != "livingroom" {
		t.Fatalf("identity after update = %+v", pending)
	}

	payload := []byte("minted nebula config")
	if err := s.ApproveWithPayload(da.UserCode, payload); err != nil {
		t.Fatalf("approve: %v", err)
	}
	// Decided flows are frozen: no further identity edits.
	if err := s.UpdateIdentity(da.UserCode, map[string]string{"name": "x"}); err == nil {
		t.Fatal("expected UpdateIdentity to fail once decided")
	}

	token, errCode := s.Poll(da.DeviceCode)
	if errCode != "" || token == "" {
		t.Fatalf("poll after approval: err=%q", errCode)
	}
	got, err := s.MeshEnrollPayload(token)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("payload = %q, %v; want %q", got, err, payload)
	}
	// Not consumed yet: readable again (redeem is Consume's job).
	if _, err := s.MeshEnrollPayload(token); err != nil {
		t.Fatalf("second read before consume: %v", err)
	}
	s.Consume(token)
	if _, err := s.MeshEnrollPayload(token); err == nil {
		t.Fatal("expected consumed token to be rejected")
	}
}

// TestMeshEnrollPayloadGuards: a machine token never redeems as a mesh
// config, and a mesh flow approved WITHOUT a payload (the generic
// Approve path) redeems to an error, not an empty config.
func TestMeshEnrollPayloadGuards(t *testing.T) {
	s, clock := newTestStore()

	m := s.Begin(KindMachine, "talos-pxe", nil)
	if err := s.Approve(m.UserCode); err != nil {
		t.Fatal(err)
	}
	mTok, _ := s.Poll(m.DeviceCode)
	if _, err := s.MeshEnrollPayload(mTok); err != ErrWrongTarget {
		t.Fatalf("machine token as mesh payload: err = %v, want ErrWrongTarget", err)
	}

	e := s.Begin(KindMeshEnroll, "mesh-enroll", nil)
	if err := s.Approve(e.UserCode); err != nil { // no payload
		t.Fatal(err)
	}
	clock.advance(PollInterval)
	eTok, _ := s.Poll(e.DeviceCode)
	if _, err := s.MeshEnrollPayload(eTok); err != ErrBadToken {
		t.Fatalf("payload-less mesh token: err = %v, want ErrBadToken", err)
	}
}

// TestUpdateIdentityUnknownCode: a stale or fabricated user code is an
// error, not a silent no-op.
func TestUpdateIdentityUnknownCode(t *testing.T) {
	s, _ := newTestStore()
	if err := s.UpdateIdentity("NOPE-CODE", map[string]string{"name": "x"}); err == nil {
		t.Fatal("expected an error for an unknown user code")
	}
}
