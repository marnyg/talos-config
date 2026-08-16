package policyclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testConfig mimics a hub-rendered device config: multi-line PEM in
// pki (the part a bad splice would mangle), plus the firewall block.
const testConfig = `pki:
    ca: |
        -----BEGIN NEBULA CERTIFICATE-----
        Q0EgUEVNIGJsb2NrIGNvbnRlbnQgZm9yIHRoZSBzcGxpY2UgdGVzdA==
        -----END NEBULA CERTIFICATE-----
    cert: |
        -----BEGIN NEBULA CERTIFICATE-----
        ZGV2aWNlIGNlcnQgY29udGVudCBmb3IgdGhlIHNwbGljZSB0ZXN0
        -----END NEBULA CERTIFICATE-----
    key: |
        -----BEGIN NEBULA X25519 PRIVATE KEY-----
        cHJpdmF0ZSBrZXkgYnl0ZXM=
        -----END NEBULA X25519 PRIVATE KEY-----
lighthouse:
    hosts:
        - 10.42.0.1
tun:
    dev: nebula0
firewall:
    outbound_action: drop
    inbound_action: drop
    outbound:
        - port: any
          proto: any
          host: any
    inbound:
        - port: any
          proto: icmp
          host: any
`

var newRules = []Rule{
	{Port: "any", Proto: "icmp", Host: "any"},
	{Port: "18080", Proto: "tcp", Group: "media"},
}

func TestSpliceInbound(t *testing.T) {
	out, err := SpliceInbound([]byte(testConfig), newRules)
	if err != nil {
		t.Fatal(err)
	}

	got, err := InboundRules(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != newRules[1] {
		t.Errorf("spliced inbound = %+v, want %+v", got, newRules)
	}

	// The identity material must survive byte-for-byte in value.
	for _, pem := range []string{
		"Q0EgUEVNIGJsb2NrIGNvbnRlbnQgZm9yIHRoZSBzcGxpY2UgdGVzdA==",
		"ZGV2aWNlIGNlcnQgY29udGVudCBmb3IgdGhlIHNwbGljZSB0ZXN0",
		"cHJpdmF0ZSBrZXkgYnl0ZXM=",
		"-----BEGIN NEBULA X25519 PRIVATE KEY-----",
	} {
		if !strings.Contains(string(out), pem) {
			t.Errorf("spliced config lost %q", pem)
		}
	}
	// Everything else survives too.
	for _, keep := range []string{"10.42.0.1", "dev: nebula0", "outbound_action: drop"} {
		if !strings.Contains(string(out), keep) {
			t.Errorf("spliced config lost %q", keep)
		}
	}

	// Determinism: splicing the same rules into the result is a
	// byte-level no-op. nebup's semantic compare assumes this can be
	// relied on for the write-skip, and mobile re-splices from its own
	// previous output.
	again, err := SpliceInbound(out, newRules)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(out) {
		t.Error("double splice is not deterministic")
	}
}

func TestSpliceInboundNoFirewall(t *testing.T) {
	if _, err := SpliceInbound([]byte("pki:\n    ca: x\n"), newRules); err == nil {
		t.Error("expected an error for a config without a firewall block")
	}
}

func TestInboundRules(t *testing.T) {
	got, err := InboundRules([]byte(testConfig))
	if err != nil {
		t.Fatal(err)
	}
	want := Rule{Port: "any", Proto: "icmp", Host: "any"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("InboundRules = %+v, want [%+v]", got, want)
	}
}

func TestFetch(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/policy" {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(`{"epoch":"e1","inbound":[{"port":"any","proto":"icmp","host":"any"}]}`))
		}))
		defer ts.Close()
		w, err := Fetch(ts.Client(), ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		if w.Epoch != "e1" || len(w.Inbound) != 1 || w.Inbound[0].Proto != "icmp" {
			t.Errorf("Fetch = %+v", w)
		}
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		defer ts.Close()
		if _, err := Fetch(ts.Client(), ts.URL); err == nil {
			t.Error("expected an error for 403")
		}
	})

	t.Run("empty rules are an error", func(t *testing.T) {
		// A rule set that drops everything must never be applied.
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"epoch":"e1","inbound":[]}`))
		}))
		defer ts.Close()
		if _, err := Fetch(ts.Client(), ts.URL); err == nil {
			t.Error("expected an error for an empty rule set")
		}
	})
}
