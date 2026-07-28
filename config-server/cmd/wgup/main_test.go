package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
