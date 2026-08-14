// Package nebtest starts throwaway nebula meshes in-process for tests.
// It exists so the overlay can be exercised end to end — real certs,
// real handshake, real UDP on loopback — from more than one package
// without copying the harness.
//
// The configs here are deliberately minimal: enough to get two members
// talking, and nothing else. They are NOT the hub's real config. What
// the hub actually ships is rendered by mesh.HubConfig and validated
// against nebula's own config checks in nebconf_test.go; if you find
// yourself adding hub-like settings here, that is the sign a test
// belongs over there instead.
package nebtest

import (
	"fmt"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

// Loopback is where test members listen; nothing here touches a real
// network interface.
const Loopback = "127.0.0.1"

// Hub starts a lighthouse+relay on the derived hub identity, listening
// on loopback at port. Peers reach it via StaticHostMap.
func Hub(tb testing.TB, master []byte, subnet netip.Prefix, port int) *nebstack.Service {
	tb.Helper()
	priv, pub := nebderive.HubKey(master)
	addr, err := nebderive.HubIP(subnet)
	if err != nil {
		tb.Fatalf("deriving hub address: %v", err)
	}
	return start(tb, master, subnet, nebderive.HubName, priv, pub, addr, fmt.Sprintf(`
static_host_map: {}
lighthouse:
  am_lighthouse: true
relay:
  am_relay: true
listen:
  host: %s
  port: %d
`, Loopback, port))
}

// Device starts a named device peer pointed at a hub on hubPort. The
// cert carries no groups — use DeviceWithGroups when the test exercises
// group-based admission (mesh /config gate, DNS live-device resolution).
func Device(tb testing.TB, master []byte, subnet netip.Prefix, name string, hubPort int) *nebstack.Service {
	return DeviceWithGroups(tb, master, subnet, name, hubPort, nil)
}

// DeviceWithGroups starts a named device peer whose cert carries the
// given groups. Under ADR-0012 the production hub never derives a
// device's private key, but the derivation is still valid for tests —
// what changes is only the wire (pubkey submitted, not derived).
func DeviceWithGroups(tb testing.TB, master []byte, subnet netip.Prefix, name string, hubPort int, groups []string) *nebstack.Service {
	tb.Helper()
	priv, pub := nebderive.DeviceKey(master, name)
	addr, err := nebderive.DeviceIP(master, name, subnet)
	if err != nil {
		tb.Fatalf("deriving address for device %q: %v", name, err)
	}
	hubIP, err := nebderive.HubIP(subnet)
	if err != nil {
		tb.Fatalf("deriving hub address: %v", err)
	}
	return startWithGroups(tb, master, subnet, name, priv, pub, addr, groups, fmt.Sprintf(`
static_host_map:
  "%s": ["%s:%d"]
lighthouse:
  hosts: ["%s"]
  interval: 1
listen:
  host: %s
  port: 0
`, hubIP, Loopback, hubPort, hubIP, Loopback))
}

// start mints a leaf from the derived CA and brings up nebula plus its
// netstack. The identity is always derived, never ad hoc, so a test that
// passes here is evidence about the real derivation chain.
func start(tb testing.TB, master []byte, subnet netip.Prefix, name string, priv, pub [32]byte, addr netip.Addr, extra string) *nebstack.Service {
	return startWithGroups(tb, master, subnet, name, priv, pub, addr, nil, extra)
}

// startWithGroups is start plus a group list for the leaf cert.
func startWithGroups(tb testing.TB, master []byte, subnet netip.Prefix, name string, priv, pub [32]byte, addr netip.Addr, groups []string, extra string) *nebstack.Service {
	tb.Helper()

	caPEM, err := nebderive.CACertPEM(master)
	if err != nil {
		tb.Fatalf("deriving CA: %v", err)
	}
	crt, err := nebderive.HostCert(master, name, pub, addr, subnet, groups,
		time.Now().Add(-time.Minute), time.Now().Add(10*time.Minute))
	if err != nil {
		tb.Fatalf("minting cert for %q: %v", name, err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		tb.Fatalf("marshalling cert for %q: %v", name, err)
	}

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
		tb.Fatalf("loading nebula config for %q: %v", name, err)
	}

	// Nebula's own logs are discarded; failures surface through tb.
	// (nebula.Main does not call logging.ApplyConfig — config-driven
	// level and format are the embedder's job.)
	control, err := nebula.Main(&c, false, "nebtest", logging.NewLogger(io.Discard), overlay.NewUserDeviceFromConfig)
	if err != nil {
		tb.Fatalf("nebula.Main for %q: %v", name, err)
	}
	svc, err := nebstack.New(control)
	if err != nil {
		tb.Fatalf("nebstack.New for %q: %v", name, err)
	}
	tb.Cleanup(func() { _ = svc.Close() })
	return svc
}

// indent prefixes every line so a PEM block can sit in a YAML block
// scalar.
func indent(pem []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(pem), "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}
