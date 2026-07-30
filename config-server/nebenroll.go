package main

// Mesh device enrollment: `nebup` (or curl) fetches a challenge, the
// owner signs it with an allowlisted wallet, and the hub returns a
// ready-to-run nebula config for that device. The wgenroll.go pattern,
// one overlay over: the signature binds a single device name and a
// single-use nonce, so a day-to-day connect never touches the fleet
// master.
//
// Two differences from wg0 enrollment, both consequences of nebula's
// trust model:
//
//   - The hub mints a *certificate*, so the returned config is the
//     device's whole membership. Nothing is registered here — re-running
//     enrollment after a wipe re-derives the same identity, and the hub
//     forgets it happened.
//   - The config is self-contained (CA, cert and key inline) rather than
//     pointing at files. One file transfers by scp, clipboard, or QR to a
//     phone, and the mobile app has nowhere to put side files anyway.

import (
	"fmt"
	"log"
	"net/http"
	"slices"

	"github.com/marnyg/talos-config/config-server/ethsig"
)

// meshEnrollMessage is the canonical text the owner signs to enroll a
// mesh device. Distinct prefix from the wg enrollment, approval, login
// and master-key messages: a signature for one of those must never be
// replayable as another.
func meshEnrollMessage(name, nonce string) string {
	return fmt.Sprintf("talos config-server mesh device enrollment\ndevice: %s\nnonce: %s", name, nonce)
}

// handleMeshEnrollChallenge (GET /mesh/enroll?name=<device>) issues the
// challenge to sign.
func (s *server) handleMeshEnrollChallenge(w http.ResponseWriter, r *http.Request) {
	nm := s.mesh()
	if nm == nil {
		http.Error(w, "mesh disabled", http.StatusNotFound)
		return
	}
	d, ok := nm.Device(r.URL.Query().Get("name"))
	if !ok {
		http.Error(w, "unknown device name (declare it in MESH_DEVICES or MESH_MEDIA_DEVICES)", http.StatusNotFound)
		return
	}
	if s.hub.sealed() {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}
	nonce := s.sessions.issueNonce()
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    d.Name,
		"group":   d.Group,
		"nonce":   nonce,
		"message": meshEnrollMessage(d.Name, nonce),
	})
}

// handleMeshEnroll (POST /mesh/enroll: name, nonce, signature) verifies
// the signed challenge and returns the device's nebula config.
func (s *server) handleMeshEnroll(w http.ResponseWriter, r *http.Request) {
	nm := s.mesh()
	if nm == nil {
		http.Error(w, "mesh disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	nonce := r.FormValue("nonce")
	d, ok := nm.Device(name)
	if !ok {
		http.Error(w, "unknown device name (declare it in MESH_DEVICES or MESH_MEDIA_DEVICES)", http.StatusNotFound)
		return
	}
	addr, err := ethsig.RecoverPersonalSign(meshEnrollMessage(d.Name, nonce), r.FormValue("signature"))
	if err != nil {
		log.Printf("mesh enroll %q: signature verification failed: %v", d.Name, err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !slices.Contains(s.adminAddrs, addr) {
		log.Printf("mesh enroll %q: wallet %s not in allowlist", d.Name, addr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.sessions.redeemNonce(nonce) {
		log.Printf("mesh enroll %q: expired or replayed nonce", d.Name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	master := s.hub.current()
	if master == nil {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}

	cfg, err := nm.DeviceConfig(master, d)
	if err != nil {
		log.Printf("mesh enroll %q: %v", d.Name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The wallet that authorized issuance is logged; the cert it
	// authorized is not secret either, but the key in the body is, so
	// this is the only record kept and it is deliberately thin.
	log.Printf("wallet %s enrolled mesh device %q (group %s)", addr, d.Name, d.Group)
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	_, _ = w.Write(cfg)
}
