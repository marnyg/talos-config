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

// verifyChecksums recomputes both checksums of an IPv4/UDP packet and
// compares against the stored values.
func verifyChecksums(t *testing.T, pkt []byte) {
	t.Helper()
	ihl := int(pkt[0]&0x0f) * 4
	gotIP := binary.BigEndian.Uint16(pkt[10:12])
	gotUDP := binary.BigEndian.Uint16(pkt[26:28])
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	cp[10], cp[11] = 0, 0
	cp[ihl+6], cp[ihl+7] = 0, 0
	if want := ipChecksum(cp[:ihl]); gotIP != want {
		t.Errorf("IP checksum = %#x, want %#x", gotIP, want)
	}
	if want := udpChecksum(cp, ihl); gotUDP != want {
		t.Errorf("UDP checksum = %#x, want %#x", gotUDP, want)
	}
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
