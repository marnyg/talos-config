package main

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
	"github.com/marnyg/talos-config/config-server/nebtest"
)

// TestMeshDNSOverOverlay closes the loose thread the spike left open:
// the hub answers DNS on the overlay without a TUN. A real device peer
// resolves a machine name over a real nebula handshake against the hub's
// netstack listener.
//
// The stock lighthouse could not do this (serve_dns needs a kernel socket
// and therefore a TUN), and the stock service package could not either
// (TCP-only Listen, unreachable stack) — so this is the test that says
// the nebstack detour actually bought what it was for.
func TestMeshDNSOverOverlay(t *testing.T) {
	master := []byte("mesh-dns-e2e-test-master-32bytes")
	subnet := netip.MustParsePrefix("10.42.0.0/16")
	const lighthousePort = 24243

	machines := map[string]machine{"aa:bb:cc:dd:ee:01": {Name: "cp1"}}
	zone, err := buildMeshZone(master, subnet, machines, adminDevices("laptop"))
	if err != nil {
		t.Fatal(err)
	}

	hub := nebtest.Hub(t, master, subnet, lighthousePort)
	dev := nebtest.Device(t, master, subnet, "laptop", lighthousePort)

	if err := serveMeshDNS(hub, zone, meshDNSZone); err != nil {
		t.Fatal(err)
	}

	hubAddr, err := nebderive.HubIP(subnet)
	if err != nil {
		t.Fatal(err)
	}
	wantCP1, err := nebderive.MachineIP(master, "aa:bb:cc:dd:ee:01", subnet)
	if err != nil {
		t.Fatal(err)
	}

	// A machine name, the hub itself, and a name that is not in the zone:
	// the third matters because a stub resolver only fails over to its
	// normal upstream on a clean NXDOMAIN.
	cases := []struct {
		qname   string
		want    netip.Addr
		wantErr dnsmessage.RCode
	}{
		{qname: "cp1." + meshDNSZone, want: wantCP1},
		{qname: nebderive.HubName + "." + meshDNSZone, want: hubAddr},
		{qname: "nope." + meshDNSZone, wantErr: dnsmessage.RCodeNameError},
		{qname: "example.com", wantErr: dnsmessage.RCodeRefused},
	}

	// Wait for the handshake once, then run every case over it.
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, lastErr = resolve(dev, hubAddr, "cp1."+meshDNSZone)
		if lastErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("mesh DNS never answered over the overlay: %v", lastErr)
	}

	for _, tc := range cases {
		t.Run(tc.qname, func(t *testing.T) {
			msg, err := resolve(dev, hubAddr, tc.qname)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want.IsValid() {
				if msg.RCode != dnsmessage.RCodeSuccess {
					t.Fatalf("rcode = %v, want success", msg.RCode)
				}
				if len(msg.Answers) != 1 {
					t.Fatalf("got %d answers, want 1", len(msg.Answers))
				}
				a, ok := msg.Answers[0].Body.(*dnsmessage.AResource)
				if !ok {
					t.Fatalf("answer is %T, want an A record", msg.Answers[0].Body)
				}
				if got := netip.AddrFrom4(a.A); got != tc.want {
					t.Errorf("%s resolved to %s, want %s", tc.qname, got, tc.want)
				}
				return
			}
			if msg.RCode != tc.wantErr {
				t.Errorf("rcode = %v, want %v", msg.RCode, tc.wantErr)
			}
			if len(msg.Answers) != 0 {
				t.Errorf("got %d answers with rcode %v, want none", len(msg.Answers), msg.RCode)
			}
		})
	}
}

// resolve sends one A query over the overlay and parses the reply.
func resolve(dev *nebstack.Service, hubAddr netip.Addr, qname string) (*dnsmessage.Message, error) {
	q := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 0x1234, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name:  dnsmessage.MustNewName(qname + "."),
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		}},
	}
	raw, err := q.Pack()
	if err != nil {
		return nil, err
	}

	c, err := dev.Dial("udp", net.JoinHostPort(hubAddr.String(), "53"))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if _, err := c.Write(raw); err != nil {
		return nil, err
	}
	if err := c.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	var msg dnsmessage.Message
	if err := msg.Unpack(buf[:n]); err != nil {
		return nil, err
	}
	return &msg, nil
}
