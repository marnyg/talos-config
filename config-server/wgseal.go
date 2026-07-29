package main

// Sealed-server WireGuard unseal: the server starts with no WG key
// material anywhere (no fly secret, no disk). An admin unseals it at
// runtime by signing wgderive.MasterMessage with an allowlisted wallet
// (EIP-191 personal_sign, deterministic per RFC 6979); the master key
// is HKDF-derived from the signature and lives only in memory. A
// server restart re-seals — see the repo decision log.

import (
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"path/filepath"
	"sync"

	"golang.zx2c4.com/wireguard/device"

	"github.com/marnyg/talos-config/config-server/wgderive"
	"github.com/marnyg/talos-config/config-server/wgstack"
)

// wgManager owns the WireGuard lifecycle. It is created when WG is
// enabled (--wg-port) and starts sealed unless a dev master key is
// supplied via WG_MASTER_KEY.
type wgManager struct {
	port       int
	addr       netip.Prefix // server tunnel address with subnet
	endpoint   string       // public ip:port machines dial
	pinnedPub  string       // expected server pubkey (base64 or hex, "" = unpinned)
	dnsDomain  string       // tunnel DNS domain ("" = tunnel DNS disabled)
	root       string       // talos/ directory
	adminAddrs []string     // wallets allowed to unseal
	adminPeers []string     // named admin WG peers (laptops)

	// start is startWireGuard, stubbed in tests.
	start func(privateKey [32]byte, port int, addr netip.Addr, peers []wgPeer) (*wgstack.Net, *device.Device, error)

	// tunnelConfig serves GET /config on the tunnel listener to admin
	// peers (set by main; nil disables the route).
	tunnelConfig http.Handler

	// mesh is the phase-1 second overlay, unsealed from the same master
	// (nil = mesh disabled). It hangs off wgManager only because the
	// unseal currently lives here; the unseal is a *hub* concern, and
	// phase 2 lifts it out when wg0 is deleted.
	mesh *nebManager

	mu       sync.Mutex
	settings *wgSettings // nil while sealed
}

func newWGManager(port int, addr netip.Prefix, endpoint, pinnedPub, dnsDomain, root string, adminAddrs, adminPeers []string) *wgManager {
	return &wgManager{
		port:       port,
		addr:       addr,
		endpoint:   endpoint,
		pinnedPub:  pinnedPub,
		dnsDomain:  dnsDomain,
		root:       root,
		adminAddrs: adminAddrs,
		adminPeers: adminPeers,
		start:      startWireGuard,
	}
}

// current returns the active settings, or nil while sealed.
func (m *wgManager) current() *wgSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (m *wgManager) sealed() bool { return m.current() == nil }

// unsealWithSignature verifies an EIP-191 signature over MasterMessage
// against the admin allowlist, then unseals with the derived master.
func (m *wgManager) unsealWithSignature(sigHex string) error {
	if len(m.adminAddrs) == 0 {
		return fmt.Errorf("no admin addresses configured; cannot verify unseal signature")
	}
	addr, err := recoverPersonalSign(wgderive.MasterMessage, sigHex)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	allowed := false
	for _, a := range m.adminAddrs {
		if addr == a {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("wallet %s not in allowlist", addr)
	}
	master, err := wgderive.MasterFromSignatureHex(sigHex)
	if err != nil {
		return err
	}
	if err := m.unsealWithMaster(master); err != nil {
		return err
	}
	log.Printf("wallet %s unsealed the wireguard control channel", addr)
	return nil
}

// unsealWithMaster derives all key material, checks the pinned server
// pubkey, and brings up the WG device. Idempotent once unsealed.
func (m *wgManager) unsealWithMaster(master []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings != nil {
		return nil // already unsealed; WG device cannot be restarted in-process
	}

	priv := wgderive.ServerKey(master)
	pub := wgderive.PublicKey(priv)
	if m.pinnedPub != "" && wgderive.KeyBase64(pub) != m.pinnedPub && wgderive.KeyHex(pub) != m.pinnedPub {
		return fmt.Errorf("derived server pubkey %s does not match pinned %s (wrong wallet or message?)",
			wgderive.KeyBase64(pub), m.pinnedPub)
	}

	wg := &wgSettings{
		master:    master,
		serverPub: pub,
		serverIP:  m.addr.Addr(),
		subnet:    m.addr.Masked(),
		endpoint:  m.endpoint,
		admins:    m.adminPeers,
		dnsDomain: m.dnsDomain,
	}
	// Secrets decrypt before anything unblocks: config serving and the
	// KMS gate on settings != nil, and an unseal that cannot produce
	// the secrets must fail loudly rather than serve broken configs.
	if err := decryptAgeSecrets(m.root, master); err != nil {
		return fmt.Errorf("decrypting secrets: %w", err)
	}
	machines, err := loadMachines(filepath.Join(m.root, "machines"))
	if err != nil {
		return fmt.Errorf("loading machines: %w", err)
	}
	peers, err := wg.derivePeers(machines)
	if err != nil {
		return fmt.Errorf("deriving peers: %w", err)
	}
	// The zone is validated (labels, collisions) even when the WG start
	// is stubbed, so a bad name fails the unseal loudly.
	var zone map[string]netip.Addr
	if m.dnsDomain != "" {
		if zone, err = buildDNSZone(machines, wg); err != nil {
			return fmt.Errorf("building dns zone: %w", err)
		}
	}
	tnet, dev, err := m.start(priv, m.port, wg.serverIP, peers)
	if err != nil {
		return fmt.Errorf("starting wireguard: %w", err)
	}
	wg.tnet, wg.dev = tnet, dev
	if wg.tnet != nil && zone != nil {
		if err := wg.serveDNS(zone); err != nil {
			return fmt.Errorf("starting tunnel dns: %w", err)
		}
		log.Printf("tunnel dns: %d name(s) under .%s on %s:53", len(zone), m.dnsDomain, wg.serverIP)
	}
	if wg.tnet != nil {
		if err := m.serveTunnelHTTP(wg); err != nil {
			return fmt.Errorf("starting tunnel http: %w", err)
		}
	}

	m.settings = wg

	// Fan out to the mesh. Deliberately non-fatal: in phase 1 wg0 carries
	// production traffic (talosconfig, KMS, auto-bootstrap) while the mesh
	// is on trial, so a mesh that cannot start must not take the working
	// overlay down with it. The error is kept on nebManager rather than
	// only logged, so it stays queryable instead of scrolling away.
	if m.mesh != nil {
		if err := m.mesh.unsealWithMaster(master); err != nil {
			log.Printf("MESH DOWN (wg0 unaffected): %v", err)
		}
	}

	log.Printf("wireguard unsealed: server pubkey %s (hex %s), endpoint %s, %d peer(s)",
		wgderive.KeyBase64(pub), wgderive.KeyHex(pub), m.endpoint, len(peers))
	for _, name := range m.adminPeers {
		name = wgderive.NormalizeAdmin(name)
		if name == "" {
			continue
		}
		ip, _ := wgderive.AdminTunnelIP(master, name, wg.subnet)
		log.Printf("admin peer %q: tunnel ip %s, pubkey %s (config: wgping -admin %s -sig <unseal-sig> -wgquick)",
			name, ip, wgderive.KeyBase64(wgderive.PublicKey(wgderive.AdminKey(master, name))), name)
	}
	return nil
}

// handleUnseal accepts the admin's signature over MasterMessage.
func (s *server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	if s.wgm == nil {
		http.Error(w, "wireguard disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	sig := r.FormValue("signature")
	if sig == "" {
		http.Error(w, "missing signature", http.StatusBadRequest)
		return
	}
	if err := s.wgm.unsealWithSignature(sig); err != nil {
		log.Printf("unseal rejected: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.respondAction(w, r, "control channel unsealed")
}

// handleSealed is a monitoring endpoint: 200 when healthy (unsealed or
// WG disabled), 503 while sealed — point an external pinger at it.
//
// Mesh state is reported but never changes the status code. In phase 1
// the mesh is on trial while wg0 carries production traffic, so a mesh
// failure is something to read, not something to page for. Phase 2
// inverts that along with everything else.
func (s *server) handleSealed(w http.ResponseWriter, _ *http.Request) {
	sealed := s.wgm != nil && s.wgm.sealed()
	if sealed {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "wireguard: SEALED")
	} else if s.wgm == nil {
		fmt.Fprintln(w, "wireguard: disabled")
	} else {
		fmt.Fprintln(w, "wireguard: unsealed")
	}

	switch mesh := s.mesh(); {
	case mesh == nil:
		fmt.Fprintln(w, "mesh: disabled")
	case mesh.up():
		fmt.Fprintln(w, "mesh: up")
	default:
		_, _, err := mesh.state()
		if err != nil {
			fmt.Fprintf(w, "mesh: DOWN (%v)\n", err)
		} else if sealed {
			fmt.Fprintln(w, "mesh: sealed")
		} else {
			fmt.Fprintln(w, "mesh: down")
		}
	}
}

// mesh returns the mesh manager, or nil when the mesh is disabled.
func (s *server) mesh() *nebManager {
	if s.wgm == nil {
		return nil
	}
	return s.wgm.mesh
}
