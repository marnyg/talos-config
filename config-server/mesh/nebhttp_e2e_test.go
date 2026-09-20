// Excluded under -race: slackhq/nebula v1.11.0 has an upstream data race
// between HandshakeManager.continueHandshake and HandshakeHostInfo.cachePacket
// (handshake_manager.go:963 / :98) that fires when two in-process nodes
// handshake. Not our code; nebula-era, removed with Mesh v3 (359).
//
//go:build !race

package mesh

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/nebstack"
	"github.com/marnyg/talos-config/config-server/nebtest"
)

// TestMeshHTTPOverOverlay proves the control channel's HTTP surface
// still answers on the mesh through a real nebula handshake against
// the hub's netstack listener — and that the routes cut from it
// (/config in 359.8.2.4, /hosts and /policy in ri3b) are really gone,
// not hidden behind a catch-all greeting.
//
// The firewall layer (cert group → tcp/80) is nebula's own enforcement
// and is validated against the rendered config in nebconf_test.go; the
// nebtest harness runs an open firewall.
func TestMeshHTTPOverOverlay(t *testing.T) {
	master := []byte("mesh-http-e2e-test-master-32byte")
	subnet := netip.MustParsePrefix("10.42.0.0/16")
	const lighthousePort = 24244

	hub := nebtest.Hub(t, master, subnet, lighthousePort)
	admin := nebtest.DeviceWithGroups(t, master, subnet, "laptop", lighthousePort, []string{GroupAdmins})
	tv := nebtest.DeviceWithGroups(t, master, subnet, "tv", lighthousePort, []string{GroupMedia})

	m := NewManager(lighthousePort, subnet, nebtest.Loopback, "hub.example:4242", "", nebPolicyRoot(t))
	if err := m.serveMeshHTTP(hub); err != nil {
		t.Fatal(err)
	}

	// Wait out each device's first handshake before any case runs, so
	// no case pays for one inside its own request timeout. Warming only
	// the admin left the tv's handshake in the first tv case, which is
	// what made this test flake under a loaded parallel `go test ./...`.
	waitOverlayReady(t, "admin", admin, hub.OverlayAddr())
	waitOverlayReady(t, "tv", tv, hub.OverlayAddr())

	t.Run("hello for every peer", func(t *testing.T) {
		status, body, err := meshGet(tv, hub.OverlayAddr(), "/")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		want := fmt.Sprintf("hello from the mesh: %s\n", hub.OverlayAddr())
		if body != want {
			t.Errorf("body = %q, want %q", body, want)
		}
	})

	// /config is gone from this listener (359.8.2.4): it lives on the
	// hub-http facet, tested in hubfacet_test.go. /hosts and /policy
	// went with the Android app's move to the identity plane (ri3b).
	for _, path := range []string{"/config?mac=aa-bb-cc-dd-ee-01", "/hosts", "/policy"} {
		t.Run(path+" is not served on the overlay", func(t *testing.T) {
			status, _, err := meshGet(admin, hub.OverlayAddr(), path)
			if err != nil {
				t.Fatal(err)
			}
			if status != http.StatusNotFound {
				t.Fatalf("%s on the overlay: %d, want 404", path, status)
			}
		})
	}
}

// waitOverlayReady polls / from dev until the overlay answers: the
// first request over a fresh tunnel includes a nebula handshake, which
// is the slow, machine-load-sensitive part.
func waitOverlayReady(t *testing.T, name string, dev *nebstack.Service, hubAddr netip.Addr) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, _, lastErr = meshGet(dev, hubAddr, "/"); lastErr == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("mesh http never answered over the overlay for %s: %v", name, lastErr)
}

// meshGetTimeout bounds one request. It is not a latency assertion —
// the hub answers in milliseconds once the tunnel is up — only a bound
// so a wedged tunnel fails the case instead of the whole run; keep it
// far above the handshake a cold tunnel may still need under load.
const meshGetTimeout = 15 * time.Second

// meshGet performs one HTTP GET through a member's netstack to the
// hub's overlay listener.
func meshGet(dev *nebstack.Service, hubAddr netip.Addr, path string) (int, string, error) {
	client := &http.Client{
		Timeout: meshGetTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dev.DialContext(ctx, network, addr)
			},
			// One tunnel-fresh connection per request; keep-alives would
			// let a later case ride an earlier case's connection.
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Get(fmt.Sprintf("http://%s%s", net.JoinHostPort(hubAddr.String(), "80"), path))
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, string(body), nil
}
