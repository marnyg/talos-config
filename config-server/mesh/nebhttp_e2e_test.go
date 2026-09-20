// Excluded under -race: slackhq/nebula v1.11.0 has an upstream data race
// between HandshakeManager.continueHandshake and HandshakeHostInfo.cachePacket
// (handshake_manager.go:963 / :98) that fires when two in-process nodes
// handshake. Not our code; nebula-era, removed with Mesh v3 (359).
//
//go:build !race

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
	if err := m.serveMeshHTTP(hub, master); err != nil {
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
	// hub-http facet, tested in hubfacet_test.go.
	t.Run("config is not served on the overlay", func(t *testing.T) {
		status, _, err := meshGet(admin, hub.OverlayAddr(), "/config?mac=aa-bb-cc-dd-ee-01")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusNotFound {
			t.Fatalf("/config on the overlay: %d, want 404", status)
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

	// /policy is the live-sync poll target (task 6462fed4 phase 3): a
	// media device reads the device-scope rules through a real
	// handshake, and an overlay installed on the hub changes what the
	// next poll returns — the propagation path devices actually ride.
	t.Run("media device polls policy, overlay propagates", func(t *testing.T) {
		status, body, err := meshGet(tv, hub.OverlayAddr(), "/policy")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", status, body)
		}
		var base struct {
			Epoch   string        `json:"epoch"`
			Inbound []nebRuleYAML `json:"inbound"`
		}
		if err := json.Unmarshal([]byte(body), &base); err != nil {
			t.Fatalf("response is not the expected JSON: %v (body %q)", err, body)
		}
		if base.Epoch == "" || len(base.Inbound) == 0 {
			t.Fatalf("empty policy response: %q", body)
		}

		if err := m.SetPolicyOverlay([]byte(nebOverlayDoc), "0xabc"); err != nil {
			t.Fatal(err)
		}
		defer m.ClearPolicyOverlay()
		_, body2, err := meshGet(tv, hub.OverlayAddr(), "/policy")
		if err != nil {
			t.Fatal(err)
		}
		var over struct {
			Epoch   string        `json:"epoch"`
			Inbound []nebRuleYAML `json:"inbound"`
		}
		if err := json.Unmarshal([]byte(body2), &over); err != nil {
			t.Fatal(err)
		}
		if over.Epoch == base.Epoch {
			t.Error("overlay did not change the policy epoch")
		}
		if len(over.Inbound) != 1 || over.Inbound[0].Port != "18080" {
			t.Errorf("overlay rules = %+v, want the overlay's single 18080 rule", over.Inbound)
		}
	})
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
