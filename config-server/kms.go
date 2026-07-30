package main

// Network KMS for Talos disk encryption (siderolabs kms-client
// protocol). At install Talos generates a random disk key and calls
// Seal; the sealed blob lands in LUKS token metadata. Every boot it
// calls Unseal to open the disk. Sealing is stateless: the per-node
// AES-256-GCM key is HKDF-derived from the wallet master and the
// node's SMBIOS UUID, so a blob that authenticates proves it was
// sealed by this fleet's master for exactly that UUID.
//
// Policy:
//   - server sealed  → everything Unavailable (no master, no keys)
//   - Seal: open but logged — an attacker sealing junk gains nothing,
//     and a fresh install must be able to seal before its UUID is
//     recorded in the repo
//   - Unseal: UUID must be declared in some machines/<mac>/meta.yaml
//     (durable allowlist — REVOCATION IS DELETING THAT LINE), or have
//     been sealed by this server instance (grace so a fresh install
//     can reboot before the admin records the UUID; a *server* restart
//     closes the grace window)
//
// The KMS endpoint must be WAN-reachable: STATE-partition unlock runs
// before the machine config (and thus the mesh) exists — Talos reads
// the encryption config from META and dials with early kernel-arg/DHCP
// networking only (invariant 4: nothing on the boot path may depend on
// the overlay).

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"sync"

	kmsapi "github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/marnyg/talos-config/config-server/masterderive"
)

type kmsServer struct {
	kmsapi.UnimplementedKMSServiceServer

	root string
	hub  *hubManager

	mu            sync.Mutex
	sessionSealed map[string]bool // UUIDs sealed this server lifetime
}

func newKMSServer(root string, hub *hubManager) *kmsServer {
	return &kmsServer{
		root:          root,
		hub:           hub,
		sessionSealed: map[string]bool{},
	}
}

// master returns the unsealed master key, or a gRPC error while sealed.
func (k *kmsServer) master() ([]byte, error) {
	if master := k.hub.current(); master != nil {
		return master, nil
	}
	return nil, status.Error(codes.Unavailable, "hub is sealed; an admin must unseal at /status")
}

// declared reports whether any machine's meta.yaml declares uuid.
func (k *kmsServer) declared(uuid string) bool {
	machines, err := loadMachines(filepath.Join(k.root, "machines"))
	if err != nil {
		log.Printf("kms: loading machines: %v", err)
		return false
	}
	for _, m := range machines {
		if m.UUID != "" && masterderive.NormalizeUUID(m.UUID) == uuid {
			return true
		}
	}
	return false
}

// undeclaredSealed returns UUIDs that sealed this lifetime but are not
// (yet) declared in the repo — surfaced on /status so the admin
// records them before the grace window closes.
func (k *kmsServer) undeclaredSealed() []string {
	k.mu.Lock()
	uuids := make([]string, 0, len(k.sessionSealed))
	for u := range k.sessionSealed {
		uuids = append(uuids, u)
	}
	k.mu.Unlock()

	var out []string
	for _, u := range uuids {
		if !k.declared(u) {
			out = append(out, u)
		}
	}
	slices.Sort(out)
	return out
}

// Seal encrypts the node's disk key under its derived seal key.
func (k *kmsServer) Seal(_ context.Context, req *kmsapi.Request) (*kmsapi.Response, error) {
	master, err := k.master()
	if err != nil {
		return nil, err
	}
	uuid := masterderive.NormalizeUUID(req.GetNodeUuid())
	if uuid == "" {
		return nil, status.Error(codes.InvalidArgument, "missing node UUID")
	}

	blob, err := sealBlob(masterderive.KMSSealKey(master, uuid), req.GetData())
	if err != nil {
		return nil, status.Error(codes.Internal, "sealing failed")
	}

	k.mu.Lock()
	k.sessionSealed[uuid] = true
	k.mu.Unlock()

	if k.declared(uuid) {
		log.Printf("kms: sealed disk key for %s", uuid)
	} else {
		log.Printf("kms: sealed disk key for UNDECLARED uuid %s — add 'uuid: %s' to its machine's meta.yaml before the next server restart", uuid, uuid)
	}
	return &kmsapi.Response{Data: blob}, nil
}

// Unseal decrypts a previously sealed blob for an allowed UUID.
func (k *kmsServer) Unseal(_ context.Context, req *kmsapi.Request) (*kmsapi.Response, error) {
	master, err := k.master()
	if err != nil {
		return nil, err
	}
	uuid := masterderive.NormalizeUUID(req.GetNodeUuid())
	if uuid == "" {
		return nil, status.Error(codes.InvalidArgument, "missing node UUID")
	}

	k.mu.Lock()
	grace := k.sessionSealed[uuid]
	k.mu.Unlock()
	if !grace && !k.declared(uuid) {
		log.Printf("kms: REFUSED unseal for undeclared uuid %s", uuid)
		return nil, status.Error(codes.PermissionDenied, "unknown node")
	}

	data, err := unsealBlob(masterderive.KMSSealKey(master, uuid), req.GetData())
	if err != nil {
		// Wrong master, wrong UUID, or a forged/corrupt blob — GCM
		// authentication failed either way.
		log.Printf("kms: REFUSED unseal for %s: blob does not authenticate", uuid)
		return nil, status.Error(codes.PermissionDenied, "sealed data does not authenticate")
	}

	log.Printf("kms: unsealed disk key for %s", uuid)
	return &kmsapi.Response{Data: data}, nil
}

// diskEncryptionPatch renders the strategic-merge patch enabling
// system disk encryption: slot 0 unseals via the network KMS on every
// boot (per-boot auth, revocable server-side), slot 1 is the derived
// break-glass passphrase (recoverable offline from the master
// signature via `recover -recovery`; stored nowhere). Applies at
// install time only — partitions encrypt when they are created.
func diskEncryptionPatch(master []byte, mac, kmsEndpoint string) string {
	pass := masterderive.RecoveryPassphrase(master, mac)
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

// sealBlob AES-256-GCM encrypts plaintext: random nonce || ciphertext.
func sealBlob(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// unsealBlob reverses sealBlob, failing if the blob does not
// authenticate under key.
func unsealBlob(key, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, fmt.Errorf("blob too short")
	}
	return gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
}
