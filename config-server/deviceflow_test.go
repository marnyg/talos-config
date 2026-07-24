package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock lets tests step time.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore() (*authStore, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newAuthStore()
	s.now = clock.now
	return s, clock
}

func TestDeviceFlowHappyPath(t *testing.T) {
	s, clock := newTestStore()

	da := s.begin("talos-pxe", map[string]string{"mac": "b0-41-6f-15-3b-8f", "uuid": "abc"})

	if _, errCode := s.poll(da.DeviceCode); errCode != errAuthorizationPending {
		t.Fatalf("expected authorization_pending, got %q", errCode)
	}

	if err := s.approve(da.UserCode); err != nil {
		t.Fatalf("approve: %v", err)
	}

	clock.advance(pollInterval)
	token, errCode := s.poll(da.DeviceCode)
	if errCode != "" || token == "" {
		t.Fatalf("expected token, got err=%q", errCode)
	}

	// Token is bound to the MAC (normalization: dashes vs colons).
	if err := s.validate(token, "b0:41:6f:15:3b:8f"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := s.validate(token, "de:ad:be:ef:00:00"); err == nil {
		t.Fatal("expected mac mismatch error")
	}

	// Single use.
	s.consume(token)
	if err := s.validate(token, "b0:41:6f:15:3b:8f"); err == nil {
		t.Fatal("expected consumed token to be rejected")
	}

	// Device code is gone after minting.
	if _, errCode := s.poll(da.DeviceCode); errCode != errInvalidGrant {
		t.Fatalf("expected invalid_grant after redemption, got %q", errCode)
	}
}

func TestDeviceFlowDenyAndSlowDown(t *testing.T) {
	s, clock := newTestStore()
	da := s.begin("talos-pxe", nil)

	if _, errCode := s.poll(da.DeviceCode); errCode != errAuthorizationPending {
		t.Fatalf("expected authorization_pending, got %q", errCode)
	}
	// Immediate re-poll violates the interval.
	if _, errCode := s.poll(da.DeviceCode); errCode != errSlowDown {
		t.Fatalf("expected slow_down, got %q", errCode)
	}

	if err := s.deny(da.UserCode); err != nil {
		t.Fatalf("deny: %v", err)
	}
	clock.advance(pollInterval)
	if _, errCode := s.poll(da.DeviceCode); errCode != errAccessDenied {
		t.Fatalf("expected access_denied, got %q", errCode)
	}
}

func TestDeviceAuthExpiry(t *testing.T) {
	s, clock := newTestStore()
	da := s.begin("talos-pxe", nil)

	clock.advance(deviceAuthTTL + time.Second)
	if _, errCode := s.poll(da.DeviceCode); errCode != errInvalidGrant {
		t.Fatalf("expected invalid_grant for expired auth, got %q", errCode)
	}
	if err := s.approve(da.UserCode); err == nil {
		t.Fatal("expected approve of expired code to fail")
	}
}

func TestTokenNotBoundWithoutMAC(t *testing.T) {
	s, clock := newTestStore()
	da := s.begin("talos-pxe", map[string]string{"uuid": "abc"}) // no mac sent
	if err := s.approve(da.UserCode); err != nil {
		t.Fatal(err)
	}
	clock.advance(pollInterval)
	token, _ := s.poll(da.DeviceCode)
	// Without a mac identity there is nothing to bind against.
	if err := s.validate(token, "any:mac:at:all:00:00"); err != nil {
		t.Fatalf("expected unbound token to validate, got %v", err)
	}
}

// newTestServer builds a server over a synthetic talos root with one machine.
func newTestServer(t *testing.T) *server {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "machines", "aa-bb-cc-dd-ee-ff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := "version: v1alpha1\nmachine:\n  type: worker\n"
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := "ip: 127.0.0.1\nconfig: base.yaml\npatches: []\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	return &server{
		root:        root,
		store:       newAuthStore(),
		requireAuth: true,
		clientID:    "talos-pxe",
		adminToken:  "test-admin-token",
	}
}

func (s *server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", s.handleConfig)
	mux.HandleFunc("POST /device/code", s.handleDeviceCode)
	mux.HandleFunc("POST /token", s.handleToken)
	mux.HandleFunc("GET /verify", s.handleVerifyPage)
	mux.HandleFunc("POST /verify", s.handleVerifyPost)
	return mux
}

func TestHTTPFullFlow(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	// 1. Unauthenticated config fetch is rejected.
	resp, err := http.Get(ts.URL + "/config?mac=aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated fetch: expected 401, got %d", resp.StatusCode)
	}

	// 2. Machine starts device flow, sending its identity.
	resp, err = http.PostForm(ts.URL+"/device/code", url.Values{
		"client_id": {"talos-pxe"},
		"mac":       {"aa:bb:cc:dd:ee:ff"},
		"uuid":      {"1234-5678"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var dc struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 3. Polling before approval → authorization_pending.
	pollToken := func() (string, string) {
		resp, err := http.PostForm(ts.URL+"/token", url.Values{
			"grant_type":  {deviceCodeGrantType},
			"device_code": {dc.DeviceCode},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.AccessToken, body.Error
	}
	if _, errCode := pollToken(); errCode != errAuthorizationPending {
		t.Fatalf("expected authorization_pending, got %q", errCode)
	}

	// 4. Wrong admin token cannot approve.
	resp, err = http.PostForm(ts.URL+"/verify", url.Values{
		"user_code": {dc.UserCode}, "admin_token": {"wrong"}, "action": {"approve"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong admin token: expected 403, got %d", resp.StatusCode)
	}

	// 5. Admin approves.
	resp, err = http.PostForm(ts.URL+"/verify", url.Values{
		"user_code": {dc.UserCode}, "admin_token": {"test-admin-token"}, "action": {"approve"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d", resp.StatusCode)
	}

	// 6. Next poll gets a token (bypass the slow-down interval).
	s.store.mu.Lock()
	s.store.byDeviceCode[dc.DeviceCode].lastPoll = time.Time{}
	s.store.mu.Unlock()
	token, errCode := pollToken()
	if errCode != "" || token == "" {
		t.Fatalf("expected token, got err=%q", errCode)
	}

	// 7. Token cannot fetch another machine's config.
	fetch := func(mac string) int {
		req, _ := http.NewRequest("GET", ts.URL+"/config?mac="+url.QueryEscape(mac), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := fetch("11:22:33:44:55:66"); code != http.StatusUnauthorized {
		t.Fatalf("cross-machine fetch: expected 401, got %d", code)
	}

	// 8. Bound machine fetch succeeds…
	req, _ := http.NewRequest("GET", ts.URL+"/config?mac=aa:bb:cc:dd:ee:ff", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bound fetch: expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "version: v1alpha1") {
		t.Fatalf("unexpected config body: %q", body)
	}

	// 9. …exactly once.
	if code := fetch("aa:bb:cc:dd:ee:ff"); code != http.StatusUnauthorized {
		t.Fatalf("token reuse: expected 401, got %d", code)
	}
}
