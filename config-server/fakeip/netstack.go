// Package fakeip is the presentation layer of Mesh v3 on a tun: the
// device-local fiction that lets IP-speaking clients (talosctl, kubectl,
// a browser, Jellyfin) reach members that are really dialed by key.
//
//	tun → gvisor netstack → { UDP/53 to ResolverIP: map-gated split DNS (Resolver);
//	                           TCP to a fake IP:  one flow, handed to the caller }
//
// Fake-IP plan (RFC 2544 benchmark range, 198.18.0.0/15 — decision 4fm,
// shared with mobile so there is one dialect):
//
//	198.18.0.1   the tun's own address
//	198.18.0.2   the resolver the tun advertises
//	198.18.1.1+  one address per name in the plane's name map, first-seen
//	             (.0 skipped: macOS route tooling treats x.x.x.0 as a net)
//
// Only the /15 is routed into the tun. A name's fake IP is stable for
// the life of the process (clients cache the server address).
//
// The package knows nothing about members, certs or iroh: the caller
// supplies a Directory (which names exist) and a flow handler (what to
// do with a TCP connection to a fake IP). Extracted from iroh-go/mobile
// (P0.2, Android) for the desktop daemon (359.9.6); mobile is the next
// consumer.
package fakeip

import (
	"fmt"
	"log"
	"net"
	"net/netip"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	FakeRange  = "198.18.0.0/15"
	TunIP      = "198.18.0.1"
	ResolverIP = "198.18.0.2"
)

var (
	fakePrefix = netip.MustParsePrefix(FakeRange)
	poolStart  = netip.MustParseAddr("198.18.1.1")
)

// Conn is what the stack hands the flow handler for an accepted TCP
// flow (*gonet.TCPConn satisfies it). CloseWrite lets a half-close
// propagate to the other side of the bridge.
type Conn interface {
	net.Conn
	CloseWrite() error
}

// FlowHandler runs one accepted TCP flow; dst is the fake IP:port the
// client dialed. It owns app and must close it.
type FlowHandler func(app Conn, dst netip.AddrPort)

// UDPHandler answers one datagram; reply != nil is written back to the
// client from the datagram's destination. Used for DNS only.
type UDPHandler func(src, dst netip.AddrPort, payload []byte) (reply []byte)

// Stack is the tun2socks shape: one NIC on the link in promiscuous +
// spoofing mode (accept any destination, answer from any source), a
// default route, and forwarders instead of listeners — every TCP SYN
// and every UDP datagram the host routes into the tun lands in a
// callback. No address plan: fake IPs are the Resolver's business.
type Stack struct {
	s *stack.Stack
}

const nicID tcpip.NICID = 1

// NewStack builds the stack on link. The link stays the caller's:
// Close stops the stack, not the link (close the link first so no
// packet is injected into a closed stack).
func NewStack(link stack.LinkEndpoint, onTCP FlowHandler, onUDP UDPHandler) (*Stack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if e := s.CreateNIC(nicID, link); e != nil {
		return nil, fmt.Errorf("CreateNIC: %s", e)
	}
	if e := s.SetPromiscuousMode(nicID, true); e != nil {
		return nil, fmt.Errorf("promiscuous: %s", e)
	}
	if e := s.SetSpoofing(nicID, true); e != nil {
		return nil, fmt.Errorf("spoofing: %s", e)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: nicID}})

	// Throughput knobs: the 80 Mbps floor with one flow means the receive
	// window must cover RTT × rate — 4 MiB is generous for a LAN path and
	// what tun2socks uses; SACK and moderate receive buffers are gvisor's
	// defaults elsewhere but off in a bare stack.
	{
		opt := tcpip.TCPSACKEnabled(true)
		if e := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); e != nil {
			return nil, fmt.Errorf("sack: %s", e)
		}
		rcv := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4 << 10, Default: 1 << 20, Max: 4 << 20}
		if e := s.SetTransportProtocolOption(tcp.ProtocolNumber, &rcv); e != nil {
			return nil, fmt.Errorf("rcvbuf: %s", e)
		}
		snd := tcpip.TCPSendBufferSizeRangeOption{Min: 4 << 10, Default: 1 << 20, Max: 4 << 20}
		if e := s.SetTransportProtocolOption(tcp.ProtocolNumber, &snd); e != nil {
			return nil, fmt.Errorf("sndbuf: %s", e)
		}
		mod := tcpip.TCPModerateReceiveBufferOption(true)
		if e := s.SetTransportProtocolOption(tcp.ProtocolNumber, &mod); e != nil {
			return nil, fmt.Errorf("moderate rcvbuf: %s", e)
		}
	}

	tcpFwd := tcp.NewForwarder(s, 0, 1024, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		dst := netip.AddrPortFrom(netip.AddrFrom4(id.LocalAddress.As4()), id.LocalPort)
		var wq waiter.Queue
		ep, e := r.CreateEndpoint(&wq)
		if e != nil {
			log.Printf("fakeip: tcp %s: create endpoint: %s", dst, e)
			r.Complete(true) // RST
			return
		}
		r.Complete(false)
		go onTCP(gonet.NewTCPConn(&wq, ep), dst)
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		id := r.ID()
		dst := netip.AddrPortFrom(netip.AddrFrom4(id.LocalAddress.As4()), id.LocalPort)
		src := netip.AddrPortFrom(netip.AddrFrom4(id.RemoteAddress.As4()), id.RemotePort)
		if dst.Port() != 53 {
			return // not DNS: drop (only the fake range is routed here)
		}
		var wq waiter.Queue
		ep, e := r.CreateEndpoint(&wq)
		if e != nil {
			log.Printf("fakeip: udp %s: create endpoint: %s", dst, e)
			return
		}
		// One datagram per endpoint: DNS is request/response and the
		// connected UDP endpoint gonet gives us already has both tuples.
		go func() {
			c := gonet.NewUDPConn(&wq, ep)
			defer c.Close()
			buf := make([]byte, 4096)
			n, _, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply := onUDP(src, dst, buf[:n]); reply != nil {
				_, _ = c.Write(reply)
			}
		}()
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	return &Stack{s: s}, nil
}

// Close stops the stack and waits for its goroutines.
func (n *Stack) Close() {
	n.s.Close()
	n.s.Wait()
}
