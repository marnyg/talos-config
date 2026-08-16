package mobile

import (
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"
)

// TestDebugSnapshotTracksDecisions drives one packet down each shim
// path and checks the DebugJSON snapshot reflects it: counters bumped,
// events recorded with the right route and qname.
func TestDebugSnapshotTracksDecisions(t *testing.T) {
	// Loopback UDP server plays the underlay resolver (same pattern as
	// TestReadForwardsNonMeshOnUnderlay).
	upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		buf := make([]byte, 1500)
		n, addr, err := upstream.ReadFromUDP(buf)
		if err != nil {
			return
		}
		upstream.WriteToUDP(buf[:n], addr) //nolint:errcheck
	}()

	upAddr := netip.MustParseAddrPort(upstream.LocalAddr().String())
	d, fake := testShim(t, []netip.AddrPort{upAddr}, nil)
	buf := make([]byte, 1500)
	marker := buildIPv4UDP(testClient, 42000, testHub, 80, []byte("not dns"))

	// Mesh query: intercepted, rewritten, returned to nebula.
	fake.in <- buildIPv4UDP(testClient, 40000, testMagic, 53, dnsQueryMsg("jellyfin.mesh.internal"))
	if _, err := d.Read(buf); err != nil {
		t.Fatal(err)
	}

	// Hub reply on the way back in.
	if _, err := d.Write(buildIPv4UDP(testHub, 53, testClient, 40000, dnsQueryMsg("jellyfin.mesh.internal"))); err != nil {
		t.Fatal(err)
	}
	<-fake.writes

	// DoT probe: TCP SYN to the magic resolver → RST, counted.
	fake.in <- tcpSegment(testClient, 5555, testMagic, 853, 1000, 0, 0x02)
	fake.in <- marker
	if _, err := d.Read(buf); err != nil {
		t.Fatal(err)
	}
	<-fake.writes

	// Underlay forward, answered by the loopback resolver.
	fake.in <- buildIPv4UDP(testClient, 41000, testMagic, 53, dnsQueryMsg("example.com"))
	fake.in <- marker
	if _, err := d.Read(buf); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.writes:
	case <-time.After(5 * time.Second):
		t.Fatal("no underlay reply written to the tun")
	}

	snap := d.debugSnapshot()
	if snap.MagicIP != testMagic.String() || snap.HubIP != testHub.String() {
		t.Errorf("addressing = magic %s hub %s", snap.MagicIP, snap.HubIP)
	}
	if len(snap.Upstreams) != 1 || snap.Upstreams[0] != upAddr.String() {
		t.Errorf("upstreams = %v, want [%s]", snap.Upstreams, upAddr)
	}
	want := dnsCounters{MeshQueries: 1, HubReplies: 1, UnderlayQueries: 1, TCPResets: 1}
	if snap.Counters != want {
		t.Errorf("counters = %+v, want %+v", snap.Counters, want)
	}

	if len(snap.Events) != 3 { // TCP resets carry no event
		t.Fatalf("events = %d, want 3: %+v", len(snap.Events), snap.Events)
	}
	for i, wantEv := range []struct{ route, qname, upstream string }{
		{"mesh", "jellyfin.mesh.internal.", ""},
		{"hub-reply", "jellyfin.mesh.internal.", ""},
		{"underlay", "example.com.", upAddr.String()},
	} {
		ev := snap.Events[i]
		if ev.Route != wantEv.route || ev.QName != wantEv.qname || ev.Upstream != wantEv.upstream || !ev.OK {
			t.Errorf("event[%d] = %+v, want route %q qname %q upstream %q ok",
				i, ev, wantEv.route, wantEv.qname, wantEv.upstream)
		}
		if ev.Time == "" {
			t.Errorf("event[%d] has no timestamp", i)
		}
	}
}

// TestDebugUnderlayFailureCounted: an exchange that finds no upstream
// still lands in the event ring, marked failed and counted.
func TestDebugUnderlayFailureCounted(t *testing.T) {
	d, _ := testShim(t, nil, nil)
	d.mu.Lock()
	d.upstreams = nil // force exchange to fail without dialing
	d.mu.Unlock()

	d.forwardUnderlay(testClient, 41000, dnsQueryMsg("example.com"))

	snap := d.debugSnapshot()
	if snap.Counters.UnderlayQueries != 1 || snap.Counters.UnderlayFails != 1 {
		t.Errorf("counters = %+v, want one underlay query, one fail", snap.Counters)
	}
	if len(snap.Events) != 1 || snap.Events[0].OK || snap.Events[0].Route != "underlay" {
		t.Errorf("events = %+v, want one failed underlay event", snap.Events)
	}
}

// TestDebugEventRingCaps: the ring holds the newest dnsDebugEvents
// entries and drops the oldest.
func TestDebugEventRingCaps(t *testing.T) {
	d, _ := testShim(t, nil, nil)
	for i := 0; i < dnsDebugEvents+10; i++ {
		d.record(dnsEvent{QName: fmt.Sprintf("q%d.", i), Route: "mesh", OK: true},
			func(c *dnsCounters) { c.MeshQueries++ })
	}
	snap := d.debugSnapshot()
	if len(snap.Events) != dnsDebugEvents {
		t.Fatalf("ring = %d events, want %d", len(snap.Events), dnsDebugEvents)
	}
	if got, want := snap.Events[0].QName, "q10."; got != want {
		t.Errorf("oldest retained = %s, want %s", got, want)
	}
	if snap.Counters.MeshQueries != uint64(dnsDebugEvents+10) {
		t.Errorf("counters undercounted: %d", snap.Counters.MeshQueries)
	}
}
