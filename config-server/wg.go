package main

// WireGuard control channel: a userspace WireGuard endpoint
// (wireguard-go + gVisor netstack, no kernel/root needed) inside the
// config server. Serves a TCP hello on the tunnel address as a cheap
// liveness check through fly.io's UDP proxying.

import (
	"fmt"
	"log"
	"net"
	"net/http"
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

// startWireGuard brings up the userspace WG device. The returned
// netstack handle dials machines through the tunnel (auto-bootstrap)
// and hosts the tunnel HTTP listener (serveTunnelHTTP); the device
// handle exposes live peer counters for /status. The netstack
// forwards between peers (wgstack), so admin peers reach machines
// through the hub.
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

	log.Printf("wireguard listening on udp/%d, tunnel address %s, %d peer(s)", port, addr, len(peers))
	return tnet, dev, nil
}

// serveTunnelHTTP starts the tunnel-side HTTP server on the hub's
// tunnel address: "/" keeps the hello (liveness, wgping) for every
// peer, /config serves hub-composed machine configs to admin peers
// only. Inside the tunnel the source address is cryptokey-routed —
// wireguard drops packets whose source doesn't match the sending
// peer's allowed-ips — so "request from an admin tunnel IP" is
// authentication: that address maps to exactly one wallet-enrolled
// admin device key.
func (m *wgManager) serveTunnelHTTP(wg *wgSettings) error {
	adminIPs := map[netip.Addr]bool{}
	for _, name := range m.adminPeers {
		name = wgderive.NormalizeAdmin(name)
		if name == "" {
			continue
		}
		ip, err := wgderive.AdminTunnelIP(wg.master, name, wg.subnet)
		if err != nil {
			return fmt.Errorf("deriving admin tunnel ip for %q: %w", name, err)
		}
		adminIPs[ip] = true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "hello from the tunnel: %s\n", wg.serverIP)
	})
	if m.tunnelConfig != nil {
		mux.Handle("GET /config", requireAdminPeer(adminIPs, m.tunnelConfig))
	}

	listener, err := wg.tnet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		return fmt.Errorf("tunnel http listener: %w", err)
	}
	go func() {
		if err := http.Serve(listener, mux); err != nil {
			log.Printf("tunnel http server: %v", err)
		}
	}()
	return nil
}

// requireAdminPeer gates a tunnel route to admin peer source
// addresses. Machines are tunnel peers too, and served configs carry
// other machines' secrets — a machine must never read a config it
// didn't earn a device-flow token for.
func requireAdminPeer(admins map[netip.Addr]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || !admins[ap.Addr().Unmap()] {
			log.Printf("tunnel %s %s: refused non-admin peer %s", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, "forbidden: admin tunnel peers only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
