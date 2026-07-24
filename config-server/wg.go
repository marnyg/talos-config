package main

// WireGuard control-channel spike: a userspace WireGuard endpoint
// (wireguard-go + gVisor netstack, no kernel/root needed) inside the
// config server. For the spike it serves a TCP hello on the tunnel
// address so a client can prove end-to-end connectivity through
// fly.io's UDP proxying.

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"

	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

type wgPeer struct {
	publicKeyHex string
	allowedIP    netip.Prefix
}

// parseWGPeers parses "pubkeyhex:10.99.0.2/32,pubkeyhex:10.99.0.3/32".
func parseWGPeers(s string) ([]wgPeer, error) {
	var peers []wgPeer
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key, ip, ok := strings.Cut(p, ":")
		if !ok {
			return nil, fmt.Errorf("peer %q: want <pubkey-hex>:<allowed-ip/cidr>", p)
		}
		if _, err := hex.DecodeString(key); err != nil || len(key) != 64 {
			return nil, fmt.Errorf("peer %q: public key must be 64 hex chars", p)
		}
		prefix, err := netip.ParsePrefix(ip)
		if err != nil {
			return nil, fmt.Errorf("peer %q: %w", p, err)
		}
		peers = append(peers, wgPeer{publicKeyHex: key, allowedIP: prefix})
	}
	return peers, nil
}

// startWireGuard brings up the userspace WG device and a spike hello
// listener on the tunnel address.
func startWireGuard(privateKeyHex string, port int, addr netip.Addr, peers []wgPeer) error {
	if _, err := hex.DecodeString(privateKeyHex); err != nil || len(privateKeyHex) != 64 {
		return fmt.Errorf("WG_PRIVATE_KEY must be 64 hex chars")
	}

	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr}, []netip.Addr{}, 1280)
	if err != nil {
		return fmt.Errorf("creating netstack TUN: %w", err)
	}

	bindIP := resolveFlyGlobalServices()
	dev := device.NewDevice(tun, newFlyBind(bindIP), device.NewLogger(device.LogLevelError, "wg: "))
	log.Printf("wireguard binding %s (fly-global-services resolved: %v)", bindIP, !bindIP.IsUnspecified())

	var uapi strings.Builder
	fmt.Fprintf(&uapi, "private_key=%s\nlisten_port=%d\nreplace_peers=true\n", privateKeyHex, port)
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
				fmt.Fprintf(c, "hello from the tunnel: %s\n", addr)
				log.Printf("wg hello served to %s", c.RemoteAddr())
			}(c)
		}
	}()

	log.Printf("wireguard listening on udp/%d, tunnel address %s, %d peer(s)", port, addr, len(peers))
	return nil
}
