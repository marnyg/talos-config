// Package nebstack embeds nebula as a library and exposes its overlay
// network through a gVisor netstack. It is a trim of nebula's own
// service package (MIT, Copyright (c) 2018-2019 Slack Technologies,
// Inc.) with one addition: ListenUDP, so the hub can serve DNS on the
// overlay.
//
// Why vendored rather than imported. Upstream's stack is fully capable
// of UDP — service.New registers udp.NewProtocol and adds the hub's
// overlay address to the NIC, and DialContext already handles udp — but
// Listen rejects anything other than TCP and *stack.Stack is an
// unexported field with no accessor, so an importer cannot reach the
// stack to bind a UDP endpoint itself. Same story as wgstack: the
// capability is there, the handle is not.
//
// This matters because nebula's built-in lighthouse DNS (serve_dns)
// cannot work here. It binds a kernel socket and therefore needs a TUN
// device, which the hub does not have — it runs the TUN-less
// overlay.UserDevice and pipes packets into userspace. Verified during
// the 2026-07-29 spike: overlay UDP/53 never reaches the process.
//
// The trim keeps upstream's shapes verbatim so the file stays diffable
// against the next nebula release; ours are the marked additions
// (ListenUDP) and the deletions. Only the surface the hub uses
// survives: New (lighthouse + relay + netstack), DialContext (hub →
// peer, e.g. apid over the overlay), Listen (TCP), ListenUDP (tunnel
// DNS), Wait and Close.
package nebstack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/overlay"
	"golang.org/x/sync/errgroup"
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
	"gvisor.dev/gvisor/pkg/waiter"
)

const nicID = 1

// Service is a running nebula instance plus the netstack that carries
// its overlay traffic into this process.
type Service struct {
	eg      *errgroup.Group
	control *nebula.Control
	ipstack *stack.Stack

	// networks is the overlay prefix set the device claims; the first
	// address is this host's own overlay address, which ListenUDP binds
	// by default.
	networks []netip.Prefix

	mu struct {
		sync.Mutex

		listeners map[uint16]*tcpListener
	}
}

// New starts nebula and wires its userspace device to a netstack. The
// control must come from nebula.Main with an overlay.UserDevice factory
// (no TUN), which is what makes this work on fly.io.
func New(control *nebula.Control) (_ *Service, reterr error) {
	// Check this before Start so a failure doesn't leave a running nebula
	device, ok := control.Device().(*overlay.UserDevice)
	if !ok {
		return nil, errors.New("must be using user device")
	}

	err := control.Start()
	if err != nil {
		return nil, err
	}

	// Anything that fails after a successful Start must tear nebula back down
	defer func() {
		if reterr != nil {
			control.Stop()
		}
	}()

	ctx := control.Context()
	eg, ctx := errgroup.WithContext(ctx)
	s := Service{
		eg:      eg,
		control: control,
	}
	s.mu.listeners = map[uint16]*tcpListener{}

	s.ipstack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4, icmp.NewProtocol6},
	})
	sackEnabledOpt := tcpip.TCPSACKEnabled(true) // TCP SACK is disabled by default
	tcpipErr := s.ipstack.SetTransportProtocolOption(tcp.ProtocolNumber, &sackEnabledOpt)
	if tcpipErr != nil {
		return nil, fmt.Errorf("could not enable TCP SACK: %v", tcpipErr)
	}
	linkEP := channel.New( /*size*/ 512 /*mtu*/, 1280, "")
	if tcpipProblem := s.ipstack.CreateNIC(nicID, linkEP); tcpipProblem != nil {
		return nil, fmt.Errorf("could not create netstack NIC: %v", tcpipProblem)
	}
	ipv4Subnet, _ := tcpip.NewSubnet(tcpip.AddrFrom4([4]byte{0x00, 0x00, 0x00, 0x00}), tcpip.MaskFrom(strings.Repeat("\x00", 4)))
	s.ipstack.SetRouteTable([]tcpip.Route{
		{
			Destination: ipv4Subnet,
			NIC:         nicID,
		},
	})

	ipNet := device.Networks()
	if len(ipNet) == 0 {
		return nil, errors.New("nebula device has no overlay networks")
	}
	s.networks = ipNet
	pa := tcpip.ProtocolAddress{
		AddressWithPrefix: tcpip.AddrFromSlice(ipNet[0].Addr().AsSlice()).WithPrefix(),
		Protocol:          ipv4.ProtocolNumber,
	}
	if err := s.ipstack.AddProtocolAddress(nicID, pa, stack.AddressProperties{
		PEB:        stack.CanBePrimaryEndpoint, // zero value default
		ConfigType: stack.AddressConfigStatic,  // zero value default
	}); err != nil {
		return nil, fmt.Errorf("error creating IP: %s", err)
	}

	const tcpReceiveBufferSize = 0
	const maxInFlightConnectionAttempts = 1024
	tcpFwd := tcp.NewForwarder(s.ipstack, tcpReceiveBufferSize, maxInFlightConnectionAttempts, s.tcpHandler)
	s.ipstack.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// No UDP forwarder on purpose. A forwarder catches datagrams the
	// stack has no endpoint for; ListenUDP binds a real endpoint on the
	// hub's own overlay address, so normal demultiplexing delivers to
	// it. Adding a forwarder would only let us answer on addresses we
	// do not own, which for DNS would be a lie.

	reader, writer := device.Pipe()

	go func() {
		<-ctx.Done()
		reader.Close()
		writer.Close()
	}()

	// create Goroutines to forward packets between Nebula and Gvisor
	eg.Go(func() error {
		buf := make([]byte, header.IPv4MaximumHeaderSize+header.IPv4MaximumPayloadSize)
		for {
			// this will read exactly one packet
			n, err := reader.Read(buf)
			if err != nil {
				return err
			}
			packetBuf := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithData(bytes.Clone(buf[:n])),
			})
			linkEP.InjectInbound(header.IPv4ProtocolNumber, packetBuf)

			if err := ctx.Err(); err != nil {
				return err
			}
		}
	})
	eg.Go(func() error {
		for {
			packet := linkEP.ReadContext(ctx)
			if packet == nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				continue
			}
			bufView := packet.ToView()
			if _, err := bufView.WriteTo(writer); err != nil {
				return err
			}
			bufView.Release()
		}
	})

	// Add the nebula wait function to the group so a fatal reader error
	// propagates out through errgroup.Wait().
	eg.Go(func() error {
		return control.Wait()
	})

	return &s, nil
}

// OverlayAddr is this host's own overlay address — the first address of
// the device's first network. ListenUDP binds it, and the tunnel DNS
// resolver address advertised to peers must match it.
func (s *Service) OverlayAddr() netip.Addr {
	return s.networks[0].Addr()
}

// Peers reports the live hostmap: one entry per established tunnel.
// Read-only introspection — the hub's /status joins this with the
// derived membership to show who is actually connected, and how
// (direct remote vs relayed, and who relays through us).
func (s *Service) Peers() []nebula.ControlHostInfo {
	return s.control.ListHostmapHosts(false)
}

func getProtocolNumber(addr netip.Addr) tcpip.NetworkProtocolNumber {
	if addr.Is6() {
		return ipv6.ProtocolNumber
	}
	return ipv4.ProtocolNumber
}

// DialContext dials the provided address.
func (s *Service) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "udp", "udp4", "udp6":
		addr, err := net.ResolveUDPAddr(network, address)
		if err != nil {
			return nil, err
		}
		fullAddr := tcpip.FullAddress{
			NIC:  nicID,
			Addr: tcpip.AddrFromSlice(addr.IP),
			Port: uint16(addr.Port),
		}
		num := getProtocolNumber(addr.AddrPort().Addr())
		return gonet.DialUDP(s.ipstack, nil, &fullAddr, num)
	case "tcp", "tcp4", "tcp6":
		addr, err := net.ResolveTCPAddr(network, address)
		if err != nil {
			return nil, err
		}
		fullAddr := tcpip.FullAddress{
			NIC:  nicID,
			Addr: tcpip.AddrFromSlice(addr.IP),
			Port: uint16(addr.Port),
		}
		num := getProtocolNumber(addr.AddrPort().Addr())
		return gonet.DialContextTCP(ctx, s.ipstack, fullAddr, num)
	default:
		return nil, fmt.Errorf("unknown network type: %s", network)
	}
}

// Dial dials the provided address
func (s *Service) Dial(network, address string) (net.Conn, error) {
	return s.DialContext(context.Background(), network, address)
}

// Listen listens on the provided address. Currently only TCP with wildcard
// addresses are supported.
func (s *Service) Listen(network, address string) (net.Listener, error) {
	if network != "tcp" && network != "tcp4" {
		return nil, errors.New("only tcp is supported")
	}
	addr, err := net.ResolveTCPAddr(network, address)
	if err != nil {
		return nil, err
	}
	if addr.IP != nil && !bytes.Equal(addr.IP, []byte{0, 0, 0, 0}) {
		return nil, fmt.Errorf("only wildcard address supported, got %q %v", address, addr.IP)
	}
	if addr.Port == 0 {
		return nil, errors.New("specific port required, got 0")
	}
	if addr.Port < 0 || addr.Port >= math.MaxUint16 {
		return nil, fmt.Errorf("invalid port %d", addr.Port)
	}
	port := uint16(addr.Port)

	l := &tcpListener{
		port:   port,
		s:      s,
		addr:   addr,
		accept: make(chan net.Conn),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.mu.listeners[port]; ok {
		return nil, fmt.Errorf("already listening on port %d", port)
	}
	s.mu.listeners[port] = l

	return l, nil
}

// ListenUDP binds a UDP socket on the overlay — the addition over
// upstream's service package, and the reason this trim exists.
//
// A nil addr, or one with a nil IP, binds this host's own overlay
// address (OverlayAddr) rather than the wildcard: the hub must answer
// DNS from the address peers sent the query to, and the netstack only
// owns that one address anyway.
//
// gonet has no ListenUDP; DialUDP with a local address and a nil remote
// is how you get a bound, unconnected UDP conn — same construction as
// wgstack.ListenUDP.
func (s *Service) ListenUDP(addr *net.UDPAddr) (*gonet.UDPConn, error) {
	if addr == nil {
		addr = &net.UDPAddr{}
	}
	ip := s.OverlayAddr()
	if addr.IP != nil {
		a, ok := netip.AddrFromSlice(addr.IP)
		if !ok {
			return nil, fmt.Errorf("invalid listen address %v", addr.IP)
		}
		ip = a.Unmap()
	}
	if addr.Port <= 0 || addr.Port >= math.MaxUint16 {
		return nil, fmt.Errorf("invalid port %d", addr.Port)
	}
	fullAddr := tcpip.FullAddress{
		NIC:  nicID,
		Addr: tcpip.AddrFromSlice(ip.AsSlice()),
		Port: uint16(addr.Port),
	}
	return gonet.DialUDP(s.ipstack, &fullAddr, nil, getProtocolNumber(ip))
}

func (s *Service) Wait() error {
	return s.eg.Wait()
}

func (s *Service) Close() error {
	s.control.Stop()
	return nil
}

func (s *Service) tcpHandler(r *tcp.ForwarderRequest) {
	endpointID := r.ID()

	s.mu.Lock()
	defer s.mu.Unlock()

	l, ok := s.mu.listeners[endpointID.LocalPort]
	if !ok {
		r.Complete(true)
		return
	}

	var wq waiter.Queue
	ep, err := r.CreateEndpoint(&wq)
	if err != nil {
		log.Printf("got error creating endpoint %q", err)
		r.Complete(true)
		return
	}
	r.Complete(false)
	ep.SocketOptions().SetKeepAlive(true)

	conn := gonet.NewTCPConn(&wq, ep)
	l.accept <- conn
}

// tcpListener is upstream's listener, unchanged.
type tcpListener struct {
	port   uint16
	s      *Service
	addr   *net.TCPAddr
	accept chan net.Conn
}

func (l *tcpListener) Accept() (net.Conn, error) {
	conn, ok := <-l.accept
	if !ok {
		return nil, io.EOF
	}
	return conn, nil
}

func (l *tcpListener) Close() error {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	delete(l.s.mu.listeners, uint16(l.addr.Port))

	close(l.accept)

	return nil
}

// Addr returns the listener's network address.
func (l *tcpListener) Addr() net.Addr {
	return l.addr
}
