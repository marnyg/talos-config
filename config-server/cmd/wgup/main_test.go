package main

import (
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
