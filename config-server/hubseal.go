package main

// Sealed-hub unseal: the server starts with no key material anywhere
// (no fly secret, no disk). An admin unseals it at runtime by signing
// masterderive.MasterMessage with an allowlisted wallet (EIP-191
// personal_sign, deterministic per RFC 6979); the master key is
// HKDF-derived from the signature and lives only in memory. A server
// restart re-seals — see the repo decision log.
//
// The master is the root of every derivation the hub serves: the mesh
// CA and all leaf identities (nebderive), the per-node KMS seal keys
// and recovery passphrases, and the age identity that decrypts the
// repo's secrets. The unseal used to live on the wg0 manager; phase 2
// deleted wg0 and lifted it here, the hub-level concern it always was.

import (
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// hubManager owns the hub's seal state: the master key and everything
// that unlocks with it. Created when the mesh is enabled (--mesh-port)
// and starts sealed unless a dev master is supplied via WG_MASTER_KEY.
type hubManager struct {
	root       string   // talos/ directory
	adminAddrs []string // wallets allowed to unseal

	// pinnedCAFP is the expected mesh CA fingerprint (hex, "" =
	// unpinned). The CA derives from the master alone, so a wrong
	// wallet — or the right wallet signing a subtly different message —
	// derives a different CA, and the unseal fails loudly instead of
	// bringing up a mesh no enrolled member trusts.
	pinnedCAFP string

	// mesh is the overlay the control channel rides. Never nil in
	// production (main refuses the combination); nil only in tests
	// that exercise seal state alone.
	mesh *nebManager

	mu     sync.Mutex
	master []byte // nil while sealed
}

func newHubManager(root string, adminAddrs []string, pinnedCAFP string, mesh *nebManager) *hubManager {
	return &hubManager{root: root, adminAddrs: adminAddrs, pinnedCAFP: pinnedCAFP, mesh: mesh}
}

// current returns the unsealed master key, or nil while sealed.
func (m *hubManager) current() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.master
}

func (m *hubManager) sealed() bool { return m.current() == nil }

// unsealWithSignature verifies an EIP-191 signature over MasterMessage
// against the admin allowlist, then unseals with the derived master.
func (m *hubManager) unsealWithSignature(sigHex string) error {
	if len(m.adminAddrs) == 0 {
		return fmt.Errorf("no admin addresses configured; cannot verify unseal signature")
	}
	addr, err := recoverPersonalSign(masterderive.MasterMessage, sigHex)
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
	master, err := masterderive.MasterFromSignatureHex(sigHex)
	if err != nil {
		return err
	}
	if err := m.unsealWithMaster(master); err != nil {
		return err
	}
	log.Printf("wallet %s unsealed the hub", addr)
	return nil
}

// unsealWithMaster checks the derived CA against the pin, decrypts the
// repo secrets, holds the master, and fans out to the mesh. Idempotent
// once unsealed.
//
// The unseal succeeds once the master is held and the secrets decrypt,
// even if the mesh then fails to start: KMS disk unlocks ride the WAN
// listener and must not depend on the overlay (invariant 4), so a mesh
// startup failure surfaces on /sealed and /status — loudly, as a 503 —
// instead of holding the master hostage.
func (m *hubManager) unsealWithMaster(master []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.master != nil {
		return nil // already unsealed; nebula cannot be restarted in-process
	}

	fp, err := nebderive.CAFingerprint(master)
	if err != nil {
		return fmt.Errorf("fingerprinting mesh CA: %w", err)
	}
	if m.pinnedCAFP != "" && fp != m.pinnedCAFP {
		return fmt.Errorf("derived mesh CA fingerprint %s does not match pinned %s (wrong wallet or message?)", fp, m.pinnedCAFP)
	}

	// Secrets decrypt before anything unblocks: config serving and the
	// KMS gate on master != nil, and an unseal that cannot produce the
	// secrets must fail loudly rather than serve broken configs.
	if err := decryptAgeSecrets(m.root, master); err != nil {
		return fmt.Errorf("decrypting secrets: %w", err)
	}

	m.master = master
	log.Printf("hub unsealed: mesh CA %s", fp)

	if m.mesh != nil {
		if err := m.mesh.unsealWithMaster(master); err != nil {
			log.Printf("MESH DOWN: %v", err)
		}
	}
	return nil
}

// handleUnseal accepts the admin's signature over MasterMessage.
func (s *server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.Error(w, "hub sealing disabled", http.StatusNotFound)
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
	if err := s.hub.unsealWithSignature(sig); err != nil {
		log.Printf("unseal rejected: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.respondAction(w, r, "hub unsealed")
}

// handleSealed is a monitoring endpoint: 200 when healthy, 503 when the
// hub is sealed OR the mesh failed to start — point an external pinger
// at it.
//
// Mesh state changing the status code is the phase-2 inversion: the
// mesh is the control channel now, so a mesh that failed to start is
// something to page for, not something to read about later. The check
// is on the recorded startup error rather than on liveness: a stubbed
// start in tests leaves no service and no error, and production's
// startMeshNebula never does.
func (s *server) handleSealed(w http.ResponseWriter, _ *http.Request) {
	sealed := s.hub != nil && s.hub.sealed()
	mesh := s.mesh()
	var meshErr error
	if mesh != nil {
		_, _, meshErr = mesh.state()
	}

	if sealed || meshErr != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	switch {
	case s.hub == nil:
		fmt.Fprintln(w, "hub: disabled")
	case sealed:
		fmt.Fprintln(w, "hub: SEALED")
	default:
		fmt.Fprintln(w, "hub: unsealed")
	}

	switch {
	case mesh == nil:
		fmt.Fprintln(w, "mesh: disabled")
	case mesh.up():
		fmt.Fprintln(w, "mesh: up")
	case meshErr != nil:
		fmt.Fprintf(w, "mesh: DOWN (%v)\n", meshErr)
	case sealed:
		fmt.Fprintln(w, "mesh: sealed")
	default:
		fmt.Fprintln(w, "mesh: down")
	}
}

// mesh returns the mesh manager, or nil when the mesh is disabled.
func (s *server) mesh() *nebManager {
	if s.hub == nil {
		return nil
	}
	return s.hub.mesh
}
