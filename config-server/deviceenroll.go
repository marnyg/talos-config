package main

// Mesh device enrollment (ADR-0012): wallet-signed, device-generated
// keys, no declared device list. The device mints its NodeId (an ed:
// actor id = its iroh EndpointId) locally and presents it; the wallet
// signs enrollmsg.V3(name, group, node, nonce); the Issuer actor mints
// the member Kit (#mint-device via Enroll, ADR-0024), re-verifying the
// wallet's signature itself. One verify+mint core, two entry modes:
//
//   - direct — wallet local (irohup): `POST /mesh/enroll/challenge`
//     for the canonical message and nonce, then `POST /mesh/enroll`
//     with the signature. The answer is the Kit.
//
//   - RFC 8628 device-flow — for devices that cannot sign locally
//     (the phone/TV app, the gateway pod): the device tool submits its
//     node id and a proposed name to `POST /mesh/enroll/device`,
//     receives {device_code, user_code}, and polls /token. The
//     approver visits /status and signs the canonical message with
//     the *final* (name, group) via `POST /mesh/enroll/approve`. On
//     approval the hub mints the Kit immediately and stashes it on
//     the pending grant; `GET /mesh/enroll/config` (bearer token)
//     redeems it.
//
// The two paths converge in verifyAndMint: identical signature and
// mint semantics, so a device joins with one wallet signature no
// matter which mode it started from.

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/enrollmsg"
	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/machines"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/cert"
)

// enrollRequest is the params both entry paths converge on before the
// verify+mint core runs. Populated from POST form (direct) or from the
// /status approval form (device flow).
type enrollRequest struct {
	Name      string
	Group     string
	Node      string // ed: actor id, the device's iroh EndpointId
	Nonce     string
	Signature string
}

// enrollContentType is what an enrollment hands the device: the Kit as
// JSON (issuer.EncodeKit).
const enrollContentType = "application/json"

// normalizeName lowercases and trims a device name. Part of the
// signing contract: the hub and every signer (irohup, the /status
// page's live rebuild) must agree on the form or the signature is
// over a different message.
func normalizeName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// nameTaken reports whether a device name would collide with a name
// git already owns: a declared machine's mesh label, or the hub's. The
// name map is witnessed, not registered (decision 2fc), so nothing
// downstream would refuse a second member with the same name — it
// would just make <name>.<zone> ambiguous. Refused here, before the
// wallet act.
func (s *server) nameTaken(name string) (bool, error) {
	if name == nodeagent.HubName {
		return true, nil
	}
	byMAC, err := machines.Load(filepath.Join(s.root, "machines"))
	if err != nil {
		return false, err
	}
	for mac, m := range byMAC {
		if machines.DNSName(mac, m) == name {
			return true, nil
		}
	}
	return false, nil
}

// parseNode validates a node id from a form: it must be an ed: actor
// id.
func parseNode(v string) (string, error) {
	id := cert.ActorID(v)
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("node: %w", err)
	}
	if sch, _ := id.Scheme(); sch != "ed:" {
		return "", fmt.Errorf("node: must be an ed: actor id (the device's iroh EndpointId)")
	}
	return string(id), nil
}

// verifyAndMint is the pure(-ish) core: given a signed enrollment
// request, verify the signature against the wallet allowlist and mint
// the device's Kit. Returns the encoded Kit plus the wallet address
// that signed. Does NOT redeem the nonce — the caller does that once
// (only!) after a successful mint, so a spent signature cannot mint
// twice. For the same reason it does not log an enrollment either:
// the mint is stateless, and the audit line belongs to the caller
// that commits the result (nonce redeemed / flow approved), not to a
// mint that may yet be refused.
func (s *server) verifyAndMint(req enrollRequest) ([]byte, string, error) {
	if s.hub == nil {
		return nil, "", fmt.Errorf("hub disabled")
	}
	if req.Name == "" || req.Node == "" || req.Nonce == "" || req.Signature == "" {
		return nil, "", fmt.Errorf("name, group, node, nonce and signature are all required")
	}
	if !policy.DeviceGroup(req.Group) {
		return nil, "", fmt.Errorf("group must be %q or %q", policy.GroupAdmins, policy.GroupMedia)
	}
	node, err := parseNode(req.Node)
	if err != nil {
		return nil, "", err
	}
	name := normalizeName(req.Name)
	if taken, err := s.nameTaken(name); err != nil {
		return nil, "", err
	} else if taken {
		return nil, "", fmt.Errorf("name %q is a declared machine or the hub", name)
	}

	addr, err := ethsig.RecoverPersonalSign(enrollmsg.V3(name, req.Group, node, req.Nonce), req.Signature)
	if err != nil {
		return nil, "", fmt.Errorf("signature verification failed: %w", err)
	}
	if !slices.Contains(s.adminAddrs, addr) {
		return nil, "", fmt.Errorf("wallet %s not in allowlist", addr)
	}
	if err := s.hub.issuer.Serving(); err != nil {
		return nil, "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kit, err := s.hub.enroll.MintDevice(ctx, issuer.MintDeviceRequest{
		Node: cert.ActorID(node), Name: name, Group: req.Group,
		Nonce: req.Nonce, Signature: req.Signature,
	})
	if err != nil {
		return nil, "", err
	}
	body, err := issuer.EncodeKit(kit)
	if err != nil {
		return nil, "", err
	}
	return body, addr, nil
}

// handleMeshEnrollChallenge (POST /mesh/enroll/challenge: name, group,
// node) issues the challenge the wallet signs. The nonce is
// server-issued and single-use; the message is echoed so the client
// can display it in the signing UI.
func (s *server) handleMeshEnrollChallenge(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.Error(w, "hub disabled", http.StatusNotFound)
		return
	}
	if err := s.hub.issuer.Serving(); err != nil {
		http.Error(w, "identity plane: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := normalizeName(r.FormValue("name"))
	if name == "" {
		// verifyAndMint would refuse the eventual enrollment anyway, but
		// handing out a signable message for name "" invites a wasted
		// wallet ceremony; refuse at issue time.
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if taken, err := s.nameTaken(name); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else if taken {
		http.Error(w, "name is a declared machine or the hub", http.StatusConflict)
		return
	}
	group := r.FormValue("group")
	if group == "" {
		group = policy.GroupAdmins
	}
	if !policy.DeviceGroup(group) {
		http.Error(w, "group must be admins or media", http.StatusBadRequest)
		return
	}
	node, err := parseNode(r.FormValue("node"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	nonce := s.sessions.issueNonce()
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    name,
		"group":   group,
		"node":    node,
		"nonce":   nonce,
		"message": enrollmsg.V3(name, group, node, nonce),
	})
}

// handleMeshEnroll (POST /mesh/enroll: name, group, node, nonce,
// signature) verifies the signed challenge and returns the device's
// Kit. The nonce is redeemed only after a successful mint so a
// mid-flight failure lets the client retry with the same challenge.
func (s *server) handleMeshEnroll(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := normalizeName(r.FormValue("name"))
	body, addr, err := s.verifyAndMint(enrollRequest{
		Name:      name,
		Group:     r.FormValue("group"),
		Node:      r.FormValue("node"),
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
		log.Printf("mesh enroll: refused %q — nonce expired, replayed or never issued", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	log.Printf("wallet %s enrolled mesh device %q (group %s)", addr, name, r.FormValue("group"))
	w.Header().Set("Content-Type", enrollContentType)
	_, _ = w.Write(body)
}

// meshEnrollClientID labels device-flow enrollments in logs and on the
// approval dashboard. Not a secret (RFC 8628 public client).
const meshEnrollClientID = "mesh-enroll"

// handleMeshEnrollPage (GET /mesh/enroll) renders the introduction
// page. Not the start of a flow: a device that can't sign starts by
// POSTing to /mesh/enroll/device from its own tool (the app, the
// gateway pod), not by filling in this page.
func (s *server) handleMeshEnrollPage(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := meshEnrollIntroTemplate.Execute(w, nil); err != nil {
		log.Printf("rendering mesh enroll intro: %v", err)
	}
}

// handleMeshEnrollDevice (POST /mesh/enroll/device: node,
// proposed_name?, proposed_group?) begins an RFC 8628 flow for a
// device that cannot sign locally. The proposed name/group are hints
// for the approver's /status card; the approver decides the final
// values (invariant: rubber-stamp resistance).
//
// The tool driving this endpoint is expected to display a QR of
// verification_uri_complete on-screen so the operator scans, signs,
// and the device polls /token for the resulting Kit.
func (s *server) handleMeshEnrollDevice(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.NotFound(w, r)
		return
	}
	// ADR-0024: Enroll refuses to start a flow the identity plane
	// cannot finish — no wasted wallet act, no queue.
	if err := s.hub.issuer.Serving(); err != nil {
		http.Error(w, "identity plane: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	node, err := parseNode(r.FormValue("node"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	proposedName := normalizeName(r.FormValue("proposed_name"))
	proposedGroup := r.FormValue("proposed_group")
	if !policy.DeviceGroup(proposedGroup) {
		proposedGroup = policy.GroupMedia
	}

	// Identity carries the render inputs for the approver's /status
	// card. The node stays server-side (in identity) so it cannot be
	// swapped by a client between start and approval; the wallet's
	// signature covers it.
	identity := map[string]string{
		"node":           node,
		"proposed_name":  proposedName,
		"proposed_group": proposedGroup,
	}
	da := s.store.Begin(deviceflow.KindMeshEnroll, meshEnrollClientID, identity)
	base := externalBase(r)
	approveURL := base + "/status?user_code=" + da.UserCode

	// The tool may or may not want the QR; produce it, and let the
	// caller ignore what it does not need.
	png, _ := qrcode.Encode(approveURL, qrcode.Medium, 256)

	log.Printf("mesh enroll started: user_code=%s proposed_name=%q proposed_group=%s node=%s",
		da.UserCode, proposedName, proposedGroup, node)
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               da.DeviceCode,
		"user_code":                 da.UserCode,
		"verification_uri":          base + "/status",
		"verification_uri_complete": approveURL,
		"qr_png_base64":             base64.StdEncoding.EncodeToString(png),
		"expires_in":                int(deviceflow.AuthTTL.Seconds()),
		"interval":                  int(deviceflow.PollInterval.Seconds()),
	})
}

// handleMeshEnrollApprove (POST /mesh/enroll/approve: user_code, name,
// group, admin_retype?, signature) is the approver's side of the
// device-flow path. Session-gated + wallet-signed: the session lets
// the operator into /status, but the *mint* still requires a wallet
// signature over the canonical message with the FINAL (name, group)
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
	if s.hub == nil {
		http.Error(w, "hub disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userCode := r.FormValue("user_code")
	name := normalizeName(r.FormValue("name"))
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
	if group == policy.GroupAdmins && adminRetype != name {
		s.respondAction(w, r, fmt.Sprintf("%s: admins requires retyping the device name to confirm", userCode))
		return
	}

	// Read the pending flow to recover the node and nonce; the
	// operator cannot override those.
	da, ok := s.pendingMeshEnroll(userCode)
	if !ok {
		s.respondAction(w, r, fmt.Sprintf("%s: unknown or expired user code", userCode))
		return
	}

	payload, walletAddr, err := s.verifyAndMint(enrollRequest{
		Name:      name,
		Group:     group,
		Node:      da.Identity["node"],
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
	if err := s.store.ApproveWithPayload(userCode, payload); err != nil {
		log.Printf("mesh enroll approve %s: %v", userCode, err)
		s.respondAction(w, r, fmt.Sprintf("%s: %v", userCode, err))
		return
	}
	log.Printf("wallet %s approved mesh enrollment: user_code=%s name=%q group=%s", walletAddr, userCode, name, group)
	s.respondAction(w, r, fmt.Sprintf("%s approved (%s, %s) — poll /token to fetch the kit", userCode, name, group))
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
// redeems an approved mesh-enroll token for the minted Kit. The token
// was minted on the first successful /token poll after the operator's
// approval; the Kit bytes were stashed on the grant at approval time
// (see ApproveWithPayload).
func (s *server) handleMeshEnrollConfig(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.NotFound(w, r)
		return
	}
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	kit, err := s.store.MeshEnrollPayload(token)
	if err != nil {
		log.Printf("mesh enroll config: %v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.store.Consume(token)
	w.Header().Set("Content-Type", enrollContentType)
	_, _ = w.Write(kit)
	log.Printf("mesh enroll: served kit (%d bytes)", len(kit))
}

var meshEnrollIntroTemplate = template.Must(template.New("meshenroll").Parse(`<!DOCTYPE html>
<html>
<head><title>Join the mesh</title><style>` + statusStyle + `</style></head>
<body>
<h1>Join the mesh</h1>
<p>Enrollment is wallet-signed. There are two ways in:</p>
<ul>
 <li><strong>With a local wallet</strong> — run <code>irohup</code> and follow the browser prompt. One signature, no server-side state.</li>
 <li><strong>Without a local wallet</strong> (the phone/TV app, a headless member) — the device starts a flow and displays a QR pointing at <code>/status</code>. The owner scans, signs, and the device polls for its member kit.</li>
</ul>
<p>In both cases the device mints its identity key locally; the hub never sees a private key.</p>
</body></html>`))
