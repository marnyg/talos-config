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
