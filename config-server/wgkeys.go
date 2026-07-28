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

	"github.com/marnyg/talos-config/config-server/wgderive"
	"github.com/marnyg/talos-config/config-server/wgstack"
)

// wgSettings is the WireGuard state needed at serve time to inject
// per-machine tunnel config.
type wgSettings struct {
	master    []byte
	serverPub [32]byte
	serverIP  netip.Addr
	subnet    netip.Prefix
	endpoint  string         // public ip:port machines dial
	admins    []string       // named admin peers (laptops), keys derived like machines'
	dnsDomain string         // tunnel DNS domain ("" = tunnel DNS disabled)
	tnet      *wgstack.Net   // dials machines through the tunnel (nil in tests)
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

// derivePeers computes the WG peer set for all known machines plus the
// named admin peers, failing on tunnel-address collisions (resolve by
// setting an explicit wgIP in one colliding machine's meta.yaml, or
// renaming the admin peer).
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
	for _, name := range w.admins {
		name = wgderive.NormalizeAdmin(name)
		if name == "" {
			continue
		}
		ip, err := wgderive.AdminTunnelIP(w.master, name, w.subnet)
		if err != nil {
			return nil, err
		}
		if prev, dup := claimed[ip]; dup {
			return nil, fmt.Errorf("tunnel address collision: %s and admin %q both get %s (rename the admin peer)", prev, name, ip)
		}
		claimed[ip] = "admin " + name
		pub := wgderive.PublicKey(wgderive.AdminKey(w.master, name))
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
// tunnel address (and its tunnel DNS name, when tunnel DNS is on) is
// also added to certSANs: apid's node-address discovery does not pick
// up wg0, and without the SAN the server's TLS dial for auto-bootstrap
// is rejected.
func (w *wgSettings) machinePatch(mac string, m machine) (string, error) {
	ip, err := w.machineTunnelIP(mac, m)
	if err != nil {
		return "", err
	}
	sans := ip.String()
	if w.dnsDomain != "" {
		sans += "\n    - " + machineDNSName(mac, m) + "." + w.dnsDomain
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
`, sans, ip, w.subnet.Bits(), wgderive.KeyBase64(priv), wgderive.KeyBase64(w.serverPub), w.endpoint, w.subnet.Masked()), nil
}

// adminWGQuick renders the ready-to-use wg-quick config for a named
// admin peer — the payload of the /wg/enroll flow.
func (w *wgSettings) adminWGQuick(name string) (string, error) {
	name = wgderive.NormalizeAdmin(name)
	ip, err := wgderive.AdminTunnelIP(w.master, name, w.subnet)
	if err != nil {
		return "", err
	}
	dns := netip.Addr{}
	if w.dnsDomain != "" {
		dns = w.serverIP
	}
	return wgderive.WGQuick(wgderive.AdminKey(w.master, name), ip, w.subnet, w.serverPub, w.endpoint, dns, w.dnsDomain), nil
}

// diskEncryptionPatch renders the strategic-merge patch enabling
// system disk encryption: slot 0 unseals via the network KMS on every
// boot (per-boot auth, revocable server-side), slot 1 is the derived
// break-glass passphrase (recoverable offline from the master
// signature via `wgping -recovery`; stored nowhere). Applies at
// install time only — partitions encrypt when they are created.
func (w *wgSettings) diskEncryptionPatch(mac, kmsEndpoint string) string {
	pass := wgderive.RecoveryPassphrase(w.master, mac)
	return fmt.Sprintf(`machine:
  systemDiskEncryption:
    state:
      provider: luks2
      keys:
        - slot: 0
          kms:
            endpoint: %[1]s
        - slot: 1
          static:
            passphrase: %[2]s
    ephemeral:
      provider: luks2
      keys:
        - slot: 0
          kms:
            endpoint: %[1]s
        - slot: 1
          static:
            passphrase: %[2]s
`, kmsEndpoint, pass)
}
