package main

// HTTP handlers for the OAuth2 device flow (RFC 8628). Endpoint paths
// match the Talos defaults derived from the talos.config URL:
// /device/code and /token. Human approval lives on the /status
// dashboard; /verify remains as the POST target for approval actions
// and as a redirect for consoles that print the old URL.

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/ethsig"
)

const deviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// approvalMessage is the canonical text the admin signs. The nonce is
// generated per device authorization, making every message unique.
func approvalMessage(action, userCode, nonce string) string {
	return fmt.Sprintf(
		"talos config-server machine approval\naction: %s\nuser_code: %s\nnonce: %s",
		action, userCode, nonce,
	)
}

// constantTimeEqual compares two strings without leaking length timing
// beyond equality of lengths.
func constantTimeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// externalBase reconstructs the externally visible base URL for
// verification_uri, honoring a TLS-terminating proxy.
func externalBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

// handleDeviceCode implements the device authorization endpoint.
// Talos sends client_id plus any extra_variable values (uuid, mac, ...).
func (s *server) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request")
		return
	}

	clientID := r.FormValue("client_id")
	if s.clientID != "" && clientID != s.clientID {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}

	// Everything that isn't a standard OAuth parameter is machine identity.
	identity := make(map[string]string)
	for k, vs := range r.Form {
		switch k {
		case "client_id", "client_secret", "scope", "audience":
			continue
		}
		if len(vs) > 0 && vs[0] != "" {
			identity[k] = vs[0]
		}
	}

	da := s.store.Begin(deviceflow.KindMachine, clientID, identity)
	base := externalBase(r)

	log.Printf("device auth started: user_code=%s identity=%v", da.UserCode, identity)
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               da.DeviceCode,
		"user_code":                 da.UserCode,
		"verification_uri":          base + "/status",
		"verification_uri_complete": base + "/status?user_code=" + da.UserCode,
		"expires_in":                int(deviceflow.AuthTTL.Seconds()),
		"interval":                  int(deviceflow.PollInterval.Seconds()),
	})
}

// handleToken implements the token endpoint (device_code grant only).
func (s *server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request")
		return
	}
	if r.FormValue("grant_type") != deviceCodeGrantType {
		oauthError(w, "unsupported_grant_type")
		return
	}

	token, errCode := s.store.Poll(r.FormValue("device_code"))
	if errCode != "" {
		oauthError(w, errCode)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(deviceflow.TokenTTL.Seconds()),
	})
}

// verifyEntry is a pending device authorization plus the canonical
// messages an admin signs to act on it. Rendered on /status.
type verifyEntry struct {
	Auth       *deviceflow.Auth
	MsgApprove string
	MsgDeny    string
}

// handleVerifyPage redirects to the /status dashboard, which hosts the
// approval UI. Kept so consoles showing an old verification_uri (and
// bookmarks) still land somewhere useful.
func (s *server) handleVerifyPage(w http.ResponseWriter, r *http.Request) {
	target := "/status"
	if uc := r.URL.Query().Get("user_code"); uc != "" {
		target += "?user_code=" + url.QueryEscape(uc)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *server) handleVerifyPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	userCode := strings.ToUpper(strings.TrimSpace(r.FormValue("user_code")))
	action := r.FormValue("action")
	if action != "approve" && action != "deny" {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	if !s.authorizeAdmin(r, userCode, action) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var err error
	if action == "approve" {
		err = s.store.Approve(userCode)
	} else {
		err = s.store.Deny(userCode)
	}
	if err != nil {
		s.respondAction(w, r, fmt.Sprintf("%s: %v", userCode, err))
		return
	}

	log.Printf("device auth %sd: user_code=%s", action, userCode)
	s.respondAction(w, r, fmt.Sprintf("%s %sd", userCode, action))
}

// authorizeAdmin accepts either a wallet signature over the canonical
// approval message (checked against the address allowlist) or the
// break-glass admin token.
func (s *server) authorizeAdmin(r *http.Request, userCode, action string) bool {
	if sig := r.FormValue("signature"); sig != "" && len(s.adminAddrs) > 0 {
		nonce, err := s.store.NonceFor(userCode)
		if err != nil {
			return false
		}
		addr, err := ethsig.RecoverPersonalSign(approvalMessage(action, userCode, nonce), sig)
		if err != nil {
			log.Printf("signature verification failed for %s: %v", userCode, err)
			return false
		}
		for _, allowed := range s.adminAddrs {
			if addr == allowed {
				log.Printf("wallet %s authorized %s of %s", addr, action, userCode)
				return true
			}
		}
		log.Printf("wallet %s not in allowlist (user_code=%s)", addr, userCode)
		return false
	}

	if s.adminToken != "" && constantTimeEqual(r.FormValue("admin_token"), s.adminToken) {
		return true
	}
	return false
}

// bearerToken extracts the token from the Authorization header, or "".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}
