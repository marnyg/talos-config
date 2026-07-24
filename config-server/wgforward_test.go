package main

// End-to-end test of hub forwarding: a real WG hub device (wgstack,
// forwarding netstack) with two peers — an "admin" and a "machine",
// both plain upstream netstacks — all in-process over loopback UDP.
// The admin dials TCP to the machine's tunnel address THROUGH the hub,
// which only works if the hub forwards between peers.

import (
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// testPeer is an in-process userspace WG client on the tunnel.
func testPeer(t *testing.T, priv [32]byte, addr netip.Addr, serverPubHex string, serverPort int) *netstack.Net {
	t.Helper()
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr}, []netip.Addr{}, 1280)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "peer: "))
	uapi := fmt.Sprintf(
		"private_key=%s\npublic_key=%s\nendpoint=127.0.0.1:%d\nallowed_ip=0.0.0.0/0\npersistent_keepalive_interval=1\n",
		wgderive.KeyHex(priv), serverPubHex, serverPort)
	if err := dev.IpcSet(uapi); err != nil {
		t.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dev.Close)
	return tnet
}

func TestHubForwardsBetweenPeers(t *testing.T) {
	master, err := wgderive.MasterFromHex("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	subnet := netip.MustParsePrefix("10.99.0.0/24")
	serverPriv := wgderive.ServerKey(master)
	serverPubHex := wgderive.KeyHex(wgderive.PublicKey(serverPriv))

	mac := "b0:41:6f:15:3b:8f"
	machinePriv := wgderive.MachineKey(master, mac)
	machineIP, err := wgderive.TunnelIP(master, mac, subnet)
	if err != nil {
		t.Fatal(err)
	}
	adminPriv := wgderive.AdminKey(master, "laptop")
	adminIP, err := wgderive.AdminTunnelIP(master, "laptop", subnet)
	if err != nil {
		t.Fatal(err)
	}

	// Hub on a random loopback UDP port.
	port := 30000 + int(machinePriv[0])%20000
	_, dev, err := startWireGuard(serverPriv, port, netip.MustParseAddr("10.99.0.1"), []wgPeer{
		{publicKeyHex: wgderive.KeyHex(wgderive.PublicKey(machinePriv)), allowedIP: netip.PrefixFrom(machineIP, 32)},
		{publicKeyHex: wgderive.KeyHex(wgderive.PublicKey(adminPriv)), allowedIP: netip.PrefixFrom(adminIP, 32)},
	})
	if err != nil {
		t.Fatalf("starting hub: %v", err)
	}
	t.Cleanup(dev.Close)

	machine := testPeer(t, machinePriv, machineIP, serverPubHex, port)
	admin := testPeer(t, adminPriv, adminIP, serverPubHex, port)

	// The "machine" runs a TCP service on its tunnel address.
	lst, err := machine.ListenTCP(&net.TCPAddr{Port: 6443})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := lst.Accept()
			if err != nil {
				return
			}
			fmt.Fprint(c, "apid says hi")
			c.Close()
		}
	}()

	// Admin → hub → machine. Retry while handshakes settle.
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		c, err := admin.Dial("tcp", fmt.Sprintf("%s:6443", machineIP))
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(c)
		c.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if got := string(body); got != "apid says hi" {
			t.Fatalf("wrong payload through hub: %q", got)
		}
		return // success
	}
	t.Fatalf("admin never reached machine through the hub: %v", lastErr)
}
