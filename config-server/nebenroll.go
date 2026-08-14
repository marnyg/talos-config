package main

// Mesh device enrollment (ADR-0012): wallet-signed, device-generated
// keys, no declared device list. One verify+mint core, two entry
// modes:
//
//   - nebup direct — wallet local: `POST /mesh/enroll/challenge` for
//     the canonical v1 message and nonce, then `POST /mesh/enroll`
//     with the signature. The client (nebup) generated its keypair
//     locally and submits the pubkey; the private key never travels.
//
//   - RFC 8628 device-flow — for devices that cannot sign locally:
//     the device tool submits its pubkey and a proposed name to
//     `POST /mesh/enroll/device`, receives {device_code, user_code},
//     and polls /token. The approver visits /status and signs the
//     canonical v1 message with the *final* (name, group) via
//     `POST /mesh/enroll/approve`. On approval the hub mints the
//     cert immediately and stashes the config on the pending grant;
//     `GET /mesh/enroll/config` (bearer token) redeems it.
//
// The two paths converge in verifyAndMint: identical signature and
// mint semantics, so a device joins with one wallet signature no
// matter which mode it started from.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"slices"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/mesh"
	"github.com/marnyg/talos-config/config-server/nebderive"
)

// meshEnrollMessageV1 is the canonical text a wallet signs to enroll
// a mesh device (ADR-0012). Distinct prefix from the wg enrollment,
// machine approval, login, and master-key messages: a signature for
// one of those must never be replayable as another. The v1 tag reserves
// space for a future v2 without ambiguity; the hub accepts one version
// at a time.
func meshEnrollMessageV1(name, group, fingerprint, nonce string) string {
	return fmt.Sprintf(
		"talos config-server mesh device enrollment v1\nname: %s\ngroup: %s\npubkey: %s\nnonce: %s",
		name, group, fingerprint, nonce,
	)
}

// enrollRequest is the params both entry paths converge on before the
// verify+mint core runs. Populated from POST form (nebup) or from the
// /status approval form (device flow).
type enrollRequest struct {
	Name      string
	Group     string
	PubkeyHex string
	Nonce     string
	Signature string
}

// verifyAndMint is the pure(-ish) core: given a signed enrollment
// request, verify the signature against the wallet allowlist and mint
// the device's config. Returns the config bytes (with pki.key empty;
// the client splices its own key) plus the wallet address that signed.
// Does NOT redeem the nonce — the caller does that once (only!) after
// a successful mint, so a spent signature cannot mint twice. For the
// same reason it does not log an enrollment either: the mint is pure
// and stateless, and the audit line belongs to the caller that commits
// the result (nonce redeemed / flow approved), not to a mint that may
// yet be refused.
func (s *server) verifyAndMint(req enrollRequest) ([]byte, string, error) {
	nm := s.mesh()
	if nm == nil {
		return nil, "", fmt.Errorf("mesh disabled")
	}
	if req.Name == "" || req.PubkeyHex == "" || req.Nonce == "" || req.Signature == "" {
		return nil, "", fmt.Errorf("name, group, pubkey, nonce and signature are all required")
	}
	if req.Group != mesh.GroupAdmins && req.Group != mesh.GroupMedia {
		return nil, "", fmt.Errorf("group must be %q or %q", mesh.GroupAdmins, mesh.GroupMedia)
	}

	pub, err := decodePubkeyHex(req.PubkeyHex)
	if err != nil {
		return nil, "", fmt.Errorf("pubkey: %w", err)
	}
	fp := mesh.PubkeyFingerprint(pub)
	name := nebderive.Normalize(req.Name)

	addr, err := ethsig.RecoverPersonalSign(meshEnrollMessageV1(name, req.Group, fp, req.Nonce), req.Signature)
	if err != nil {
		return nil, "", fmt.Errorf("signature verification failed: %w", err)
	}
	if !slices.Contains(s.adminAddrs, addr) {
		return nil, "", fmt.Errorf("wallet %s not in allowlist", addr)
	}

	master := s.hub.current()
	if master == nil {
		return nil, "", fmt.Errorf("sealed: an admin must unseal the hub at /status")
	}
	cfg, err := nm.EnrollDevice(mesh.EnrollDeviceParams{
		Master: master, Name: name, Group: req.Group, Pubkey: pub,
	})
	if err != nil {
		return nil, "", err
	}
	return cfg, addr, nil
}

// decodePubkeyHex parses a 64-char hex string as a 32-byte X25519
// pubkey. Distinct step so error messages are pointed and callers do
// not silently accept short input.
func decodePubkeyHex(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("not hex: %w", err)
	}
	if len(b) != 32 {
		return out, fmt.Errorf("want 32 bytes (64 hex chars), got %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}

// handleMeshEnrollChallenge (POST /mesh/enroll/challenge:
// name, group, pubkey) issues the challenge nebup signs. The nonce is
// server-issued and single-use; the fingerprint is echoed so the
// client can display it in the signing UI.
func (s *server) handleMeshEnrollChallenge(w http.ResponseWriter, r *http.Request) {
	nm := s.mesh()
	if nm == nil {
		http.Error(w, "mesh disabled", http.StatusNotFound)
		return
	}
	if s.hub.sealed() {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := nebderive.Normalize(r.FormValue("name"))
	group := r.FormValue("group")
	if group == "" {
		group = mesh.GroupAdmins
	}
	if group != mesh.GroupAdmins && group != mesh.GroupMedia {
		http.Error(w, "group must be admins or media", http.StatusBadRequest)
		return
	}
	pub, err := decodePubkeyHex(r.FormValue("pubkey"))
	if err != nil {
		http.Error(w, "pubkey: "+err.Error(), http.StatusBadRequest)
		return
	}
	fp := mesh.PubkeyFingerprint(pub)
	nonce := s.sessions.issueNonce()
	writeJSON(w, http.StatusOK, map[string]string{
		"name":        name,
		"group":       group,
		"nonce":       nonce,
		"fingerprint": fp,
		"message":     meshEnrollMessageV1(name, group, fp, nonce),
	})
}

// handleMeshEnroll (POST /mesh/enroll: name, group, pubkey, nonce,
// signature) verifies the signed challenge and returns the device's
// nebula config. The nonce is redeemed only after a successful mint so
// a mid-flight failure lets the client retry with the same challenge.
func (s *server) handleMeshEnroll(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	cfg, addr, err := s.verifyAndMint(enrollRequest{
		Name:      r.FormValue("name"),
		Group:     r.FormValue("group"),
		PubkeyHex: r.FormValue("pubkey"),
		Nonce:     r.FormValue("nonce"),
		Signature: r.FormValue("signature"),
	})
	if err != nil {
		log.Printf("mesh enroll: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.sessions.redeemNonce(r.FormValue("nonce")) {
		// verifyAndMint minted, but the nonce is spent — this branch
		// means we mint twice in a race, or a replayed signature over a
		// nonce this server never issued. Refuse before the caller
		// takes delivery; nothing is logged as enrolled.
		log.Printf("mesh enroll: refused %q — nonce expired, replayed or never issued", nebderive.Normalize(r.FormValue("name")))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	log.Printf("wallet %s enrolled mesh device %q (group %s)", addr, nebderive.Normalize(r.FormValue("name")), r.FormValue("group"))
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	_, _ = w.Write(cfg)
}

// meshEnrollClientID labels device-flow enrollments in logs and on the
// approval dashboard. Not a secret (RFC 8628 public client).
const meshEnrollClientID = "mesh-enroll"

// handleMeshEnrollPage (GET /mesh/enroll) renders the introduction
// page. Not the start of a flow: a device that can't sign starts by
// POSTing to /mesh/enroll/device from its own tool (nebup-headless or
// an appliance client), not by filling in this page.
func (s *server) handleMeshEnrollPage(w http.ResponseWriter, r *http.Request) {
	if s.mesh() == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := meshEnrollIntroTemplate.Execute(w, nil); err != nil {
		log.Printf("rendering mesh enroll intro: %v", err)
	}
}

// handleMeshEnrollDevice (POST /mesh/enroll/device: pubkey,
// proposed_name?, proposed_group?) begins an RFC 8628 flow for a
// device that cannot sign locally. The proposed name/group are hints
// for the approver's /status card; the approver decides the final
// values (invariant: rubber-stamp resistance).
//
// The tool driving this endpoint is expected to display a QR of
// verification_uri_complete on-screen so the operator scans, signs,
// and the device polls /token for the resulting config.
func (s *server) handleMeshEnrollDevice(w http.ResponseWriter, r *http.Request) {
	nm := s.mesh()
	if nm == nil {
		http.NotFound(w, r)
		return
	}
	if s.hub.sealed() {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	pub, err := decodePubkeyHex(r.FormValue("pubkey"))
	if err != nil {
		http.Error(w, "pubkey: "+err.Error(), http.StatusBadRequest)
		return
	}
	proposedName := nebderive.Normalize(r.FormValue("proposed_name"))
	proposedGroup := r.FormValue("proposed_group")
	if proposedGroup != mesh.GroupAdmins && proposedGroup != mesh.GroupMedia {
		proposedGroup = mesh.GroupMedia
	}
	fp := mesh.PubkeyFingerprint(pub)

	// Identity carries the render inputs for the approver's /status
	// card. The pubkey stays server-side (bytes, in identity as hex)
	// so it cannot be swapped by a client between start and approval.
	identity := map[string]string{
		"pubkey":         hex.EncodeToString(pub[:]),
		"pubkey_fp":      fp,
		"proposed_name":  proposedName,
		"proposed_group": proposedGroup,
	}
	da := s.store.Begin(deviceflow.KindMeshEnroll, meshEnrollClientID, identity)
	base := externalBase(r)
	approveURL := base + "/status?user_code=" + da.UserCode

	// The tool may or may not want the QR; produce it, and let the
	// caller ignore what it does not need.
	png, _ := qrcode.Encode(approveURL, qrcode.Medium, 256)

	log.Printf("mesh enroll started: user_code=%s proposed_name=%q proposed_group=%s fp=%s\u2026",
		da.UserCode, proposedName, proposedGroup, fp[:12])
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               da.DeviceCode,
		"user_code":                 da.UserCode,
		"verification_uri":          base + "/status",
		"verification_uri_complete": approveURL,
		"qr_png_base64":             base64.StdEncoding.EncodeToString(png),
		"expires_in":                int(deviceflow.AuthTTL.Seconds()),
		"interval":                  int(deviceflow.PollInterval.Seconds()),
		"fingerprint":               fp,
	})
}

// handleMeshEnrollApprove (POST /mesh/enroll/approve: user_code, name,
// group, admin_retype?, signature) is the approver's side of the
// device-flow path. Session-gated + wallet-signed: the session lets
// the operator into /status, but the *mint* still requires a wallet
// signature over the canonical v1 message with the FINAL (name, group)
// the operator chose.
func (s *server) handleMeshEnrollApprove(w http.ResponseWriter, r *http.Request) {
	if !s.statusEnabled() {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.sessionAddr(r); !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.mesh() == nil {
		http.Error(w, "mesh disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userCode := r.FormValue("user_code")
	name := nebderive.Normalize(r.FormValue("name"))
	group := r.FormValue("group")
	sig := r.FormValue("signature")
	adminRetype := r.FormValue("admin_retype")

	if userCode == "" || name == "" || sig == "" {
		http.Error(w, "user_code, name and signature required", http.StatusBadRequest)
		return
	}
	// admins-group promotion requires the operator to re-type the
	// device name. Checked server-side (not just JS): the retype is a
	// deliberate friction gate against a slip on the radio button.
	if group == mesh.GroupAdmins && adminRetype != name {
		s.respondAction(w, r, fmt.Sprintf("%s: admins requires retyping the device name to confirm", userCode))
		return
	}

	// Read the pending flow to recover the pubkey and nonce; the
	// operator cannot override those.
	da, ok := s.pendingMeshEnroll(userCode)
	if !ok {
		s.respondAction(w, r, fmt.Sprintf("%s: unknown or expired user code", userCode))
		return
	}
	pubHex := da.Identity["pubkey"]

	cfg, walletAddr, err := s.verifyAndMint(enrollRequest{
		Name:      name,
		Group:     group,
		PubkeyHex: pubHex,
		Nonce:     da.Nonce,
		Signature: sig,
	})
	if err != nil {
		log.Printf("mesh enroll approve %s: %v", userCode, err)
		s.respondAction(w, r, fmt.Sprintf("%s: %v", userCode, err))
		return
	}
	// Write the final decisions back so the /status card shows what
	// was actually approved (helpful during the poll wait).
	_ = s.store.UpdateIdentity(userCode, map[string]string{"name": name, "group": group})
	if err := s.store.ApproveWithPayload(userCode, cfg); err != nil {
		log.Printf("mesh enroll approve %s: %v", userCode, err)
		s.respondAction(w, r, fmt.Sprintf("%s: %v", userCode, err))
		return
	}
	log.Printf("wallet %s approved mesh enrollment: user_code=%s name=%q group=%s", walletAddr, userCode, name, group)
	s.respondAction(w, r, fmt.Sprintf("%s approved (%s, %s) \u2014 poll /token to fetch config", userCode, name, group))
}

// pendingMeshEnroll finds a pending KindMeshEnroll by user_code.
// Snapshot copy: the caller reads Identity and Nonce; the store still
// owns the underlying record.
func (s *server) pendingMeshEnroll(userCode string) (*deviceflow.Auth, bool) {
	for _, da := range s.store.Pending() {
		if da.UserCode == userCode && da.Kind == deviceflow.KindMeshEnroll {
			return da, true
		}
	}
	return nil, false
}

// handleMeshEnrollConfig (GET /mesh/enroll/config, Bearer token)
// redeems an approved mesh-enroll token for the minted config. The
// token was minted on the first successful /token poll after the
// operator's approval; the config bytes were stashed on the grant at
// approval time (see ApproveWithPayload).
func (s *server) handleMeshEnrollConfig(w http.ResponseWriter, r *http.Request) {
	if s.mesh() == nil {
		http.NotFound(w, r)
		return
	}
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	cfg, err := s.store.MeshEnrollPayload(token)
	if err != nil {
		log.Printf("mesh enroll config: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.store.Consume(token)
	// The fingerprint the operator approved is echoed in a header so a
	// paranoid client can compare it against the pubkey it submitted
	// before splicing in the matching private key.
	sum := sha256.Sum256(cfg)
	w.Header().Set("X-Config-Sha256", hex.EncodeToString(sum[:]))
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	_, _ = w.Write(cfg)
	log.Printf("mesh enroll: served config (%d bytes)", len(cfg))
}

var meshEnrollIntroTemplate = template.Must(template.New("meshenroll").Parse(`<!DOCTYPE html>
<html>
<head><title>Join the mesh</title><style>` + statusStyle + `</style></head>
<body>
<h1>Join the mesh</h1>
<p>Enrollment is wallet-signed. There are two ways in:</p>
<ul>
 <li><strong>With a local wallet</strong> — run <code>nebup</code> and follow the browser prompt. One signature, no server-side state.</li>
 <li><strong>Without a local wallet</strong> (headless appliance, TV) — the device tool starts a flow and displays a QR pointing at <code>/status</code>. The owner scans, signs, and the device polls for its config.</li>
</ul>
<p>In both cases the device generates its keypair locally; the hub never sees a private key.</p>
</body></html>`))
