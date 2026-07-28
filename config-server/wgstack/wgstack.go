// Package wgstack is a userspace TUN device backed by gVisor's
// netstack, trimmed from wireguard-go's tun/netstack (MIT, Copyright
// (C) 2017-2025 WireGuard LLC) with one behavioral change: IPv4
// forwarding is enabled, so the WireGuard device acting as the tunnel
// hub routes traffic BETWEEN peers (admin laptop → machine, machine →
// machine) instead of only terminating connections addressed to the
// hub itself. Hairpin forwarding goes out the same NIC it came in on;
// wireguard-go then routes the re-emitted packet to the destination
// peer by allowed-ip.
//
// Vendored because upstream keeps the *stack.Stack field unexported —
// same story as the fly-global-services bind (wgbind.go). Only the
// surface the server uses survives the trim: CreateNetTUN,
// DialContextTCPAddrPort (auto-bootstrap dials apid), and ListenTCP
// (tunnel hello).
package wgstack

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"

	"golang.zx2c4.com/wireguard/tun"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

type netTun struct {
	ep             *channel.Endpoint
	stack          *stack.Stack
	events         chan tun.Event
	notifyHandle   *channel.NotificationHandle
	incomingPacket chan *buffer.View
	mtu            int
}

// Net dials and listens on the tunnel through the netstack.
type Net netTun

// CreateNetTUN builds the forwarding-enabled netstack TUN for the hub.
func CreateNetTUN(localAddresses []netip.Addr, mtu int) (tun.Device, *Net, error) {
	opts := stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol6, icmp.NewProtocol4},
		HandleLocal:        true,
	}
	dev := &netTun{
		ep:             channel.New(1024, uint32(mtu), ""),
		stack:          stack.New(opts),
		events:         make(chan tun.Event, 10),
		incomingPacket: make(chan *buffer.View),
		mtu:            mtu,
	}
	sackEnabledOpt := tcpip.TCPSACKEnabled(true) // TCP SACK is disabled by default
	if err := dev.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &sackEnabledOpt); err != nil {
		return nil, nil, fmt.Errorf("could not enable TCP SACK: %v", err)
	}
	// The change vs upstream: transit packets (dst ≠ hub address) are
	// forwarded back out the NIC instead of dropped.
	if err := dev.stack.SetForwardingDefaultAndAllNICs(ipv4.ProtocolNumber, true); err != nil {
		return nil, nil, fmt.Errorf("enabling IPv4 forwarding: %v", err)
	}
	dev.notifyHandle = dev.ep.AddNotify(dev)
	if err := dev.stack.CreateNIC(1, dev.ep); err != nil {
		return nil, nil, fmt.Errorf("CreateNIC: %v", err)
	}
	hasV4 := false
	for _, ip := range localAddresses {
		var protoNumber tcpip.NetworkProtocolNumber
		if ip.Is4() {
			protoNumber = ipv4.ProtocolNumber
			hasV4 = true
		} else if ip.Is6() {
			protoNumber = ipv6.ProtocolNumber
		}
		protoAddr := tcpip.ProtocolAddress{
			Protocol:          protoNumber,
			AddressWithPrefix: tcpip.AddrFromSlice(ip.AsSlice()).WithPrefix(),
		}
		if err := dev.stack.AddProtocolAddress(1, protoAddr, stack.AddressProperties{}); err != nil {
			return nil, nil, fmt.Errorf("AddProtocolAddress(%v): %v", ip, err)
		}
	}
	if hasV4 {
		dev.stack.AddRoute(tcpip.Route{Destination: header.IPv4EmptySubnet, NIC: 1})
	}

	dev.events <- tun.EventUp
	return dev, (*Net)(dev), nil
}

func (tun *netTun) Name() (string, error) { return "go", nil }
func (tun *netTun) File() *os.File        { return nil }
func (tun *netTun) Events() <-chan tun.Event {
	return tun.events
}

func (tun *netTun) Read(buf [][]byte, sizes []int, offset int) (int, error) {
	view, ok := <-tun.incomingPacket
	if !ok {
		return 0, os.ErrClosed
	}
	n, err := view.Read(buf[0][offset:])
	if err != nil {
		return 0, err
	}
	sizes[0] = n
	return 1, nil
}

func (tun *netTun) Write(buf [][]byte, offset int) (int, error) {
	for _, buf := range buf {
		packet := buf[offset:]
		if len(packet) == 0 {
			continue
		}
		pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(packet)})
		switch packet[0] >> 4 {
		case 4:
			tun.ep.InjectInbound(header.IPv4ProtocolNumber, pkb)
		case 6:
			tun.ep.InjectInbound(header.IPv6ProtocolNumber, pkb)
		default:
			return 0, syscall.EAFNOSUPPORT
		}
	}
	return len(buf), nil
}

func (tun *netTun) WriteNotify() {
	pkt := tun.ep.Read()
	if pkt == nil {
		return
	}
	view := pkt.ToView()
	pkt.DecRef()
	tun.incomingPacket <- view
}

func (tun *netTun) Close() error {
	tun.stack.RemoveNIC(1)
	tun.stack.Close()
	tun.ep.RemoveNotify(tun.notifyHandle)
	tun.ep.Close()
	if tun.events != nil {
		close(tun.events)
	}
	if tun.incomingPacket != nil {
		close(tun.incomingPacket)
	}
	return nil
}

func (tun *netTun) MTU() (int, error) { return tun.mtu, nil }
func (tun *netTun) BatchSize() int    { return 1 }

func convertToFullAddr(endpoint netip.AddrPort) (tcpip.FullAddress, tcpip.NetworkProtocolNumber) {
	var protoNumber tcpip.NetworkProtocolNumber
	if endpoint.Addr().Is4() {
		protoNumber = ipv4.ProtocolNumber
	} else {
		protoNumber = ipv6.ProtocolNumber
	}
	return tcpip.FullAddress{
		NIC:  1,
		Addr: tcpip.AddrFromSlice(endpoint.Addr().AsSlice()),
		Port: endpoint.Port(),
	}, protoNumber
}

// DialContextTCPAddrPort dials a TCP peer through the tunnel.
func (n *Net) DialContextTCPAddrPort(ctx context.Context, addr netip.AddrPort) (*gonet.TCPConn, error) {
	fa, pn := convertToFullAddr(addr)
	return gonet.DialContextTCP(ctx, n.stack, fa, pn)
}

// ListenTCP listens on the tunnel address.
func (n *Net) ListenTCP(addr *net.TCPAddr) (*gonet.TCPListener, error) {
	if addr == nil {
		addr = &net.TCPAddr{}
	}
	ip, _ := netip.AddrFromSlice(addr.IP)
	fa, pn := convertToFullAddr(netip.AddrPortFrom(ip, uint16(addr.Port)))
	return gonet.ListenTCP(n.stack, fa, pn)
}

// ListenUDP listens for UDP datagrams on the tunnel address (used by
// the hub DNS server).
func (n *Net) ListenUDP(addr *net.UDPAddr) (*gonet.UDPConn, error) {
	if addr == nil {
		addr = &net.UDPAddr{}
	}
	ip, _ := netip.AddrFromSlice(addr.IP)
	fa, pn := convertToFullAddr(netip.AddrPortFrom(ip, uint16(addr.Port)))
	return gonet.DialUDP(n.stack, &fa, nil, pn)
}
