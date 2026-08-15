package devkey

import (
	"encoding/hex"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// hubRenderedNoKey mimics what the hub returns under ADR-0012:
// pki.ca and pki.cert inline, pki.key an empty scalar for the client
// to splice its own key into.
const hubRenderedNoKey = `pki:
    ca: |
        -----BEGIN NEBULA CERTIFICATE-----
        AAAA
        -----END NEBULA CERTIFICATE-----
    cert: |
        -----BEGIN NEBULA CERTIFICATE-----
        BBBB
        -----END NEBULA CERTIFICATE-----
    key: ""
lighthouse:
    hosts:
        - 10.42.0.1
tun:
    mtu: 1300
`

func TestGenerateAndParseRoundTrip(t *testing.T) {
	priv, pub, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	// RFC 7748 clamp must hold: low 3 bits clear, high bit clear,
	// second-highest set.
	if priv[0]&7 != 0 || priv[31]&128 != 0 || priv[31]&64 == 0 {
		t.Errorf("private key not clamped: first=%08b last=%08b", priv[0], priv[31])
	}
	priv2, pub2, err := ParsePrivHex(hex.EncodeToString(priv[:]) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if priv2 != priv || pub2 != pub {
		t.Error("ParsePrivHex did not round-trip Generate's keypair")
	}
}

func TestParsePrivHexRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "zz", "abcd", strings.Repeat("ab", 33)} {
		if _, _, err := ParsePrivHex(bad); err == nil {
			t.Errorf("ParsePrivHex(%q): want error", bad)
		}
	}
}

func TestSpliceKeyInline(t *testing.T) {
	var priv [32]byte
	priv[0] = 0x40 // any clamped-looking key; the PEM shape is what matters
	out, err := SpliceKeyInline([]byte(hubRenderedNoKey), priv)
	if err != nil {
		t.Fatal(err)
	}

	// The result must still be one well-formed YAML document …
	var doc struct {
		PKI struct {
			CA   string `yaml:"ca"`
			Cert string `yaml:"cert"`
			Key  string `yaml:"key"`
		} `yaml:"pki"`
		Tun struct {
			MTU int `yaml:"mtu"`
		} `yaml:"tun"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("spliced config is not valid YAML: %v\n%s", err, out)
	}
	// … with the key filled in and everything else untouched.
	if !strings.Contains(doc.PKI.Key, "-----BEGIN NEBULA X25519 PRIVATE KEY-----") {
		t.Errorf("pki.key not a PEM block after splice: %q", doc.PKI.Key)
	}
	if !strings.Contains(doc.PKI.CA, "AAAA") || !strings.Contains(doc.PKI.Cert, "BBBB") {
		t.Errorf("ca/cert damaged by splice: ca=%q cert=%q", doc.PKI.CA, doc.PKI.Cert)
	}
	if doc.Tun.MTU != 1300 {
		t.Errorf("tun.mtu = %d, want 1300 (trailing content damaged)", doc.Tun.MTU)
	}
	// Every original line except the key placeholder survives verbatim —
	// the splice must not reflow or concatenate lines (regression: an
	// earlier version joined all preceding lines without newlines).
	for _, line := range strings.Split(strings.TrimRight(hubRenderedNoKey, "\n"), "\n") {
		if strings.Contains(line, "key:") {
			continue
		}
		if !strings.Contains(string(out), line+"\n") {
			t.Errorf("original line lost or reflowed: %q\n%s", line, out)
		}
	}
}

func TestSpliceKeyInlineRefusesNonEmptyKey(t *testing.T) {
	var priv [32]byte
	once, err := SpliceKeyInline([]byte(hubRenderedNoKey), priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SpliceKeyInline(once, priv); err == nil {
		t.Fatal("want a refusal when pki.key is already set")
	}
}

func TestSpliceKeyInlineNoPlaceholder(t *testing.T) {
	var priv [32]byte
	if _, err := SpliceKeyInline([]byte("tun:\n    mtu: 1300\n"), priv); err == nil {
		t.Fatal("want an error when the config has no pki.key line")
	}
}
