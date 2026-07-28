package main

// Admin device enrollment: `wgup` (or curl) fetches a challenge, the
// admin signs it with an allowlisted wallet, and the server returns
// the device's ready-to-use wg-quick tunnel config. The signed message
// binds a single device name and a single-use nonce — unlike the
// master message, this signature grants exactly one device config and
// nothing else, so day-to-day connects never touch the fleet master.
// Device names must be pre-declared (WG_ADMIN_PEERS): the peer set is
// derived, not registered, so enrollment mints no server state.

import (
	"fmt"
	"log"
	"net/http"
	"slices"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// enrollMessage is the canonical text the admin signs to enroll a
// device. Distinct prefix from the approval, login, and master-key
// messages.
func enrollMessage(name, nonce string) string {
	return fmt.Sprintf("talos config-server wg device enrollment\ndevice: %s\nnonce: %s", name, nonce)
}

// enrollPeerKnown reports whether name is a declared admin peer.
func (s *server) enrollPeerKnown(name string) bool {
	return s.wgm != nil && slices.ContainsFunc(s.wgm.adminPeers, func(p string) bool {
		return wgderive.NormalizeAdmin(p) == name
	})
}

// handleEnrollChallenge (GET /wg/enroll?name=<device>) issues the
// challenge to sign.
func (s *server) handleEnrollChallenge(w http.ResponseWriter, r *http.Request) {
	if s.wgm == nil {
		http.Error(w, "wireguard disabled", http.StatusNotFound)
		return
	}
	name := wgderive.NormalizeAdmin(r.URL.Query().Get("name"))
	if name == "" || !s.enrollPeerKnown(name) {
		http.Error(w, "unknown device name (declare it in WG_ADMIN_PEERS)", http.StatusNotFound)
		return
	}
	if s.wgm.sealed() {
		http.Error(w, "sealed: an admin must unseal the control channel at /verify", http.StatusServiceUnavailable)
		return
	}
	nonce := s.sessions.issueNonce()
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    name,
		"nonce":   nonce,
		"message": enrollMessage(name, nonce),
	})
}

// handleEnroll (POST /wg/enroll: name, nonce, signature) verifies the
// signed challenge and returns the wg-quick config.
func (s *server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if s.wgm == nil {
		http.Error(w, "wireguard disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := wgderive.NormalizeAdmin(r.FormValue("name"))
	nonce := r.FormValue("nonce")
	if name == "" || !s.enrollPeerKnown(name) {
		http.Error(w, "unknown device name (declare it in WG_ADMIN_PEERS)", http.StatusNotFound)
		return
	}
	addr, err := recoverPersonalSign(enrollMessage(name, nonce), r.FormValue("signature"))
	if err != nil {
		log.Printf("enroll %q: signature verification failed: %v", name, err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !slices.Contains(s.adminAddrs, addr) {
		log.Printf("enroll %q: wallet %s not in allowlist", name, addr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.sessions.redeemNonce(nonce) {
		log.Printf("enroll %q: expired or replayed nonce", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	wg := s.wgm.current()
	if wg == nil {
		http.Error(w, "sealed: an admin must unseal the control channel at /verify", http.StatusServiceUnavailable)
		return
	}
	cfg, err := wg.adminWGQuick(name)
	if err != nil {
		log.Printf("enroll %q: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("wallet %s enrolled device %q", addr, name)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(cfg))
}
