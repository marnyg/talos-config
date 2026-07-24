package main

// flyBind is a minimal wireguard-go conn.Bind over a single UDP socket
// bound to a specific address. Fly.io's UDP proxy only delivers packets
// to sockets bound to the special fly-global-services address (and
// replies must originate from it), so the default wildcard bind never
// sees any traffic there.

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
)

type flyBind struct {
	bindIP netip.Addr

	mu   sync.Mutex
	sock *net.UDPConn
}

// resolveFlyGlobalServices returns the bind address for fly's UDP
// routing, or the unspecified address when not running on fly. The
// lookup is bounded: off-fly the name doesn't exist and some resolvers
// take ~20s to say so, which would stall every local WG-enabled start.
func resolveFlyGlobalServices() netip.Addr {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", "fly-global-services")
	if err == nil {
		for _, ip := range ips {
			if a, ok := netip.AddrFromSlice(ip.To4()); ok && ip.To4() != nil {
				return a
			}
		}
	}
	return netip.IPv4Unspecified()
}

func newFlyBind(bindIP netip.Addr) *flyBind { return &flyBind{bindIP: bindIP} }

func (b *flyBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sock != nil {
		return nil, 0, conn.ErrBindAlreadyOpen
	}

	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: b.bindIP.AsSlice(), Port: int(port)})
	if err != nil {
		return nil, 0, fmt.Errorf("binding %s:%d: %w", b.bindIP, port, err)
	}
	b.sock = sock

	recv := func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		n, addr, err := sock.ReadFromUDPAddrPort(bufs[0])
		if err != nil {
			return 0, err
		}
		sizes[0] = n
		eps[0] = &conn.StdNetEndpoint{AddrPort: addr}
		return 1, nil
	}
	return []conn.ReceiveFunc{recv}, uint16(sock.LocalAddr().(*net.UDPAddr).Port), nil
}

func (b *flyBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sock == nil {
		return nil
	}
	err := b.sock.Close()
	b.sock = nil
	return err
}

func (b *flyBind) SetMark(uint32) error { return nil }

func (b *flyBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	b.mu.Lock()
	sock := b.sock
	b.mu.Unlock()
	if sock == nil {
		return net.ErrClosed
	}
	dst := ep.(*conn.StdNetEndpoint).AddrPort
	for _, buf := range bufs {
		if _, err := sock.WriteToUDPAddrPort(buf, dst); err != nil {
			return err
		}
	}
	return nil
}

func (b *flyBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return &conn.StdNetEndpoint{AddrPort: ap}, nil
}

func (b *flyBind) BatchSize() int { return 1 }
