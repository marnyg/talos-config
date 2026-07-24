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
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/device"

	"github.com/marnyg/talos-config/config-server/wgderive"
	"github.com/marnyg/talos-config/config-server/wgstack"
)

type wgPeer struct {
	publicKeyHex string
	allowedIP    netip.Prefix
}

// startWireGuard brings up the userspace WG device and a hello
// listener on the tunnel address. The hello speaks minimal HTTP/1.0 so
// plain TCP reads, curl, wget, and dumb uptime probes all get a clean
// response. The returned netstack handle dials machines through the
// tunnel (auto-bootstrap); the device handle exposes live peer
// counters for /status. The netstack forwards between peers (wgstack),
// so admin peers reach machines through the hub.
func startWireGuard(privateKey [32]byte, port int, addr netip.Addr, peers []wgPeer) (*wgstack.Net, *device.Device, error) {
	tun, tnet, err := wgstack.CreateNetTUN([]netip.Addr{addr}, 1280)
	if err != nil {
		return nil, nil, fmt.Errorf("creating netstack TUN: %w", err)
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
		return nil, nil, fmt.Errorf("configuring WG device: %w", err)
	}
	if err := dev.Up(); err != nil {
		return nil, nil, fmt.Errorf("bringing WG device up: %w", err)
	}

	listener, err := tnet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		return nil, nil, fmt.Errorf("tunnel hello listener: %w", err)
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
	return tnet, dev, nil
}

// wgPeerStat is one peer's live counters from the device UAPI.
type wgPeerStat struct {
	lastHandshake time.Time
	rxBytes       uint64
	txBytes       uint64
	endpoint      string // observed WAN endpoint (ip:port)
}

// peerStats reads the device's live per-peer counters, keyed by peer
// public key (hex). Nil map when there is no live device (tests,
// sealed).
func (w *wgSettings) peerStats() (map[string]wgPeerStat, error) {
	if w.dev == nil {
		return nil, nil
	}
	raw, err := w.dev.IpcGet()
	if err != nil {
		return nil, fmt.Errorf("reading WG device state: %w", err)
	}
	return parsePeerStats(raw), nil
}

// parsePeerStats parses UAPI get=1 output (RFC-ish key=value lines,
// peer sections started by public_key).
func parsePeerStats(raw string) map[string]wgPeerStat {
	stats := map[string]wgPeerStat{}
	var cur string
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k == "public_key" {
			cur = v
			stats[cur] = wgPeerStat{}
			continue
		}
		if cur == "" {
			continue // device-level keys before the first peer section
		}
		st := stats[cur]
		switch k {
		case "endpoint":
			st.endpoint = v
		case "last_handshake_time_sec":
			if sec, err := strconv.ParseInt(v, 10, 64); err == nil && sec > 0 {
				st.lastHandshake = time.Unix(sec, 0)
			}
		case "rx_bytes":
			st.rxBytes, _ = strconv.ParseUint(v, 10, 64)
		case "tx_bytes":
			st.txBytes, _ = strconv.ParseUint(v, 10, 64)
		}
		stats[cur] = st
	}
	return stats
}
