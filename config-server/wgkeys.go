package main

// WireGuard control-channel key management: all key material and
// tunnel addresses are HKDF-derived from a single master secret
// (WG_MASTER_KEY) via the wgderive package. Peers are computed from
// talos/machines/ at startup; per-machine tunnel config is injected
// into served configs at serve time only — key material never touches
// the repo.

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// wgSettings is the WireGuard state needed at serve time to inject
// per-machine tunnel config.
type wgSettings struct {
	master    []byte
	serverPub [32]byte
	serverIP  netip.Addr
	subnet    netip.Prefix
	endpoint  string         // public ip:port machines dial
	tnet      *netstack.Net  // dials machines through the tunnel (nil in tests)
	dev       *device.Device // live WG device, for peer stats (nil in tests)
}

// machineTunnelIP returns the machine's tunnel address: the explicit
// meta.yaml wgIP override if set, else HKDF-derived from the MAC.
func (w *wgSettings) machineTunnelIP(mac string, m machine) (netip.Addr, error) {
	if m.WGIP != "" {
		ip, err := netip.ParseAddr(m.WGIP)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("machine %s: invalid wgIP %q: %w", mac, m.WGIP, err)
		}
		return ip, nil
	}
	return wgderive.TunnelIP(w.master, mac, w.subnet)
}

// derivePeers computes the WG peer set for all known machines, failing
// on tunnel-address collisions (resolve by setting an explicit wgIP in
// one colliding machine's meta.yaml).
func (w *wgSettings) derivePeers(machines map[string]machine) ([]wgPeer, error) {
	claimed := map[netip.Addr]string{w.serverIP: "the server"}
	var peers []wgPeer
	for _, mac := range slices.Sorted(maps.Keys(machines)) {
		ip, err := w.machineTunnelIP(mac, machines[mac])
		if err != nil {
			return nil, err
		}
		if prev, dup := claimed[ip]; dup {
			return nil, fmt.Errorf("tunnel address collision: %s and %s both get %s (set wgIP in meta.yaml to resolve)", prev, mac, ip)
		}
		claimed[ip] = mac
		pub := wgderive.PublicKey(wgderive.MachineKey(w.master, mac))
		peers = append(peers, wgPeer{
			publicKeyHex: wgderive.KeyHex(pub),
			allowedIP:    netip.PrefixFrom(ip, 32),
		})
	}
	return peers, nil
}

// machinePatch renders the strategic-merge patch giving the machine
// its tunnel interface. persistentKeepaliveInterval keeps the NAT
// mapping open so the server can reach the machine unprompted. The
// tunnel address is also added to certSANs: apid's node-address
// discovery does not pick up wg0, and without the SAN the server's
// TLS dial for auto-bootstrap is rejected.
func (w *wgSettings) machinePatch(mac string, m machine) (string, error) {
	ip, err := w.machineTunnelIP(mac, m)
	if err != nil {
		return "", err
	}
	priv := wgderive.MachineKey(w.master, mac)
	return fmt.Sprintf(`machine:
  certSANs:
    - %s
  network:
    interfaces:
      - interface: wg0
        addresses:
          - %s/%d
        wireguard:
          privateKey: %s
          peers:
            - publicKey: %s
              endpoint: %s
              persistentKeepaliveInterval: 25s
              allowedIPs:
                - %s
`, ip, ip, w.subnet.Bits(), wgderive.KeyBase64(priv), wgderive.KeyBase64(w.serverPub), w.endpoint, w.subnet.Masked()), nil
}
