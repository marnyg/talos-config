// External test package (nebstack_test, not nebstack) so it can import
// nebtest, which imports nebstack — an in-package test would be an
// import cycle.
package nebstack_test

import (
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
	"github.com/marnyg/talos-config/config-server/nebtest"
)

var testSubnet = netip.MustParsePrefix("10.42.0.0/16")

// testMaster is a fixed stand-in for the hub's HKDF master. Any 32 bytes
// work; the derivation is what is under test, not the secret.
var testMaster = []byte("nebstack-test-master-key-32bytes")

// TestListenUDPOverOverlay is the reason this package exists: a peer
// sends a UDP datagram to the hub's overlay address and the hub both
// receives it and answers from that address. Upstream's Service cannot
// express this (Listen is TCP-only, the stack is unreachable), which is
// what blocks tunnel DNS on the TUN-less hub.
//
// The mesh is built from derived identities, so this also exercises the
// real chain — derived CA signs derived hub and device certs, nebula
// accepts them — and is the permanent home of the evidence the
// 2026-07-29 spike produced and then deleted along with itself.
func TestListenUDPOverOverlay(t *testing.T) {
	const lighthousePort = 24242
	hub := nebtest.Hub(t, testMaster, testSubnet, lighthousePort)
	dev := nebtest.Device(t, testMaster, testSubnet, "laptop", lighthousePort)

	hubAddr, err := nebderive.HubIP(testSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if got := hub.OverlayAddr(); got != hubAddr {
		t.Fatalf("hub OverlayAddr() = %s, want %s", got, hubAddr)
	}

	// Bind the port tunnel DNS will use, via the nil-IP path so the
	// OverlayAddr default is exercised too.
	conn, err := hub.ListenUDP(&net.UDPAddr{Port: 53})
	if err != nil {
		t.Fatalf("hub ListenUDP: %v", err)
	}
	defer conn.Close()
	if got := conn.LocalAddr().String(); got != net.JoinHostPort(hubAddr.String(), "53") {
		t.Fatalf("listener bound to %s, want %s:53", got, hubAddr)
	}

	// Echo one datagram back, upper-cased, so the reply cannot be
	// confused with the request looping back.
	go func() {
		buf := make([]byte, 512)
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		resp := make([]byte, n)
		for i := 0; i < n; i++ {
			b := buf[i]
			if b >= 'a' && b <= 'z' {
				b -= 32
			}
			resp[i] = b
		}
		_, _ = conn.WriteTo(resp, from)
	}()

	// The handshake needs a lighthouse round trip first, so retry the
	// exchange until it lands or we give up.
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = exchange(dev, hubAddr); lastErr == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no UDP reply over the overlay: %v", lastErr)
}

func exchange(dev *nebstack.Service, hubAddr netip.Addr) error {
	c, err := dev.Dial("udp", net.JoinHostPort(hubAddr.String(), "53"))
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.Write([]byte("query")); err != nil {
		return err
	}
	if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	if got, want := string(buf[:n]), "QUERY"; got != want {
		return fmt.Errorf("got reply %q, want %q", got, want)
	}
	return nil
}
