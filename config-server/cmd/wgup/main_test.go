package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const renderedConf = `[Interface]
PrivateKey = KEY
Address = 10.99.0.207/24
MTU = 1240
DNS = 10.99.0.1, talos.wg

[Peer]
PublicKey = PUB
Endpoint = 203.0.113.7:51820
AllowedIPs = 10.99.0.0/24
PersistentKeepalive = 25
`

func TestAdaptDNS(t *testing.T) {
	// auto + resolvectl → split DNS PostUp, no DNS= line.
	out, note := adaptDNS(renderedConf, "auto", true)
	if strings.Contains(out, "DNS =") {
		t.Errorf("DNS line survived split adaptation:\n%s", out)
	}
	if !strings.Contains(out, "PostUp = resolvectl dns %i 10.99.0.1; resolvectl domain %i talos.wg") {
		t.Errorf("missing resolvectl PostUp:\n%s", out)
	}
	if note == "" {
		t.Error("split adaptation should explain itself")
	}

	// Idempotent: adapting the adapted config is a no-op.
	again, note2 := adaptDNS(out, "auto", true)
	if again != out || note2 != "" {
		t.Error("adaptation not idempotent")
	}

	// auto without resolvectl → DNS stripped, warning note.
	out, note = adaptDNS(renderedConf, "auto", false)
	if strings.Contains(out, "DNS =") || strings.Contains(out, "PostUp") {
		t.Errorf("auto without resolvectl must strip DNS:\n%s", out)
	}
	if note == "" {
		t.Error("stripping DNS should warn")
	}

	// off → stripped silently; keep → untouched.
	out, note = adaptDNS(renderedConf, "off", true)
	if strings.Contains(out, "DNS =") || note != "" {
		t.Errorf("off: got note %q, config:\n%s", note, out)
	}
	out, _ = adaptDNS(renderedConf, "keep", true)
	if out != renderedConf {
		t.Error("keep must not modify the config")
	}

	// The rest of the config is untouched by adaptation.
	out, _ = adaptDNS(renderedConf, "auto", true)
	for _, want := range []string{"PrivateKey = KEY", "Address = 10.99.0.207/24", "MTU = 1240", "Endpoint = 203.0.113.7:51820"} {
		if !strings.Contains(out, want) {
			t.Errorf("adaptation lost %q", want)
		}
	}
}

func TestSignHandler(t *testing.T) {
	sigCh := make(chan string, 1)
	ts := httptest.NewServer(signHandler("laptop", "challenge\nnonce: abc", "tok123", sigCh))
	defer ts.Close()

	// The signing page renders only under the token path.
	resp, err := http.Get(ts.URL + "/tok123")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign page: got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "laptop") || !strings.Contains(string(body), "personal_sign") {
		t.Errorf("sign page incomplete:\n%s", body)
	}

	for _, path := range []string{"/", "/wrong", "/wrong/sig"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: got %d, want 404", path, resp.StatusCode)
		}
	}

	// Empty signature is rejected, real one lands on the channel.
	resp, err = http.Post(ts.URL+"/tok123/sig", "text/plain", strings.NewReader("  "))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty signature: got %d, want 400", resp.StatusCode)
	}
	resp, err = http.Post(ts.URL+"/tok123/sig", "text/plain", strings.NewReader("0xdeadbeef\n"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("signature post: got %d, want 200", resp.StatusCode)
	}
	select {
	case sig := <-sigCh:
		if sig != "0xdeadbeef" {
			t.Errorf("got signature %q", sig)
		}
	default:
		t.Fatal("signature not delivered")
	}
}

// TestBrowserSignature drives the whole local flow with a stubbed
// browser: capture the page URL, post the signature, get it returned.
func TestBrowserSignature(t *testing.T) {
	urlCh := make(chan string, 1)
	orig := openBrowser
	openBrowser = func(u string) { urlCh <- u }
	defer func() { openBrowser = orig }()

	done := make(chan struct {
		sig string
		err error
	}, 1)
	go func() {
		sig, err := browserSignature("laptop", "msg")
		done <- struct {
			sig string
			err error
		}{sig, err}
	}()

	pageURL := <-urlCh
	resp, err := http.Post(pageURL+"/sig", "text/plain", strings.NewReader("0xf00d"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.sig != "0xf00d" {
		t.Errorf("got %q, want 0xf00d", res.sig)
	}
}
