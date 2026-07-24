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
	root       string       // talos/ directory
	adminAddrs []string     // wallets allowed to unseal
	adminPeers []string     // named admin WG peers (laptops)

	// start is startWireGuard, stubbed in tests.
	start func(privateKey [32]byte, port int, addr netip.Addr, peers []wgPeer) (*wgstack.Net, *device.Device, error)

	mu       sync.Mutex
	settings *wgSettings // nil while sealed
}

func newWGManager(port int, addr netip.Prefix, endpoint, pinnedPub, root string, adminAddrs, adminPeers []string) *wgManager {
	return &wgManager{
		port:       port,
		addr:       addr,
		endpoint:   endpoint,
		pinnedPub:  pinnedPub,
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
	tnet, dev, err := m.start(priv, m.port, wg.serverIP, peers)
	if err != nil {
		return fmt.Errorf("starting wireguard: %w", err)
	}
	wg.tnet, wg.dev = tnet, dev

	m.settings = wg
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
	s.renderVerify(w, "control channel unsealed")
}

// handleSealed is a monitoring endpoint: 200 when healthy (unsealed or
// WG disabled), 503 while sealed — point an external pinger at it.
func (s *server) handleSealed(w http.ResponseWriter, _ *http.Request) {
	switch {
	case s.wgm == nil:
		fmt.Fprintln(w, "wireguard: disabled")
	case s.wgm.sealed():
		http.Error(w, "wireguard: SEALED", http.StatusServiceUnavailable)
	default:
		fmt.Fprintln(w, "wireguard: unsealed")
	}
}
