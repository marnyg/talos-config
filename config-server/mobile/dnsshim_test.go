package mobile

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/slackhq/nebula/routing"
)

var (
	testClient = netip.MustParseAddr("10.42.9.9")
	testHub    = netip.MustParseAddr("10.42.0.1")
	testMagic  = netip.MustParseAddr("10.42.0.53")
)

// fakeDevice is the tun stand-in: queued packets feed Read; Write
// lands on a channel.
type fakeDevice struct {
	in     chan []byte
	writes chan []byte
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{in: make(chan []byte, 8), writes: make(chan []byte, 8)}
}

func (f *fakeDevice) Read(b []byte) (int, error) {
	p, ok := <-f.in
	if !ok {
		return 0, io.EOF
	}
	return copy(b, p), nil
}

func (f *fakeDevice) Write(b []byte) (int, error) {
	c := make([]byte, len(b))
	copy(c, b)
	f.writes <- c
	return len(b), nil
}

func (f *fakeDevice) Close() error    { return nil }
func (f *fakeDevice) Activate() error { return nil }
func (f *fakeDevice) Name() string    { return "fake" }
func (f *fakeDevice) Networks() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("10.42.9.9/16")}
}
func (f *fakeDevice) RoutesFor(netip.Addr) routing.Gateways { return nil }
func (f *fakeDevice) SupportsMultiqueue() bool              { return true }
func (f *fakeDevice) NewMultiQueueReader() (io.ReadWriteCloser, error) {
	return nil, errors.New("unused")
}

func testShim(t *testing.T, upstreams []netip.AddrPort, p SocketProtector) (*dnsDevice, *fakeDevice) {
	t.Helper()
	if upstreams == nil {
		upstreams = []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")} // never dialed in these tests
	}
	fake := newFakeDevice()
	d, err := newDNSDevice(fake, testHub, upstreams, p)
	if err != nil {
		t.Fatal(err)
	}
	return d, fake
}

// dnsQueryMsg builds a minimal one-question DNS query (type A, class IN).
func dnsQueryMsg(name string) []byte {
	msg := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	return append(msg, 0, 0, 1, 0, 1)
}

// verifyChecksums recomputes both checksums of an IPv4 TCP/UDP packet
// and compares against the stored values.
func verifyChecksums(t *testing.T, pkt []byte) {
	t.Helper()
	ihl := int(pkt[0]&0x0f) * 4
	proto := pkt[9]
	ckOff := ihl + 6 // UDP
	if proto == protoTCP {
		ckOff = ihl + 16
	}
	gotIP := binary.BigEndian.Uint16(pkt[10:12])
	gotL4 := binary.BigEndian.Uint16(pkt[ckOff : ckOff+2])
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	cp[10], cp[11] = 0, 0
	cp[ckOff], cp[ckOff+1] = 0, 0
	if want := ipChecksum(cp[:ihl]); gotIP != want {
		t.Errorf("IP checksum = %#x, want %#x", gotIP, want)
	}
	if want := transportChecksum(cp, ihl, proto); gotL4 != want {
		t.Errorf("L4 checksum = %#x, want %#x", gotL4, want)
	}
}

// tcpSegment builds a minimal IPv4/TCP packet (20-byte headers, no
// payload) for feeding the shim's RST path.
func tcpSegment(src netip.Addr, sport uint16, dst netip.Addr, dport uint16, seq, ack uint32, flags byte) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], 40)
	pkt[8] = 64
	pkt[9] = protoTCP
	s4, d4 := src.As4(), dst.As4()
	copy(pkt[12:16], s4[:])
	copy(pkt[16:20], d4[:])
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:20]))
	t := pkt[20:]
	binary.BigEndian.PutUint16(t[0:2], sport)
	binary.BigEndian.PutUint16(t[2:4], dport)
	binary.BigEndian.PutUint32(t[4:8], seq)
	binary.BigEndian.PutUint32(t[8:12], ack)
	t[12] = 0x50
	t[13] = flags
	binary.BigEndian.PutUint16(t[16:18], transportChecksum(pkt, 20, protoTCP))
	return pkt
}

func TestDNSMagicIP(t *testing.T) {
	got, err := dnsMagicIP(netip.MustParsePrefix("10.42.9.9/16"))
	if err != nil {
		t.Fatal(err)
	}
	if got != testMagic {
		t.Errorf("magic IP = %s, want %s", got, testMagic)
	}
	if _, err := dnsMagicIP(netip.MustParsePrefix("fd00::/64")); err == nil {
		t.Error("expected error for IPv6 network")
	}
}

func TestDNSQName(t *testing.T) {
	name, ok := dnsQName(dnsQueryMsg("Jellyfin.MESH.internal"))
	if !ok || name != "jellyfin.mesh.internal." {
		t.Errorf("qname = %q, %v; want normalized lowercase with trailing dot", name, ok)
	}
	for _, bad := range [][]byte{
		nil,
		make([]byte, 11),                     // runt
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, // qdcount 0
		append(dnsQueryMsg("x")[:12], 0xc0, 0x0c), // compression pointer
		append(dnsQueryMsg("x")[:12], 5, 'a'),     // label runs past end
	} {
		if _, ok := dnsQName(bad); ok {
			t.Errorf("dnsQName accepted invalid message %v", bad)
		}
	}
}

func TestReadRewritesMeshQueryToHub(t *testing.T) {
	d, fake := testShim(t, nil, nil)
	fake.in <- buildIPv4UDP(testClient, 40000, testMagic, 53, dnsQueryMsg("jellyfin.mesh.internal"))

	buf := make([]byte, 1500)
	n, err := d.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := parseIPv4UDP(buf[:n])
	if !ok {
		t.Fatal("rewritten packet does not parse")
	}
	if m.dst != testHub || m.dport != 53 {
		t.Errorf("dst = %s:%d, want %s:53", m.dst, m.dport, testHub)
	}
	if m.src != testClient || m.sport != 40000 {
		t.Errorf("src mangled: %s:%d", m.src, m.sport)
	}
	verifyChecksums(t, buf[:n])
}

func TestReadPassesZoneApexToHub(t *testing.T) {
	d, fake := testShim(t, nil, nil)
	fake.in <- buildIPv4UDP(testClient, 40000, testMagic, 53, dnsQueryMsg("mesh.internal"))

	buf := make([]byte, 1500)
	n, err := d.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := parseIPv4UDP(buf[:n]); m.dst != testHub {
		t.Errorf("apex query dst = %s, want hub", m.dst)
	}
}

func TestWriteRewritesHubReplySource(t *testing.T) {
	d, fake := testShim(t, nil, nil)
	reply := buildIPv4UDP(testHub, 53, testClient, 40000, dnsQueryMsg("jellyfin.mesh.internal"))
	if _, err := d.Write(reply); err != nil {
		t.Fatal(err)
	}
	got := <-fake.writes
	m, ok := parseIPv4UDP(got)
	if !ok {
		t.Fatal("rewritten reply does not parse")
	}
	if m.src != testMagic || m.sport != 53 {
		t.Errorf("src = %s:%d, want %s:53", m.src, m.sport, testMagic)
	}
	verifyChecksums(t, got)
}

func TestWriteLeavesOtherTrafficAlone(t *testing.T) {
	d, fake := testShim(t, nil, nil)
	pkt := buildIPv4UDP(testHub, 8080, testClient, 40000, []byte("payload"))
	want := make([]byte, len(pkt))
	copy(want, pkt)
	if _, err := d.Write(pkt); err != nil {
		t.Fatal(err)
	}
	got := <-fake.writes
	if string(got) != string(want) {
		t.Error("non-DNS packet was modified on Write")
	}
}

type recordingProtector struct{ fds chan int32 }

func (p *recordingProtector) Protect(fd int32) bool {
	p.fds <- fd
	return true
}

func TestReadForwardsNonMeshOnUnderlay(t *testing.T) {
	// A loopback UDP server plays the underlay resolver.
	upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	answer := []byte("fake-dns-answer")
	go func() {
		buf := make([]byte, 1500)
		n, addr, err := upstream.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) != string(dnsQueryMsg("example.com")) {
			panic("upstream got a different query than the client sent")
		}
		upstream.WriteToUDP(answer, addr) //nolint:errcheck
	}()

	prot := &recordingProtector{fds: make(chan int32, 1)}
	d, fake := testShim(t, []netip.AddrPort{netip.MustParseAddrPort(upstream.LocalAddr().String())}, prot)

	fake.in <- buildIPv4UDP(testClient, 41000, testMagic, 53, dnsQueryMsg("example.com"))
	marker := buildIPv4UDP(testClient, 42000, testHub, 80, []byte("not dns"))
	fake.in <- marker

	// Read must skip the intercepted query and hand nebula the marker.
	buf := make([]byte, 1500)
	n, err := d.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != string(marker) {
		t.Fatal("Read returned the intercepted DNS query instead of the next packet")
	}

	// The reply must appear on the tun as magic:53 → client.
	select {
	case got := <-fake.writes:
		m, ok := parseIPv4UDP(got)
		if !ok {
			t.Fatal("crafted reply does not parse")
		}
		if m.src != testMagic || m.sport != 53 || m.dst != testClient || m.dport != 41000 {
			t.Errorf("reply addressing = %s:%d → %s:%d", m.src, m.sport, m.dst, m.dport)
		}
		if string(m.payload) != string(answer) {
			t.Errorf("reply payload = %q, want %q", m.payload, answer)
		}
		verifyChecksums(t, got)
	case <-time.After(5 * time.Second):
		t.Fatal("no reply written to the tun")
	}

	select {
	case <-prot.fds:
	default:
		t.Error("underlay socket was never protected")
	}
}

func TestReadPassesUnrelatedPacketsThrough(t *testing.T) {
	d, fake := testShim(t, nil, nil)
	for _, pkt := range [][]byte{
		buildIPv4UDP(testClient, 40000, testHub, 53, dnsQueryMsg("x.mesh.internal")), // real hub, not magic
		buildIPv4UDP(testClient, 40000, testMagic, 80, []byte("not port 53")),
		{0x60, 0, 0, 0, 0, 0, 0, 0}, // IPv6-looking runt
	} {
		want := make([]byte, len(pkt))
		copy(want, pkt)
		fake.in <- pkt
		buf := make([]byte, 1500)
		n, err := d.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if string(buf[:n]) != string(want) {
			t.Errorf("packet was modified or intercepted: %v", want)
		}
	}
}

func TestReadResetsTCPToMagicIP(t *testing.T) {
	d, fake := testShim(t, nil, nil)

	// A DoT probe: SYN to magic:853, no ACK → RST|ACK acking seq+1.
	fake.in <- tcpSegment(testClient, 5555, testMagic, 853, 1000, 0, 0x02)
	marker := buildIPv4UDP(testClient, 42000, testHub, 80, []byte("not dns"))
	fake.in <- marker

	buf := make([]byte, 1500)
	n, err := d.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != string(marker) {
		t.Fatal("TCP segment to magic IP reached nebula")
	}

	select {
	case rst := <-fake.writes:
		ip, ok := parseIPv4(rst)
		if !ok || ip.proto != protoTCP {
			t.Fatal("reply is not IPv4/TCP")
		}
		if ip.src != testMagic || ip.dst != testClient {
			t.Errorf("RST addressing = %s → %s", ip.src, ip.dst)
		}
		tcp := rst[20:]
		if got := binary.BigEndian.Uint16(tcp[0:2]); got != 853 {
			t.Errorf("RST src port = %d, want 853", got)
		}
		if tcp[13] != 0x14 {
			t.Errorf("flags = %#x, want RST|ACK", tcp[13])
		}
		if got := binary.BigEndian.Uint32(tcp[8:12]); got != 1001 {
			t.Errorf("RST ack = %d, want 1001 (SYN counts)", got)
		}
		verifyChecksums(t, rst)
	case <-time.After(time.Second):
		t.Fatal("no RST written to the tun")
	}

	// A segment carrying ACK → plain RST at SEQ=SEG.ACK.
	fake.in <- tcpSegment(testClient, 5555, testMagic, 53, 2000, 7777, 0x10)
	fake.in <- marker
	if _, err := d.Read(buf); err != nil {
		t.Fatal(err)
	}
	rst := <-fake.writes
	tcp := rst[20:]
	if tcp[13] != 0x04 {
		t.Errorf("flags = %#x, want plain RST", tcp[13])
	}
	if got := binary.BigEndian.Uint32(tcp[4:8]); got != 7777 {
		t.Errorf("RST seq = %d, want SEG.ACK 7777", got)
	}

	// A RST to magic must not be answered (no RST-on-RST loops).
	fake.in <- tcpSegment(testClient, 5555, testMagic, 853, 3000, 0, 0x04)
	fake.in <- marker
	if _, err := d.Read(buf); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.writes:
		t.Error("answered a RST with a RST")
	default:
	}
}

func TestReadPassesTCPToOthersThrough(t *testing.T) {
	d, fake := testShim(t, nil, nil)
	pkt := tcpSegment(testClient, 5555, testHub, 80, 1, 0, 0x02) // real host, not magic
	want := make([]byte, len(pkt))
	copy(want, pkt)
	fake.in <- pkt
	buf := make([]byte, 1500)
	n, err := d.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != string(want) {
		t.Error("TCP to a real host was intercepted or modified")
	}
}

func TestSetUpstreamsSwapsResolvers(t *testing.T) {
	newUpstream := func(answer string) (*net.UDPConn, netip.AddrPort) {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			buf := make([]byte, 1500)
			for {
				_, addr, err := c.ReadFromUDP(buf)
				if err != nil {
					return
				}
				c.WriteToUDP([]byte(answer), addr) //nolint:errcheck
			}
		}()
		return c, netip.MustParseAddrPort(c.LocalAddr().String())
	}
	aConn, aAddr := newUpstream("answer-A")
	defer aConn.Close()
	bConn, bAddr := newUpstream("answer-B")
	defer bConn.Close()

	d, fake := testShim(t, []netip.AddrPort{aAddr}, nil)
	ask := func(want string) {
		t.Helper()
		fake.in <- buildIPv4UDP(testClient, 41000, testMagic, 53, dnsQueryMsg("example.com"))
		fake.in <- buildIPv4UDP(testClient, 42000, testHub, 80, []byte("marker"))
		buf := make([]byte, 1500)
		if _, err := d.Read(buf); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-fake.writes:
			m, _ := parseIPv4UDP(got)
			if string(m.payload) != want {
				t.Errorf("answer = %q, want %q", m.payload, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no reply")
		}
	}

	ask("answer-A")
	d.setUpstreams(parseUpstreams(bAddr.String()))
	ask("answer-B")
	d.setUpstreams(nil) // empty: ignored, B stays
	ask("answer-B")
}

func TestParseUpstreams(t *testing.T) {
	got := parseUpstreams("192.168.1.1, 8.8.8.8:5353")
	if len(got) != 2 || got[0].String() != "192.168.1.1:53" || got[1].String() != "8.8.8.8:5353" {
		t.Errorf("parseUpstreams = %v", got)
	}
	for _, s := range []string{"", "garbage, more garbage"} {
		if got := parseUpstreams(s); len(got) != 1 || got[0].String() != "1.1.1.1:53" {
			t.Errorf("parseUpstreams(%q) = %v, want the 1.1.1.1 fallback", s, got)
		}
	}
}

func TestParseIPv4UDPRejectsFragments(t *testing.T) {
	pkt := buildIPv4UDP(testClient, 40000, testMagic, 53, dnsQueryMsg("x"))
	binary.BigEndian.PutUint16(pkt[6:8], 0x2000) // MF set
	if _, ok := parseIPv4UDP(pkt); ok {
		t.Error("fragment accepted")
	}
}

func TestNewDNSDeviceRejectsBadInput(t *testing.T) {
	fake := newFakeDevice()
	if _, err := newDNSDevice(fake, testHub, nil, nil); err == nil {
		t.Error("expected error for empty upstream list")
	}
	if d, _ := newDNSDevice(fake, testHub, []netip.AddrPort{netip.MustParseAddrPort("1.1.1.1:53")}, nil); d.SupportsMultiqueue() {
		t.Error("shim must not advertise multiqueue (readers would bypass it)")
	}
}
