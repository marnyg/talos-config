package main

import (
	"strings"
	"testing"
)

// hubRendered mimics the shape deviceConfig emits (yaml.v3, 4-space
// indent, no tun.dev — device configs are portable by design).
const hubRendered = `pki:
    ca: |
        -----BEGIN NEBULA CERTIFICATE-----
        AAAA
        -----END NEBULA CERTIFICATE-----
static_host_map:
    10.42.0.1:
        - example.fly.dev:4242
lighthouse:
    am_lighthouse: false
    serve_dns: false
    interval: 60
    hosts:
        - 10.42.0.1
tun:
    disabled: false
    mtu: 1300
logging:
    level: info
    format: text
`

func TestAdaptSplitDNSPinsDevAndFindsHub(t *testing.T) {
	out, dev, hub, note := adaptSplitDNS([]byte(hubRendered), "auto", true)
	if note != "" {
		t.Fatalf("unexpected note: %q", note)
	}
	if dev != nebTunDev {
		t.Fatalf("dev = %q, want %q", dev, nebTunDev)
	}
	if hub != "10.42.0.1" {
		t.Fatalf("hub = %q, want 10.42.0.1", hub)
	}
	if !strings.Contains(string(out), "\n    dev: "+nebTunDev+"\n") {
		t.Fatalf("dev not pinned into tun block:\n%s", out)
	}
	// PEM block untouched: the insert must not reflow the file.
	if !strings.Contains(string(out), "-----BEGIN NEBULA CERTIFICATE-----") {
		t.Fatalf("config reflowed:\n%s", out)
	}
}

func TestAdaptSplitDNSIdempotent(t *testing.T) {
	once, _, _, _ := adaptSplitDNS([]byte(hubRendered), "auto", true)
	twice, dev, hub, note := adaptSplitDNS(once, "auto", true)
	if string(twice) != string(once) {
		t.Fatalf("second adaptation changed the config")
	}
	if dev != nebTunDev || hub != "10.42.0.1" || note != "" {
		t.Fatalf("dev=%q hub=%q note=%q", dev, hub, note)
	}
}

func TestAdaptSplitDNSOff(t *testing.T) {
	out, dev, hub, note := adaptSplitDNS([]byte(hubRendered), "off", true)
	if string(out) != hubRendered || dev != "" || hub != "" || note != "" {
		t.Fatalf("off mode must be a no-op: dev=%q hub=%q note=%q", dev, hub, note)
	}
}

func TestAdaptSplitDNSNoResolvectl(t *testing.T) {
	out, dev, _, note := adaptSplitDNS([]byte(hubRendered), "auto", false)
	if string(out) != hubRendered || dev != "" {
		t.Fatalf("without resolvectl the config must pass through: dev=%q", dev)
	}
	if !strings.Contains(note, "no resolvectl") {
		t.Fatalf("note = %q, want a no-resolvectl explanation", note)
	}
}

func TestAdaptSplitDNSKeepsExistingDev(t *testing.T) {
	pinned := strings.Replace(hubRendered, "tun:\n", "tun:\n    dev: utun7\n", 1)
	out, dev, hub, note := adaptSplitDNS([]byte(pinned), "auto", true)
	if string(out) != pinned {
		t.Fatalf("existing dev name must not be rewritten")
	}
	if dev != "utun7" || hub != "10.42.0.1" || note != "" {
		t.Fatalf("dev=%q hub=%q note=%q", dev, hub, note)
	}
}

func TestAdaptSplitDNSGarbageConfig(t *testing.T) {
	out, dev, _, note := adaptSplitDNS([]byte(":\tnot yaml"), "auto", true)
	if string(out) != ":\tnot yaml" || dev != "" {
		t.Fatalf("garbage must pass through with split DNS disabled")
	}
	if !strings.Contains(note, "-reenroll") {
		t.Fatalf("note = %q, want a reenroll hint", note)
	}
}

func TestPinTunDevNoTunBlock(t *testing.T) {
	if _, err := pinTunDev([]byte("lighthouse:\n    hosts:\n        - 10.42.0.1\n"), nebTunDev); err == nil {
		t.Fatal("want an error when the config has no tun block")
	}
}
