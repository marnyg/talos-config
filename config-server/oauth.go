package main

// HTTP handlers for the OAuth2 device flow (RFC 8628) and the human
// verification page. Endpoint paths match the Talos defaults derived
// from the talos.config URL: /device/code and /token.

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
)

const deviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"

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

	da := s.store.begin(clientID, identity)
	base := externalBase(r)

	log.Printf("device auth started: user_code=%s identity=%v", da.UserCode, identity)
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               da.DeviceCode,
		"user_code":                 da.UserCode,
		"verification_uri":          base + "/verify",
		"verification_uri_complete": base + "/verify?user_code=" + da.UserCode,
		"expires_in":                int(deviceAuthTTL.Seconds()),
		"interval":                  int(pollInterval.Seconds()),
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

	token, errCode := s.store.poll(r.FormValue("device_code"))
	if errCode != "" {
		oauthError(w, errCode)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(tokenTTL.Seconds()),
	})
}

var verifyTemplate = template.Must(template.New("verify").Parse(`<!DOCTYPE html>
<html>
<head><title>Machine approval</title>
<style>
 body { font-family: monospace; max-width: 40rem; margin: 2rem auto; }
 table { border-collapse: collapse; width: 100%; }
 td, th { border: 1px solid #999; padding: .4rem .6rem; text-align: left; }
 .msg { padding: .5rem; border: 1px solid #999; margin-bottom: 1rem; }
</style></head>
<body>
<h1>Pending machine approvals</h1>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
{{if not .Pending}}<p>No pending requests.</p>{{end}}
{{range .Pending}}
<table>
 <tr><th>User code</th><td>{{.UserCode}}</td></tr>
 {{range $k, $v := .Identity}}<tr><th>{{$k}}</th><td>{{$v}}</td></tr>{{end}}
 <tr><th>Requested</th><td>{{.CreatedAt.Format "15:04:05"}}</td></tr>
</table>
<form method="POST" action="/verify">
 <input type="hidden" name="user_code" value="{{.UserCode}}">
 <input type="password" name="admin_token" placeholder="admin token" required>
 <button name="action" value="approve">Approve</button>
 <button name="action" value="deny">Deny</button>
</form>
<br>
{{end}}
</body></html>`))

type verifyPageData struct {
	Pending []*deviceAuth
	Message string
}

func (s *server) handleVerifyPage(w http.ResponseWriter, r *http.Request) {
	s.renderVerify(w, "")
}

func (s *server) handleVerifyPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	if s.adminToken == "" || !constantTimeEqual(r.FormValue("admin_token"), s.adminToken) {
		http.Error(w, "invalid admin token", http.StatusForbidden)
		return
	}

	userCode := strings.ToUpper(strings.TrimSpace(r.FormValue("user_code")))
	var err error
	action := r.FormValue("action")
	switch action {
	case "approve":
		err = s.store.approve(userCode)
	case "deny":
		err = s.store.deny(userCode)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.renderVerify(w, fmt.Sprintf("%s: %v", userCode, err))
		return
	}

	log.Printf("device auth %sd: user_code=%s", action, userCode)
	s.renderVerify(w, fmt.Sprintf("%s %sd", userCode, action))
}

func (s *server) renderVerify(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := verifyTemplate.Execute(w, verifyPageData{Pending: s.store.pending(), Message: msg}); err != nil {
		log.Printf("rendering verify page: %v", err)
	}
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
