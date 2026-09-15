package p0mobile

import (
	"fmt"
	"log"
	"net/netip"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/fdbased"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// netstack is the tun2socks shape: one NIC on the tun fd in promiscuous +
// spoofing mode (accept any destination, answer from any source), a
// default route, and forwarders instead of listeners — every TCP SYN and
// every UDP datagram the phone routes into the tun lands in a callback.
// Same gvisor as the hub's nebstack, without an address plan: fake IPs
// are the DNS layer's business, not the stack's.
type netstack struct {
	s *stack.Stack
}

const nicID tcpip.NICID = 1

// udpHandler answers one datagram; reply != nil is written back to the
// app from the datagram's destination. Used for DNS only.
type udpHandler func(src, dst netip.AddrPort, payload []byte) (reply []byte)

func newNetstack(fd int, mtu uint32, onTCP func(tcpConn, netip.AddrPort), onUDP udpHandler) (*netstack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	// fdbased picks a readv dispatcher for non-socket fds, which is what
	// an Android tun is (packet per read, no ethernet header).
	link, err := fdbased.New(&fdbased.Options{FDs: []int{fd}, MTU: mtu, EthernetHeader: false})
	if err != nil {
		return nil, fmt.Errorf("fdbased: %w", err)
	}
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
			log.Printf("tcp %s: create endpoint: %s", dst, e)
			r.Complete(true) // RST
			return
		}
		r.Complete(false)
		go onTCP(gonet.NewTCPConn(&wq, ep), dst)
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		dst := netip.AddrPortFrom(netip.AddrFrom4(id.LocalAddress.As4()), id.LocalPort)
		src := netip.AddrPortFrom(netip.AddrFrom4(id.RemoteAddress.As4()), id.RemotePort)
		if dst.Port() != 53 {
			return false // not DNS: drop (only the fake range is routed here)
		}
		var wq waiter.Queue
		ep, e := r.CreateEndpoint(&wq)
		if e != nil {
			log.Printf("udp %s: create endpoint: %s", dst, e)
			return true
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
		return true
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	return &netstack{s: s}, nil
}

func (n *netstack) close() {
	n.s.Close()
	n.s.Wait()
}
