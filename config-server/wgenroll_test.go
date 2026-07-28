package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// newEnrollServer returns an unsealed server (admin peer "laptop"
// declared) and its HTTP test frontend.
func newEnrollServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	m := testWGManager(t, []string{wellKnownAddr}, "")
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	s := &server{
		root:       m.root,
		store:      newAuthStore(),
		sessions:   newSessionStore(),
		adminAddrs: []string{wellKnownAddr},
		wgm:        m,
	}
	ts := httptest.NewServer(s.mux())
	t.Cleanup(ts.Close)
	return s, ts
}

// enrollChallenge fetches and parses the challenge for name.
func enrollChallenge(t *testing.T, base, name string) (nonce, message string) {
	t.Helper()
	code, body := get(t, http.DefaultClient, base+"/wg/enroll?name="+url.QueryEscape(name))
	if code != http.StatusOK {
		t.Fatalf("challenge for %q: got %d: %s", name, code, body)
	}
	var ch struct{ Name, Nonce, Message string }
	if err := json.Unmarshal([]byte(body), &ch); err != nil {
		t.Fatal(err)
	}
	return ch.Nonce, ch.Message
}

func postEnroll(t *testing.T, base, name, nonce, sig string) (int, string) {
	t.Helper()
	resp, err := http.PostForm(base+"/wg/enroll", url.Values{
		"name": {name}, "nonce": {nonce}, "signature": {sig},
	})
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

func TestEnrollFlow(t *testing.T) {
	_, ts := newEnrollServer(t)

	// Name normalization: "Laptop " must resolve to the declared peer.
	nonce, message := enrollChallenge(t, ts.URL, "Laptop")
	if !strings.Contains(message, "device: laptop") || !strings.Contains(message, "nonce: "+nonce) {
		t.Fatalf("challenge message malformed:\n%s", message)
	}

	code, body := postEnroll(t, ts.URL, "laptop", nonce, personalSign(t, testKey(t), message))
	if code != http.StatusOK {
		t.Fatalf("enroll: got %d: %s", code, body)
	}

	// The returned config must match offline derivation exactly.
	master, err := wgderive.MasterFromSignatureHex(unsealSig(t))
	if err != nil {
		t.Fatal(err)
	}
	subnet := netip.MustParsePrefix("10.99.0.0/24")
	ip, err := wgderive.AdminTunnelIP(master, "laptop", subnet)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PrivateKey = " + wgderive.KeyBase64(wgderive.AdminKey(master, "laptop")),
		"Address = " + ip.String() + "/24",
		"DNS = 10.99.0.1, talos.wg",
		"PublicKey = " + wgderive.KeyBase64(wgderive.PublicKey(wgderive.ServerKey(master))),
		"Endpoint = 203.0.113.7:51820",
		"AllowedIPs = 10.99.0.0/24",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config missing %q:\n%s", want, body)
		}
	}

	// Nonce is single-use: the same signature must not enroll twice.
	code, _ = postEnroll(t, ts.URL, "laptop", nonce, personalSign(t, testKey(t), message))
	if code != http.StatusForbidden {
		t.Errorf("replayed nonce: got %d, want 403", code)
	}
}

func TestEnrollRejectsWrongWallet(t *testing.T) {
	_, ts := newEnrollServer(t)
	nonce, message := enrollChallenge(t, ts.URL, "laptop")
	code, _ := postEnroll(t, ts.URL, "laptop", nonce, personalSign(t, otherTestKey(t), message))
	if code != http.StatusForbidden {
		t.Errorf("wrong wallet: got %d, want 403", code)
	}
}

func TestEnrollRejectsUnknownDevice(t *testing.T) {
	_, ts := newEnrollServer(t)
	code, _ := get(t, http.DefaultClient, ts.URL+"/wg/enroll?name=phone")
	if code != http.StatusNotFound {
		t.Errorf("unknown device challenge: got %d, want 404", code)
	}
	code, _ = postEnroll(t, ts.URL, "phone", "deadbeef", "0x00")
	if code != http.StatusNotFound {
		t.Errorf("unknown device enroll: got %d, want 404", code)
	}
}

func TestEnrollWhileSealed(t *testing.T) {
	m := testWGManager(t, []string{wellKnownAddr}, "")
	s := &server{root: m.root, store: newAuthStore(), sessions: newSessionStore(), adminAddrs: []string{wellKnownAddr}, wgm: m}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	code, _ := get(t, http.DefaultClient, ts.URL+"/wg/enroll?name=laptop")
	if code != http.StatusServiceUnavailable {
		t.Errorf("sealed challenge: got %d, want 503", code)
	}
}

func TestEnrollDisabledWithoutWG(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	code, _ := get(t, http.DefaultClient, ts.URL+"/wg/enroll?name=laptop")
	if code != http.StatusNotFound {
		t.Errorf("wg disabled: got %d, want 404", code)
	}
}
