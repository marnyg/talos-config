package main

// Tests for the /policy page and the signed overlay set/clear actions.
// Server construction reuses newMeshEnrollServer (a hub-managed server
// with the repo policy installed) and the /status login helpers — the
// page follows the same session + per-action-signature model.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// otherKey is a valid wallet that is NOT in the admin allowlist.
func otherKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	b := make([]byte, 32)
	b[31] = 2
	return secp256k1.PrivKeyFromBytes(b)
}

// policyTestDoc is a valid replacement policy with a marker port no
// rule in the repo file uses.
const policyTestDoc = `hub:
  inbound:
    - {port: any, proto: icmp, host: any}
node:
  inbound:
    - {port: "18443", proto: tcp, group: admins}
device:
  inbound:
    - {port: any, proto: icmp, host: any}
`

// postPolicy submits a signed overlay install and returns the response
// (redirects not followed by the passed client are the caller's business).
func postPolicy(t *testing.T, c *http.Client, base string, form url.Values) *http.Response {
	t.Helper()
	resp, err := c.PostForm(base+"/policy/overlay", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestPolicyPageRequiresSession(t *testing.T) {
	_, ts := newMeshEnrollServer(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Get(ts.URL + "/policy")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/status" {
		t.Errorf("anonymous /policy: got %d -> %q, want 303 -> /status", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Mutations without a session are refused outright, signature or not.
	resp, err = c.PostForm(ts.URL+"/policy/overlay", url.Values{"policy": {policyTestDoc}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("anonymous overlay post: got %d, want 403", resp.StatusCode)
	}
}

func TestPolicyOverlayLifecycle(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	client := login(t, ts)

	// Baseline page: git file in effect, repo content visible.
	code, body := get(t, client, ts.URL+"/policy")
	if code != http.StatusOK {
		t.Fatalf("policy page: got %d", code)
	}
	for _, want := range []string{"The git file is in effect", "30096"} {
		if !strings.Contains(body, want) {
			t.Errorf("baseline page missing %q", want)
		}
	}

	// Install: signature binds sha256(text) + a fresh nonce.
	nonce := s.sessions.issueNonce()
	sig := personalSign(t, testKey(t), policySetMessageV1(policySHA256(policyTestDoc), nonce))
	resp := postPolicy(t, client, ts.URL, url.Values{
		"policy": {policyTestDoc}, "nonce": {nonce}, "signature": {sig},
	})
	if resp.Request.URL.Path != "/policy" { // followed the 303 back
		t.Fatalf("overlay post landed on %s", resp.Request.URL)
	}
	raw, by, _, ok := s.mesh().PolicyOverlay()
	if !ok || by != wellKnownAddr || !strings.Contains(string(raw), "18443") {
		t.Fatalf("overlay not installed: ok=%v by=%q raw=%q", ok, by, raw)
	}

	// Page now shows the overlay, the diff and the export text.
	code, body = get(t, client, ts.URL+"/policy")
	if code != http.StatusOK {
		t.Fatalf("policy page with overlay: got %d", code)
	}
	for _, want := range []string{"Ephemeral overlay active", "18443", "Diff", wellKnownAddr, "/policy/clear"} {
		if !strings.Contains(body, want) {
			t.Errorf("overlay page missing %q", want)
		}
	}

	// Clear: also a signed action (reverting can widen access).
	nonce = s.sessions.issueNonce()
	sig = personalSign(t, testKey(t), policyClearMessageV1(nonce))
	resp2, err := client.PostForm(ts.URL+"/policy/clear", url.Values{
		"nonce": {nonce}, "signature": {sig},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if _, _, _, ok := s.mesh().PolicyOverlay(); ok {
		t.Error("overlay survived a signed clear")
	}
}

// TestPolicyOverlayCRLF: browsers hash textarea.value (LF) but submit
// CRLF; the server must normalize before verifying, or every real
// install would fail.
func TestPolicyOverlayCRLF(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	client := login(t, ts)

	nonce := s.sessions.issueNonce()
	sig := personalSign(t, testKey(t), policySetMessageV1(policySHA256(policyTestDoc), nonce))
	crlf := strings.ReplaceAll(policyTestDoc, "\n", "\r\n")
	postPolicy(t, client, ts.URL, url.Values{
		"policy": {crlf}, "nonce": {nonce}, "signature": {sig},
	})
	raw, _, _, ok := s.mesh().PolicyOverlay()
	if !ok {
		t.Fatal("CRLF submission rejected despite a valid LF signature")
	}
	if strings.Contains(string(raw), "\r") {
		t.Error("stored overlay kept CRLF — export text would not round-trip")
	}
}

func TestPolicyOverlayRejections(t *testing.T) {
	s, ts := newMeshEnrollServer(t)
	client := login(t, ts)

	fresh := func() string { return s.sessions.issueNonce() }

	t.Run("wrong wallet", func(t *testing.T) {
		nonce := fresh()
		sig := personalSign(t, otherKey(t), policySetMessageV1(policySHA256(policyTestDoc), nonce))
		resp := postPolicy(t, client, ts.URL, url.Values{
			"policy": {policyTestDoc}, "nonce": {nonce}, "signature": {sig},
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("got %d, want 403", resp.StatusCode)
		}
	})

	t.Run("signature over different content", func(t *testing.T) {
		nonce := fresh()
		sig := personalSign(t, testKey(t), policySetMessageV1(policySHA256(policyTestDoc), nonce))
		tampered := strings.Replace(policyTestDoc, "18443", "22", 1)
		resp := postPolicy(t, client, ts.URL, url.Values{
			"policy": {tampered}, "nonce": {nonce}, "signature": {sig},
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("got %d, want 403", resp.StatusCode)
		}
	})

	t.Run("invalid document", func(t *testing.T) {
		bad := strings.Replace(policyTestDoc, "group: admins", "group: admiins", 1)
		nonce := fresh()
		sig := personalSign(t, testKey(t), policySetMessageV1(policySHA256(bad), nonce))
		resp := postPolicy(t, client, ts.URL, url.Values{
			"policy": {bad}, "nonce": {nonce}, "signature": {sig},
		})
		// Valid signature, invalid content: bounced back to the page
		// with the validation error, nothing installed.
		if resp.Request.URL.Path != "/policy" || !strings.Contains(resp.Request.URL.RawQuery, "rejected") {
			t.Errorf("landed on %s, want /policy?msg=rejected...", resp.Request.URL)
		}
	})

	t.Run("replayed nonce", func(t *testing.T) {
		nonce := fresh()
		sig := personalSign(t, testKey(t), policySetMessageV1(policySHA256(policyTestDoc), nonce))
		form := url.Values{"policy": {policyTestDoc}, "nonce": {nonce}, "signature": {sig}}
		postPolicy(t, client, ts.URL, form) // consumes the nonce
		s.mesh().ClearPolicyOverlay()
		resp := postPolicy(t, client, ts.URL, form)
		if _, _, _, ok := s.mesh().PolicyOverlay(); ok {
			t.Error("replayed nonce installed an overlay")
		}
		if resp.Request.URL.Path != "/policy" || !strings.Contains(resp.Request.URL.RawQuery, "expired") {
			t.Errorf("landed on %s, want /policy?msg=...expired...", resp.Request.URL)
		}
	})

	if _, _, _, ok := s.mesh().PolicyOverlay(); ok {
		t.Error("a rejected path left an overlay installed")
	}
}

func TestDiffPolicyLines(t *testing.T) {
	got := diffPolicyLines("a\nb\nc\n", "a\nx\nc\nd\n")
	want := []diffLine{{" ", "a"}, {"-", "b"}, {"+", "x"}, {" ", "c"}, {"+", "d"}}
	if len(got) != len(want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("diff[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
