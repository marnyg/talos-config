package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/marnyg/talos-config/config-server/deviceflow"
)

// testMAC is the machine declared by newTestServer.
const testMAC = "aa:bb:cc:dd:ee:ff"

// otherTestKey is private key 0x...02 — deliberately not allowlisted.
func otherTestKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	b := make([]byte, 32)
	b[31] = 2
	return secp256k1.PrivKeyFromBytes(b)
}

func newStatusServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	s := newTestServer(t)
	s.adminAddrs = []string{wellKnownAddr}
	ts := httptest.NewServer(s.mux())
	t.Cleanup(ts.Close)
	return s, ts
}

func get(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

var nonceRe = regexp.MustCompile(`name="nonce" value="([0-9a-f]+)"`)

// loginNonce fetches the login page and extracts the challenge nonce.
func loginNonce(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	code, body := get(t, c, base+"/status")
	if code != http.StatusOK {
		t.Fatalf("login page: got %d", code)
	}
	m := nonceRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no nonce in login page:\n%s", body)
	}
	return m[1]
}

func TestStatusLeaksNothingLoggedOut(t *testing.T) {
	s, ts := newStatusServer(t)
	da := s.store.Begin(deviceflow.KindMachine, "talos-pxe", map[string]string{"mac": testMAC, "uuid": "1234-5678"})

	code, body := get(t, http.DefaultClient, ts.URL+"/status")
	if code != http.StatusOK {
		t.Fatalf("got %d", code)
	}
	for _, leak := range []string{testMAC, "Machines", "bootstrap", wellKnownAddr, da.UserCode, "1234-5678"} {
		if strings.Contains(body, leak) {
			t.Errorf("logged-out page leaks %q", leak)
		}
	}
	if !strings.Contains(body, "Sign in") {
		t.Error("login prompt missing")
	}
}

func TestStatusDisabledWithoutAdminCreds(t *testing.T) {
	s := newTestServer(t)
	s.adminToken = "" // no wallet allowlist, no break-glass token
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	for _, req := range [][2]string{{"GET", "/status"}, {"POST", "/status/login"}, {"POST", "/status/logout"}} {
		r, _ := http.NewRequest(req[0], ts.URL+req[1], nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s without allowlist: got %d, want 404", req[0], req[1], resp.StatusCode)
		}
	}
}

func TestStatusLoginFlow(t *testing.T) {
	s, ts := newStatusServer(t)
	s.recordFetch(testMAC)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	nonce := loginNonce(t, client, ts.URL)
	sig := personalSign(t, testKey(t), loginMessage(nonce))
	resp, err := client.PostForm(ts.URL+"/status/login", url.Values{
		"nonce": {nonce}, "signature": {sig},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK { // after following the 303
		t.Fatalf("login: got %d", resp.StatusCode)
	}

	code, body := get(t, client, ts.URL+"/status")
	if code != http.StatusOK {
		t.Fatalf("status after login: got %d", code)
	}
	for _, want := range []string{testMAC, "signed in as " + wellKnownAddr, "Machines"} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q", want)
		}
	}

	// Logout kills the session.
	resp, err = client.PostForm(ts.URL+"/status/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, body := get(t, client, ts.URL+"/status"); strings.Contains(body, testMAC) {
		t.Error("machine info visible after logout")
	}
}

func TestStatusLoginRejectsWrongWallet(t *testing.T) {
	_, ts := newStatusServer(t)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	nonce := loginNonce(t, client, ts.URL)
	otherSig := personalSign(t, otherTestKey(t), loginMessage(nonce))
	resp, err := client.PostForm(ts.URL+"/status/login", url.Values{
		"nonce": {nonce}, "signature": {otherSig},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong wallet: got %d, want 403", resp.StatusCode)
	}
	if code, body := get(t, client, ts.URL+"/status"); code != http.StatusOK || strings.Contains(body, testMAC) {
		t.Error("wrong wallet still ended up with a session")
	}
}

func TestStatusLoginWithAdminToken(t *testing.T) {
	_, ts := newStatusServer(t) // newTestServer sets adminToken

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Wrong token: no session.
	resp, err := client.PostForm(ts.URL+"/status/login", url.Values{"admin_token": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong token: got %d, want 403", resp.StatusCode)
	}

	// Correct token opens a break-glass session.
	resp, err = client.PostForm(ts.URL+"/status/login", url.Values{"admin_token": {"test-admin-token"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	code, body := get(t, client, ts.URL+"/status")
	if code != http.StatusOK || !strings.Contains(body, testMAC) {
		t.Fatalf("token session: got %d, machine visible: %v", code, strings.Contains(body, testMAC))
	}
	if !strings.Contains(body, tokenSessionAddr) {
		t.Error("break-glass session not labeled as such")
	}
}

func TestVerifyRedirectsToStatus(t *testing.T) {
	_, ts := newStatusServer(t)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(ts.URL + "/verify?user_code=ABCD-1234")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /verify: got %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/status?user_code=ABCD-1234" {
		t.Fatalf("redirect location: got %q", loc)
	}
}

// login opens a wallet session and returns a cookie-carrying client.
func login(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	nonce := loginNonce(t, client, ts.URL)
	resp, err := client.PostForm(ts.URL+"/status/login", url.Values{
		"nonce": {nonce}, "signature": {personalSign(t, testKey(t), loginMessage(nonce))},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return client
}

func TestStatusShowsPendingApprovals(t *testing.T) {
	s, ts := newStatusServer(t)
	da := s.store.Begin(deviceflow.KindMachine, "talos-pxe", map[string]string{"mac": testMAC, "uuid": "1234-5678"})

	client := login(t, ts)
	code, body := get(t, client, ts.URL+"/status")
	if code != http.StatusOK {
		t.Fatalf("status: got %d", code)
	}
	for _, want := range []string{da.UserCode, "1234-5678", "Pending approvals", `action="/verify"`} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q", want)
		}
	}

	// Approving from the dashboard (admin token path) re-renders it.
	resp, err := client.PostForm(ts.URL+"/verify", url.Values{
		"user_code": {da.UserCode}, "action": {"approve"}, "admin_token": {"test-admin-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: got %d", resp.StatusCode)
	}
	for _, want := range []string{da.UserCode + " approved", "Cluster status"} {
		if !strings.Contains(body, want) {
			t.Errorf("post-approve page missing %q", want)
		}
	}
}

func TestStatusShowsUnsealFormWhenSealed(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	s := &server{
		root:       m.root,
		store:      deviceflow.NewStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		hub:        m,
	}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	client := login(t, ts)
	code, body := get(t, client, ts.URL+"/status")
	if code != http.StatusOK {
		t.Fatalf("status: got %d", code)
	}
	for _, want := range []string{"SEALED", `action="/unseal"`} {
		if !strings.Contains(body, want) {
			t.Errorf("sealed status page missing %q", want)
		}
	}

	// Unsealing from the dashboard re-renders it, form gone.
	resp, err := client.PostForm(ts.URL+"/unseal", url.Values{"signature": {unsealSig(t)}})
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unseal: got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "hub unsealed") || strings.Contains(body, `action="/unseal"`) {
		t.Error("post-unseal page should confirm and drop the unseal form")
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStatusLoginNonceSingleUse(t *testing.T) {
	_, ts := newStatusServer(t)

	// Don't follow redirects: the Set-Cookie lives on the 303 itself.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	nonce := loginNonce(t, client, ts.URL)
	sig := personalSign(t, testKey(t), loginMessage(nonce))

	post := func() *http.Response {
		resp, err := client.PostForm(ts.URL+"/status/login", url.Values{
			"nonce": {nonce}, "signature": {sig},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	first := post()
	if first.StatusCode != http.StatusSeeOther || first.Header.Get("Set-Cookie") == "" {
		t.Fatalf("first login: got %d, cookie %q", first.StatusCode, first.Header.Get("Set-Cookie"))
	}

	// Replay: nonce burned — no redirect, no cookie.
	replay := post()
	if replay.StatusCode == http.StatusSeeOther || replay.Header.Get("Set-Cookie") != "" {
		t.Fatalf("replayed login succeeded: got %d, cookie %q", replay.StatusCode, replay.Header.Get("Set-Cookie"))
	}
}

// TestStatusShowsMeshMembers: with the mesh configured, the dashboard
// carries a mesh seal-state line and, once unsealed, the full derived
// membership — offline members included. Also pins the soft-refresh
// contract: a #live region and no meta-refresh reload.
func TestStatusShowsMeshMembers(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	mesh, _ := testNebManager(t, m.root, []string{"laptop"})
	m.mesh = mesh
	s := &server{
		root:       m.root,
		store:      deviceflow.NewStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		hub:        m,
	}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	client := login(t, ts)
	code, body := get(t, client, ts.URL+"/status")
	if code != http.StatusOK {
		t.Fatalf("status: got %d", code)
	}
	if !strings.Contains(body, ">mesh</th>") || !strings.Contains(body, "sealed") {
		t.Error("sealed page should carry the mesh seal-state line")
	}
	if strings.Contains(body, "<h2>Mesh</h2>") && strings.Contains(body, "laptop") {
		t.Error("sealed page must not list mesh members")
	}

	if resp, err := client.PostForm(ts.URL+"/unseal", url.Values{"signature": {unsealSig(t)}}); err != nil {
		t.Fatal(err)
	} else {
		readBody(t, resp)
	}

	code, body = get(t, client, ts.URL+"/status")
	if code != http.StatusOK {
		t.Fatalf("status after unseal: got %d", code)
	}
	// "+" renders as &#43; under html/template's text escaping.
	for _, want := range []string{"<h2>Mesh</h2>", "laptop", "lighthouse&#43;relay", "aa-bb-cc-dd-ee-ff"} {
		if !strings.Contains(body, want) {
			t.Errorf("unsealed page missing %q", want)
		}
	}
	if !strings.Contains(body, `id="live"`) {
		t.Error("dynamic region #live missing")
	}
	if strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("meta refresh should be gone — the poller replaced it")
	}
}
