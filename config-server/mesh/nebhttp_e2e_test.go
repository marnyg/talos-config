package mesh

import (
	"context"
	"encoding/json"
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

	m := NewManager(lighthousePort, subnet, nebtest.Loopback, "hub.example:4242", "", nebPolicyRoot(t))
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

	// /hosts is the device-facing member list (the TV app's screen).
	// Both groups may read it; each caller must appear in its own list
	// as a live device, and the hub row is always present and online.
	for caller, svc := range map[string]*nebstack.Service{"media": tv, "admin": admin} {
		t.Run(caller+" device lists hosts", func(t *testing.T) {
			status, body, err := meshGet(svc, hub.OverlayAddr(), "/hosts")
			if err != nil {
				t.Fatal(err)
			}
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", status, body)
			}
			var got struct {
				Hosts []hostEntry `json:"hosts"`
			}
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("response is not the expected JSON: %v (body %q)", err, body)
			}
			rows := map[string]hostEntry{}
			for _, h := range got.Hosts {
				rows[h.Name] = h
			}
			if h, ok := rows["hub"]; !ok || h.Kind != "hub" || !h.Online {
				t.Errorf("hub row = %+v, want kind=hub online=true", rows["hub"])
			}
			if h, ok := rows["tv"]; !ok || h.Kind != "device" || !h.Online {
				t.Errorf("tv row = %+v, want kind=device online=true (caller must see live devices)", rows["tv"])
			}
		})
	}
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
