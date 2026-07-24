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
	_, ts := newStatusServer(t)

	code, body := get(t, http.DefaultClient, ts.URL+"/status")
	if code != http.StatusOK {
		t.Fatalf("got %d", code)
	}
	for _, leak := range []string{testMAC, "Machines", "bootstrap", wellKnownAddr} {
		if strings.Contains(body, leak) {
			t.Errorf("logged-out page leaks %q", leak)
		}
	}
	if !strings.Contains(body, "Sign in") {
		t.Error("login prompt missing")
	}
}

func TestStatusDisabledWithoutAdminAddrs(t *testing.T) {
	s := newTestServer(t)
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
