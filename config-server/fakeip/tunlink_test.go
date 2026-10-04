package fakeip

import (
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// memTun is a tun.Device made of channels: what the host "sends into the
// tun" goes on in, what the stack writes to the tun comes out on out.
// Frames are bare IP, no AF header — the darwin framing is the real
// device's concern, and the offset contract is what we test here.
type memTun struct {
	in, out chan []byte
	closed  chan struct{}
	events  chan tun.Event
	once    sync.Once
}

func newMemTun() *memTun {
	return &memTun{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{}), events: make(chan tun.Event)}
}

func (m *memTun) File() *os.File           { return nil }
func (m *memTun) MTU() (int, error)        { return 1500, nil }
func (m *memTun) Name() (string, error)    { return "memtun", nil }
func (m *memTun) Events() <-chan tun.Event { return m.events }
func (m *memTun) BatchSize() int           { return 1 }
func (m *memTun) Close() error             { m.once.Do(func() { close(m.closed) }); return nil }
func (m *memTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case p := <-m.in:
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	case <-m.closed:
		return 0, os.ErrClosed
	}
}
func (m *memTun) Write(bufs [][]byte, offset int) (int, error) {
	for _, b := range bufs {
		if offset < 4 {
			panic("darwin contract: offset must be >= 4") // what NativeTun.Write enforces
		}
		m.out <- append([]byte(nil), b[offset:]...)
	}
	return len(bufs), nil
}

// udp4 builds a bare IPv4+UDP packet with correct checksums.
func udp4(src, dst netip.AddrPort, payload []byte) []byte {
	pkt := make([]byte, header.IPv4MinimumSize+header.UDPMinimumSize+len(payload))
	ip := header.IPv4(pkt)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(pkt)), TTL: 64, Protocol: uint8(header.UDPProtocolNumber),
		SrcAddr: tcpip.AddrFrom4(src.Addr().As4()), DstAddr: tcpip.AddrFrom4(dst.Addr().As4()),
	})
	ip.SetChecksum(^ip.CalculateChecksum())
	u := header.UDP(pkt[header.IPv4MinimumSize:])
	u.Encode(&header.UDPFields{SrcPort: src.Port(), DstPort: dst.Port(), Length: uint16(header.UDPMinimumSize + len(payload))})
	copy(u.Payload(), payload)
	xsum := header.PseudoHeaderChecksum(header.UDPProtocolNumber, ip.SourceAddress(), ip.DestinationAddress(), u.Length())
	u.SetChecksum(^u.CalculateChecksum(checksum.Checksum(payload, xsum)))
	return pkt
}

// A DNS query written into the tun comes back out as a reply from the
// resolver address: the whole read → inject → stack → handler → write
// path, with the darwin offset contract observed.
func TestTunLinkRoundTripsDNS(t *testing.T) {
	dev := newMemTun()
	link, err := NewTunLink(dev, 8)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := NewResolver(ResolverOptions{Directory: DirectoryFunc(func(n string) bool { return n == "cp1" })})
	flows := make(chan netip.AddrPort, 1)
	s, err := NewStack(link, func(c Conn, dst netip.AddrPort) { c.Close(); flows <- dst }, r.HandleUDP)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { link.Close(); s.Close() }()

	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 42, RecursionDesired: true})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("cp1.mesh.internal."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	q, _ := b.Finish()
	dev.in <- udp4(src, dst, q)

	select {
	case out := <-dev.out:
		ip := header.IPv4(out)
		if !ip.IsValid(len(out)) || ip.SourceAddress() != tcpip.AddrFrom4(dst.Addr().As4()) || ip.DestinationAddress() != tcpip.AddrFrom4(src.Addr().As4()) {
			t.Fatalf("reply not from resolver to client: %v → %v", ip.SourceAddress(), ip.DestinationAddress())
		}
		u := header.UDP(out[ip.HeaderLength():])
		if u.SourcePort() != 53 || u.DestinationPort() != src.Port() {
			t.Fatalf("ports %d → %d", u.SourcePort(), u.DestinationPort())
		}
		hdr, ips := parse(t, u.Payload())
		if hdr.ID != 42 || len(ips) != 1 || ips[0] != poolStart {
			t.Fatalf("id %d ips %v", hdr.ID, ips)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reply left the tun")
	}
	if len(flows) != 0 {
		t.Error("a UDP query became a TCP flow")
	}
}

// The device going away is surfaced on Done/Err rather than swallowed,
// so the daemon can exit and let launchd restart it.
func TestTunLinkReportsDeviceLoss(t *testing.T) {
	dev := newMemTun()
	link, err := NewTunLink(dev, 8)
	if err != nil {
		t.Fatal(err)
	}
	_ = dev.Close()
	select {
	case <-link.Done():
		if link.Err() == nil {
			t.Error("Err nil after device loss")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("link did not notice device loss")
	}
	link.Close()
}

// tcp4 builds a bare IPv4+TCP SYN with correct checksums.
func tcp4syn(src, dst netip.AddrPort) []byte {
	pkt := make([]byte, header.IPv4MinimumSize+header.TCPMinimumSize)
	ip := header.IPv4(pkt)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(pkt)), TTL: 64, Protocol: uint8(header.TCPProtocolNumber),
		SrcAddr: tcpip.AddrFrom4(src.Addr().As4()), DstAddr: tcpip.AddrFrom4(dst.Addr().As4()),
	})
	ip.SetChecksum(^ip.CalculateChecksum())
	tc := header.TCP(pkt[header.IPv4MinimumSize:])
	tc.Encode(&header.TCPFields{
		SrcPort: src.Port(), DstPort: dst.Port(), SeqNum: 1000, DataOffset: header.TCPMinimumSize,
		Flags: header.TCPFlagSyn, WindowSize: 65535,
	})
	xsum := header.PseudoHeaderChecksum(header.TCPProtocolNumber, ip.SourceAddress(), ip.DestinationAddress(), uint16(header.TCPMinimumSize))
	tc.SetChecksum(^tc.CalculateChecksum(xsum))
	return pkt
}

// A TCP SYN at the resolver (Android's Private DNS probing DoT :853) or
// the tun's own address is refused with an RST before any flow exists:
// the handler never sees it, so it costs no flow error and no log line
// (359.9.4.3). A SYN at a minted fake IP still becomes a flow.
func TestTCPToResolverIsRefused(t *testing.T) {
	dev := newMemTun()
	link, err := NewTunLink(dev, 8)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := NewResolver(ResolverOptions{Directory: DirectoryFunc(func(n string) bool { return n == "cp1" })})
	flows := make(chan netip.AddrPort, 1)
	s, err := NewStack(link, func(c Conn, dst netip.AddrPort) { c.Close(); flows <- dst }, r.HandleUDP)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { link.Close(); s.Close() }()

	client := netip.MustParseAddrPort("198.18.0.1:40000")
	for _, target := range []netip.AddrPort{netip.MustParseAddrPort(ResolverIP + ":853"), netip.MustParseAddrPort(TunIP + ":80")} {
		dev.in <- tcp4syn(client, target)
		select {
		case out := <-dev.out:
			ip := header.IPv4(out)
			tc := header.TCP(out[ip.HeaderLength():])
			if ip.SourceAddress() != tcpip.AddrFrom4(target.Addr().As4()) || tc.Flags()&header.TCPFlagRst == 0 {
				t.Fatalf("%s: expected RST from the target, got flags %v from %v", target, tc.Flags(), ip.SourceAddress())
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: nothing left the tun", target)
		}
		select {
		case d := <-flows:
			t.Fatalf("%s: SYN reached the flow handler as %s", target, d)
		case <-time.After(200 * time.Millisecond):
		}
	}

	if got := s.Stats.TCPRefused.Load(); got != 2 {
		t.Errorf("TCPRefused = %d, want 2", got)
	}
	// A non-DNS datagram is dropped and counted, not handed anywhere.
	dev.in <- udp4(client, netip.MustParseAddrPort(ResolverIP+":853"), []byte("hello"))
	select {
	case out := <-dev.out:
		t.Fatalf("non-DNS datagram got a %d-byte answer", len(out))
	case <-time.After(200 * time.Millisecond):
	}
	if got := s.Stats.UDPDropped.Load(); got != 1 {
		t.Errorf("UDPDropped = %d, want 1", got)
	}

	// Mint cp1 a fake IP (straight at the resolver, no packet involved),
	// then a SYN there is accepted and handed over.
	r.HandleUDP(src, dst, query(t, "cp1.mesh.internal.", dnsmessage.TypeA))
	dev.in <- tcp4syn(client, netip.AddrPortFrom(poolStart, 8096))
	select {
	case out := <-dev.out:
		tc := header.TCP(out[header.IPv4(out).HeaderLength():])
		if tc.Flags()&header.TCPFlagSyn == 0 || tc.Flags()&header.TCPFlagAck == 0 {
			t.Fatalf("fake IP: expected SYN-ACK, got %v", tc.Flags())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake IP: nothing left the tun")
	}
}
