package main

// Boot enrollment for machines (ADR-0015; talos-config-359.8.3.2).
//
// A served machine config carries a boottoken instead of a key
// (agentPatch, injected by serveTimePatches). The node's agent mints
// its NodeId at first boot and POSTs {node, token} here over HTTPS —
// provisioning-plane, invariant 4 — and walks away with a member cert
// to that NodeId carrying the MAC's git-declared name, in group
// `machines`. Name and groups come from git; the key comes from the
// node; the hub mints certs, never keys.
//
// Decision talos-config-488: token verification lives here, in the
// HTTP handler, with the master the hubManager holds — not in an
// Enroll/Provisioner actor facet — until Provisioner-as-actor lands
// (ADR-0024 outstanding). The Issuer mints exactly as it does for
// Enroll's wallet-approved devices; only the approval differs (a
// MAC-bound token the master signed vs an EIP-191 signature).

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/boottoken"
	"github.com/marnyg/talos-config/config-server/fakeip"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/mesh"
	"github.com/marnyg/talos-config/config-server/nodeagent"
)

// agentPatch renders the machine's identity-plane patch for mac: a
// machine.certSANs merge adding <name>.<zone>, then the p0agent
// ExtensionServiceConfig with a fresh boot token. "" when the hub has
// no iroh identity plane (no --iroh-relay): a node then runs nebula
// alone, as before Phase 1.
//
// The SAN is the name every identity-plane caller verifies apid's TLS
// against: talosconfig over the irohup tun and the hub's own apid dials
// (bootstrap.go talosClient) both dial <name>.mesh.internal. apid's own
// cert SANs cover every node address but never a mesh name, so the
// serve injects it. (Moved here from the nebula render, Phase 4 P4.2 —
// the name outlives the overlay.) Two documents: configpatcher merges
// the first into machine: (certSANs is append-merged, never replaced)
// and appends the second as its own document.
func (m *hubManager) agentPatch(master []byte, mac string, mach machines.Machine, now time.Time) (string, error) {
	if m.publicURL == "" {
		return "", nil
	}
	token, err := boottoken.Mint(master, mac, now)
	if err != nil {
		return "", err
	}
	doc, err := nodeagent.Patch(nodeagent.Config{Hub: m.publicURL, Relay: m.publicURL, Token: token})
	if err != nil {
		return "", err
	}
	sans := "machine:\n  certSANs:\n    - " + machineSAN(mac, mach) + "\n"
	return sans + "---\n" + doc, nil
}

// machineSAN is the machine's identity-plane name, <name>.<zone>: the
// git-declared mesh label under the presentation zone.
func machineSAN(mac string, m machines.Machine) string {
	return mesh.MachineDNSName(mac, m) + "." + strings.TrimSuffix(fakeip.Zone, ".")
}

// handleNodeEnroll (POST /mesh/enroll/node) redeems a boot token for a
// Kit. Statuses are the agent's retry signal: 503 (sealed) and 5xx
// mean try again later; 401/409 mean this token is dead and the node
// needs a fresh config serve.
func (s *server) handleNodeEnroll(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.NotFound(w, r)
		return
	}
	var req nodeagent.EnrollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := req.Node.Validate(); err != nil {
		http.Error(w, "bad node id", http.StatusBadRequest)
		return
	}
	master := s.hub.current()
	if master == nil {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}
	now := time.Now()
	mac, err := boottoken.Verify(master, req.Token, now)
	if err != nil {
		log.Printf("node enroll from %s refused: %v", r.RemoteAddr, err)
		http.Error(w, "token not accepted", http.StatusUnauthorized)
		return
	}
	// Git approval: the MAC must still be declared (deleting the
	// directory is how a machine is un-approved).
	byMAC, err := machines.Load(filepath.Join(s.root, "machines"))
	if err != nil {
		log.Printf("node enroll: loading machines: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	m, ok := byMAC[mac]
	if !ok {
		log.Printf("node enroll for %s refused: not declared", mac)
		http.Error(w, "token not accepted", http.StatusUnauthorized)
		return
	}
	// The master alone does not make the hub an issuer (ADR-0018): a
	// sealed or nagging Issuer is a retry, and must not burn the token.
	if err := s.hub.issuer.Serving(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := s.hub.bootSeen.Use(req.Token, now); err != nil {
		log.Printf("node enroll for %s refused: %v", mac, err)
		http.Error(w, "token already redeemed", http.StatusConflict)
		return
	}
	kit, err := s.hub.issuer.Mint(req.Node, mesh.MachineDNSName(mac, m), []string{mesh.GroupMachines})
	if err != nil {
		s.hub.bootSeen.Release(req.Token)
		log.Printf("node enroll for %s: mint: %v", mac, err)
		status := http.StatusInternalServerError
		if errors.Is(err, issuer.ErrSealed) || errors.Is(err, issuer.ErrNag) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, "cannot mint: "+err.Error(), status)
		return
	}
	body, err := issuer.EncodeKit(kit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
	log.Printf("node enroll: minted member %s for %s (%s)", mesh.MachineDNSName(mac, m), mac, req.Node)
}
