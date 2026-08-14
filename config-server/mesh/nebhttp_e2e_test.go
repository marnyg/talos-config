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
// works on the mesh: a real admin device fetches /config through a real
// nebula handshake against the hub's netstack listener, and a device
// outside the admins set is refused by the source-IP gate.
//
// The firewall layer (cert group → tcp/80) is nebula's own enforcement
// and is validated against the rendered config in nebconf_test.go; the
// nebtest harness runs an open firewall precisely so this test isolates
// the second layer — the derived-admin-address + cert-group gate that
// carries ADR-0003 onto the mesh (ADR-0012).
func TestMeshHTTPOverOverlay(t *testing.T) {
	master := []byte("mesh-http-e2e-test-master-32byte")
	subnet := netip.MustParsePrefix("10.42.0.0/16")
	const lighthousePort = 24244

	hub := nebtest.Hub(t, master, subnet, lighthousePort)
	admin := nebtest.DeviceWithGroups(t, master, subnet, "laptop", lighthousePort, []string{GroupAdmins})
	tv := nebtest.DeviceWithGroups(t, master, subnet, "tv", lighthousePort, []string{GroupMedia})

	m := NewManager(lighthousePort, subnet, nebtest.Loopback, "hub.example:4242", "", t.TempDir())
	m.TunnelConfig = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "composed-config")
	})
	if err := m.serveMeshHTTP(hub, master); err != nil {
		t.Fatal(err)
	}

	// Wait for the admin's handshake once; every case runs over the
	// established tunnels after that.
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, _, lastErr = meshGet(admin, hub.OverlayAddr(), "/")
		if lastErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("mesh http never answered over the overlay: %v", lastErr)
	}

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

	t.Run("admin device gets the config", func(t *testing.T) {
		status, body, err := meshGet(admin, hub.OverlayAddr(), "/config?mac=aa-bb-cc-dd-ee-01")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		if body != "composed-config" {
			t.Errorf("body = %q, want %q", body, "composed-config")
		}
	})

	t.Run("non-admin device is refused", func(t *testing.T) {
		status, _, err := meshGet(tv, hub.OverlayAddr(), "/config?mac=aa-bb-cc-dd-ee-01")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (media device must not read machine configs)", status)
		}
	})
}

// meshGet performs one HTTP GET through a member's netstack to the
// hub's overlay listener.
func meshGet(dev *nebstack.Service, hubAddr netip.Addr, path string) (int, string, error) {
	client := &http.Client{
		Timeout: 2 * time.Second,
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
