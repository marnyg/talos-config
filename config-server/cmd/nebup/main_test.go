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

func TestRouteDev(t *testing.T) {
	for _, tc := range []struct {
		route, want string
	}{
		{"1.2.3.4 via 10.0.0.1 dev wlp3s0 src 10.0.0.42 uid 1000\n    cache\n", "wlp3s0"},
		{"1.2.3.4 dev tailscale0 table 52 src 100.100.1.2 uid 1000", "tailscale0"},
		{"unreachable 1.2.3.4", ""},
		{"", ""},
	} {
		if got := routeDev(tc.route); got != tc.want {
			t.Errorf("routeDev(%q) = %q, want %q", tc.route, got, tc.want)
		}
	}
}

func TestOverlayLink(t *testing.T) {
	for _, tc := range []struct {
		dev, detail string
		want        bool
	}{
		// Physical links, with realistic `ip -d -o link show` detail.
		{"wlp3s0", "3: wlp3s0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP mode DORMANT ... addrgenmode none numtxqueues 1", false},
		{"enp0s31f6", "2: enp0s31f6: <BROADCAST,MULTICAST> mtu 1500 ... link/ether aa:bb:cc:dd:ee:ff", false},
		// Tunnels by link kind.
		{"corp0", "14: corp0: <POINTOPOINT,...> mtu 1420 ... link/none  promiscuity 0 allmulti 0 minmtu 0 maxmtu 2147483552 wireguard addrgenmode eui64", true},
		{"corp1", "15: corp1: <POINTOPOINT,...> mtu 1500 ... link/none  promiscuity 0 tun type tun pi off vnet_hdr off persist off", true},
		// Tunnels by name when the detail carries no kind.
		{"tailscale0", "", true},
		{"wg0", "", true},
		{"tun0", "", true},
		{"nebula1", "", true},
	} {
		if got := overlayLink(tc.dev, tc.detail); got != tc.want {
			t.Errorf("overlayLink(%q, …) = %v, want %v", tc.dev, got, tc.want)
		}
	}
}

func TestPinTunDevNoTunBlock(t *testing.T) {
	if _, err := pinTunDev([]byte("lighthouse:\n    hosts:\n        - 10.42.0.1\n"), nebTunDev); err == nil {
		t.Fatal("want an error when the config has no tun block")
	}
}
