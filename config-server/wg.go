package main

// WireGuard control channel: a userspace WireGuard endpoint
// (wireguard-go + gVisor netstack, no kernel/root needed) inside the
// config server. Serves a TCP hello on the tunnel address as a cheap
// liveness check through fly.io's UDP proxying.

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

type wgPeer struct {
	publicKeyHex string
	allowedIP    netip.Prefix
}

// startWireGuard brings up the userspace WG device and a hello
// listener on the tunnel address. The hello speaks minimal HTTP/1.0 so
// plain TCP reads, curl, wget, and dumb uptime probes all get a clean
// response.
func startWireGuard(privateKey [32]byte, port int, addr netip.Addr, peers []wgPeer) error {
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr}, []netip.Addr{}, 1280)
	if err != nil {
		return fmt.Errorf("creating netstack TUN: %w", err)
	}

	bindIP := resolveFlyGlobalServices()
	dev := device.NewDevice(tun, newFlyBind(bindIP), device.NewLogger(device.LogLevelError, "wg: "))
	log.Printf("wireguard binding %s (fly-global-services resolved: %v)", bindIP, !bindIP.IsUnspecified())

	var uapi strings.Builder
	fmt.Fprintf(&uapi, "private_key=%s\nlisten_port=%d\nreplace_peers=true\n", wgderive.KeyHex(privateKey), port)
	for _, p := range peers {
		fmt.Fprintf(&uapi, "public_key=%s\nallowed_ip=%s\n", p.publicKeyHex, p.allowedIP)
	}
	if err := dev.IpcSet(uapi.String()); err != nil {
		return fmt.Errorf("configuring WG device: %w", err)
	}
	if err := dev.Up(); err != nil {
		return fmt.Errorf("bringing WG device up: %w", err)
	}

	listener, err := tnet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		return fmt.Errorf("tunnel hello listener: %w", err)
	}
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				log.Printf("wg hello accept: %v", err)
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				body := fmt.Sprintf("hello from the tunnel: %s\n", addr)
				fmt.Fprintf(c, "HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
				// Drain any request bytes so closing doesn't RST the
				// response out from under HTTP clients.
				_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				_, _ = io.Copy(io.Discard, c)
				log.Printf("wg hello served to %s", c.RemoteAddr())
			}(c)
		}
	}()

	log.Printf("wireguard listening on udp/%d, tunnel address %s, %d peer(s)", port, addr, len(peers))
	return nil
}
