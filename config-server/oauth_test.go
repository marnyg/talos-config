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

	"github.com/marnyg/talos-config/config-server/deviceflow"
)

// skipSlowDown backdates the store's clock view so the next poll is not
// throttled by the RFC 8628 interval (store unit tests live in the
// deviceflow package; HTTP tests here only need to get past it).
func skipSlowDown(s *deviceflow.Store) {
	s.Now = func() time.Time { return time.Now().Add(2 * deviceflow.PollInterval) }
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
		store:       deviceflow.NewStore(),
		sessions:    newSessionStore(),
		requireAuth: true,
		clientID:    "talos-pxe",
		adminToken:  "test-admin-token",
	}
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
	if _, errCode := pollToken(); errCode != deviceflow.ErrCodeAuthorizationPending {
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
	skipSlowDown(s.store)
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
