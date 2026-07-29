package nebstack

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/nebderive"
)

// The harness deliberately builds its mesh from nebderive rather than
// nebula's cert_test helpers: that way the test exercises the real chain
// the hub will use — derived CA signs derived hub and device identities,
// nebula accepts them, traffic flows. It also makes this the permanent
// home of the evidence the 2026-07-29 spike produced and then threw
// away with the spike directory.

var testSubnet = netip.MustParsePrefix("10.42.0.0/16")

// testMaster is a fixed stand-in for the hub's HKDF master. Any 32 bytes
// work; the derivation is what is under test, not the secret.
var testMaster = []byte("nebstack-test-master-key-32bytes")

// newService starts an in-process nebula with a derived identity and
// wraps it in a Service. name/addr come from nebderive so the cert's
// networks match the netstack address.
func newService(t *testing.T, name string, priv [32]byte, pub [32]byte, addr netip.Addr, extra string) *Service {
	t.Helper()

	caPEM, err := nebderive.CACertPEM(testMaster)
	if err != nil {
		t.Fatalf("deriving CA: %v", err)
	}
	crt, err := nebderive.HostCert(testMaster, name, pub, addr, testSubnet, nil,
		time.Now().Add(-time.Minute), time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("minting cert for %s: %v", name, err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		t.Fatalf("marshalling cert for %s: %v", name, err)
	}

	// Indented as a YAML block scalar so the PEM blocks survive.
	raw := fmt.Sprintf(`
pki:
  ca: |
%s
  cert: |
%s
  key: |
%s
firewall:
  outbound:
    - {proto: any, port: any, host: any}
  inbound:
    - {proto: any, port: any, host: any}
timers:
  pending_deletion_interval: 2
  connection_alive_interval: 2
handshakes:
  try_interval: 200ms
%s`, indent(caPEM), indent(crtPEM), indent(nebderive.HostKeyPEM(priv)), extra)

	var c config.C
	if err := c.LoadString(raw); err != nil {
		t.Fatalf("loading nebula config for %s: %v", name, err)
	}

	// Discard nebula's own logging; a failing test reports through t.
	// (Note for the hub: nebula.Main does not call logging.ApplyConfig,
	// so config-driven level/format is the embedder's job.)
	logger := logging.NewLogger(io.Discard)

	control, err := nebula.Main(&c, false, "nebstack-test", logger, overlay.NewUserDeviceFromConfig)
	if err != nil {
		t.Fatalf("nebula.Main for %s: %v", name, err)
	}
	s, err := New(control)
	if err != nil {
		t.Fatalf("nebstack.New for %s: %v", name, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// indent prefixes every non-empty line so a PEM block can be embedded
// in a YAML block scalar.
func indent(pem []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(pem), "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}

// TestListenUDPOverOverlay is the reason this package exists: a peer
// sends a UDP datagram to the hub's overlay address and the hub both
// receives it and answers from that address. Upstream's Service cannot
// express this (Listen is TCP-only, the stack is unreachable), which is
// what blocks tunnel DNS on the TUN-less hub.
func TestListenUDPOverOverlay(t *testing.T) {
	hubPriv, hubPub := nebderive.HubKey(testMaster)
	hubAddr, err := nebderive.HubIP(testSubnet)
	if err != nil {
		t.Fatal(err)
	}
	devPriv, devPub := nebderive.DeviceKey(testMaster, "laptop")
	devAddr, err := nebderive.DeviceIP(testMaster, "laptop", testSubnet)
	if err != nil {
		t.Fatal(err)
	}
	if hubAddr == devAddr {
		t.Fatalf("derived hub and device to the same address %s", hubAddr)
	}

	const lighthousePort = 24242
	hub := newService(t, nebderive.HubName, hubPriv, hubPub, hubAddr, fmt.Sprintf(`
static_host_map: {}
lighthouse:
  am_lighthouse: true
relay:
  am_relay: true
listen:
  host: 127.0.0.1
  port: %d
`, lighthousePort))

	dev := newService(t, "laptop", devPriv, devPub, devAddr, fmt.Sprintf(`
static_host_map:
  "%s": ["127.0.0.1:%d"]
lighthouse:
  hosts: ["%s"]
  interval: 1
listen:
  host: 127.0.0.1
  port: 0
`, hubAddr, lighthousePort, hubAddr))

	if got := hub.OverlayAddr(); got != hubAddr {
		t.Fatalf("hub OverlayAddr() = %s, want %s", got, hubAddr)
	}

	// Bind the port tunnel DNS will use, via the nil-addr path so the
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The handshake needs the lighthouse round trip first, so retry the
	// dial-and-exchange until it lands or the context expires.
	var lastErr error
	for ctx.Err() == nil {
		lastErr = exchange(dev, hubAddr, "query")
		if lastErr == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no UDP reply over the overlay: %v", lastErr)
}

func exchange(dev *Service, hubAddr netip.Addr, msg string) error {
	c, err := dev.Dial("udp", net.JoinHostPort(hubAddr.String(), "53"))
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.Write([]byte(msg)); err != nil {
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
