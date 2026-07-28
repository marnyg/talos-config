package main

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// dnsSettings is testWGSettings plus tunnel DNS and an admin peer.
func dnsSettings(t *testing.T) *wgSettings {
	t.Helper()
	w := testWGSettings(t)
	w.dnsDomain = "talos.wg"
	w.admins = []string{"laptop"}
	return w
}

func TestBuildDNSZone(t *testing.T) {
	w := dnsSettings(t)
	machines := map[string]machine{
		"b0:41:6f:15:3b:8f": {Name: "cp1", WGIP: "10.99.0.54"},
		"aa:bb:cc:dd:ee:ff": {}, // no name: MAC-with-dashes fallback
	}

	zone, err := buildDNSZone(machines, w)
	if err != nil {
		t.Fatal(err)
	}

	if zone["hub"] != w.serverIP {
		t.Errorf("hub: got %v, want %v", zone["hub"], w.serverIP)
	}
	if zone["cp1"] != netip.MustParseAddr("10.99.0.54") {
		t.Errorf("cp1: got %v, want 10.99.0.54", zone["cp1"])
	}
	wantMachine, err := w.machineTunnelIP("aa:bb:cc:dd:ee:ff", machines["aa:bb:cc:dd:ee:ff"])
	if err != nil {
		t.Fatal(err)
	}
	if zone["aa-bb-cc-dd-ee-ff"] != wantMachine {
		t.Errorf("mac fallback: got %v, want %v", zone["aa-bb-cc-dd-ee-ff"], wantMachine)
	}
	wantAdmin, err := wgderive.AdminTunnelIP(w.master, "laptop", w.subnet)
	if err != nil {
		t.Fatal(err)
	}
	if zone["laptop"] != wantAdmin {
		t.Errorf("laptop: got %v, want %v", zone["laptop"], wantAdmin)
	}
}

func TestBuildDNSZoneRejectsBadNames(t *testing.T) {
	cases := map[string]map[string]machine{
		"name collision": {
			"aa:bb:cc:dd:ee:01": {Name: "cp1", WGIP: "10.99.0.10"},
			"aa:bb:cc:dd:ee:02": {Name: "cp1", WGIP: "10.99.0.11"},
		},
		"reserved hub name": {
			"aa:bb:cc:dd:ee:01": {Name: "hub", WGIP: "10.99.0.10"},
		},
		"admin collision": {
			"aa:bb:cc:dd:ee:01": {Name: "laptop", WGIP: "10.99.0.10"},
		},
		"invalid label": {
			"aa:bb:cc:dd:ee:01": {Name: "bad_name", WGIP: "10.99.0.10"},
		},
	}
	for desc, machines := range cases {
		if _, err := buildDNSZone(machines, dnsSettings(t)); err == nil {
			t.Errorf("%s: expected error", desc)
		}
	}
}

// mkQuery builds a raw single-question DNS query.
func mkQuery(t *testing.T, name string, qtype dnsmessage.Type) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 42, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	err := b.Question(dnsmessage.Question{
		Name:  dnsmessage.MustNewName(name),
		Type:  qtype,
		Class: dnsmessage.ClassINET,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDNSRespond(t *testing.T) {
	zone := map[string]netip.Addr{"cp1": netip.MustParseAddr("10.99.0.54")}

	cases := []struct {
		desc    string
		name    string
		qtype   dnsmessage.Type
		rcode   dnsmessage.RCode
		answers int
	}{
		{"known A", "cp1.talos.wg.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 1},
		{"case-insensitive", "CP1.Talos.WG.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 1},
		{"known AAAA is empty NOERROR", "cp1.talos.wg.", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess, 0},
		{"unknown in zone", "nope.talos.wg.", dnsmessage.TypeA, dnsmessage.RCodeNameError, 0},
		{"apex", "talos.wg.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 0},
		{"out of zone", "example.com.", dnsmessage.TypeA, dnsmessage.RCodeRefused, 0},
	}
	for _, tc := range cases {
		resp := dnsRespond(zone, "talos.wg", mkQuery(t, tc.name, tc.qtype))
		if resp == nil {
			t.Fatalf("%s: nil response", tc.desc)
		}
		var msg dnsmessage.Message
		if err := msg.Unpack(resp); err != nil {
			t.Fatalf("%s: unpacking response: %v", tc.desc, err)
		}
		if msg.Header.ID != 42 || !msg.Header.Response || !msg.Header.Authoritative {
			t.Errorf("%s: bad header %+v", tc.desc, msg.Header)
		}
		if msg.Header.RCode != tc.rcode {
			t.Errorf("%s: rcode %v, want %v", tc.desc, msg.Header.RCode, tc.rcode)
		}
		if len(msg.Answers) != tc.answers {
			t.Errorf("%s: %d answers, want %d", tc.desc, len(msg.Answers), tc.answers)
		}
		if tc.answers == 1 {
			a, ok := msg.Answers[0].Body.(*dnsmessage.AResource)
			if !ok || netip.AddrFrom4(a.A) != zone["cp1"] {
				t.Errorf("%s: answer %v, want %v", tc.desc, msg.Answers[0].Body, zone["cp1"])
			}
		}
	}

	// Garbage and responses must be dropped, not answered.
	if dnsRespond(zone, "talos.wg", []byte("bogus")) != nil {
		t.Error("garbage input answered")
	}
	reply := dnsRespond(zone, "talos.wg", mkQuery(t, "cp1.talos.wg.", dnsmessage.TypeA))
	if dnsRespond(zone, "talos.wg", reply) != nil {
		t.Error("response packet answered (loop risk)")
	}
}

// TestHubServesDNS is the end-to-end check: a real hub with the DNS
// listener up, a real admin peer, and an A query through the tunnel.
func TestHubServesDNS(t *testing.T) {
	master, err := wgderive.MasterFromHex("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	subnet := netip.MustParsePrefix("10.99.0.0/24")
	serverPriv := wgderive.ServerKey(master)
	adminPriv := wgderive.AdminKey(master, "laptop")
	adminIP, err := wgderive.AdminTunnelIP(master, "laptop", subnet)
	if err != nil {
		t.Fatal(err)
	}

	port := 30000 + int(adminPriv[1])%20000
	tnet, dev, err := startWireGuard(serverPriv, port, netip.MustParseAddr("10.99.0.1"), []wgPeer{
		{publicKeyHex: wgderive.KeyHex(wgderive.PublicKey(adminPriv)), allowedIP: netip.PrefixFrom(adminIP, 32)},
	})
	if err != nil {
		t.Fatalf("starting hub: %v", err)
	}
	t.Cleanup(dev.Close)

	w := &wgSettings{serverIP: netip.MustParseAddr("10.99.0.1"), dnsDomain: "talos.wg", tnet: tnet}
	if err := w.serveDNS(map[string]netip.Addr{"cp1": netip.MustParseAddr("10.99.0.54")}); err != nil {
		t.Fatal(err)
	}

	admin := testPeer(t, adminPriv, adminIP, wgderive.KeyHex(wgderive.PublicKey(serverPriv)), port)
	query := mkQuery(t, "cp1.talos.wg.", dnsmessage.TypeA)

	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		c, err := admin.Dial("udp", "10.99.0.1:53")
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		_, _ = c.Write(query)
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 1500)
		n, err := c.Read(buf)
		c.Close()
		if err != nil {
			lastErr = err
			continue
		}
		var msg dnsmessage.Message
		if err := msg.Unpack(buf[:n]); err != nil {
			t.Fatalf("unpacking tunnel DNS response: %v", err)
		}
		if len(msg.Answers) != 1 {
			t.Fatalf("got %d answers, want 1: %+v", len(msg.Answers), msg)
		}
		if a := msg.Answers[0].Body.(*dnsmessage.AResource); netip.AddrFrom4(a.A) != netip.MustParseAddr("10.99.0.54") {
			t.Fatalf("answer %v, want 10.99.0.54", a)
		}
		return // success
	}
	t.Fatalf("never got a DNS answer through the tunnel: %v", lastErr)
}

// TestMachinePatchDNSSAN: with tunnel DNS on, the machine's DNS name
// joins its tunnel IP in certSANs; with it off, only the IP.
func TestMachinePatchDNSSAN(t *testing.T) {
	m := machine{Name: "cp1", WGIP: "10.99.0.54"}

	w := dnsSettings(t)
	patch, err := w.machinePatch("b0:41:6f:15:3b:8f", m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "- cp1.talos.wg") {
		t.Errorf("patch missing DNS SAN:\n%s", patch)
	}

	w.dnsDomain = ""
	patch, err = w.machinePatch("b0:41:6f:15:3b:8f", m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(patch, "talos.wg") {
		t.Errorf("patch has DNS SAN with tunnel DNS disabled:\n%s", patch)
	}
}
