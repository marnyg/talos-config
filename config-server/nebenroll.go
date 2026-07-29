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
	"time"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/nebderive"
)

// nebDeviceCertValidity bounds a device's leaf certificate. Shorter than
// a machine's five years because re-enrolling a device is one command
// against a hub that needs no memory of the last one — this is the one
// place where short-lived certs are a practical revocation strategy
// rather than a maintenance cliff (thread uuid dc04e3e8).
const nebDeviceCertValidity = 90 * 24 * time.Hour

// nebDeviceTunDev is the interface name a device's client creates.
// Distinct from the nodes' so a machine that ever also enrolled as a
// device could not collide with itself.
const nebDeviceTunDev = "nebula1"

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
	mesh := s.mesh()
	if mesh == nil {
		http.Error(w, "mesh disabled", http.StatusNotFound)
		return
	}
	d, ok := mesh.device(r.URL.Query().Get("name"))
	if !ok {
		http.Error(w, "unknown device name (declare it in MESH_DEVICES or MESH_MEDIA_DEVICES)", http.StatusNotFound)
		return
	}
	if s.wgm.sealed() {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}
	nonce := s.sessions.issueNonce()
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    d.name,
		"group":   d.group,
		"nonce":   nonce,
		"message": meshEnrollMessage(d.name, nonce),
	})
}

// handleMeshEnroll (POST /mesh/enroll: name, nonce, signature) verifies
// the signed challenge and returns the device's nebula config.
func (s *server) handleMeshEnroll(w http.ResponseWriter, r *http.Request) {
	mesh := s.mesh()
	if mesh == nil {
		http.Error(w, "mesh disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	nonce := r.FormValue("nonce")
	d, ok := mesh.device(name)
	if !ok {
		http.Error(w, "unknown device name (declare it in MESH_DEVICES or MESH_MEDIA_DEVICES)", http.StatusNotFound)
		return
	}
	addr, err := recoverPersonalSign(meshEnrollMessage(d.name, nonce), r.FormValue("signature"))
	if err != nil {
		log.Printf("mesh enroll %q: signature verification failed: %v", d.name, err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !slices.Contains(s.adminAddrs, addr) {
		log.Printf("mesh enroll %q: wallet %s not in allowlist", d.name, addr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.sessions.redeemNonce(nonce) {
		log.Printf("mesh enroll %q: expired or replayed nonce", d.name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	wg := s.wgm.current()
	if wg == nil {
		http.Error(w, "sealed: an admin must unseal the hub at /status", http.StatusServiceUnavailable)
		return
	}

	cfg, err := mesh.deviceConfig(wg.master, d)
	if err != nil {
		log.Printf("mesh enroll %q: %v", d.name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The wallet that authorized issuance is logged; the cert it
	// authorized is not secret either, but the key in the body is, so
	// this is the only record kept and it is deliberately thin.
	log.Printf("wallet %s enrolled mesh device %q (group %s)", addr, d.name, d.group)
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	_, _ = w.Write(cfg)
}

// deviceConfig renders a device's self-contained nebula config: the
// derived identity, inline, plus the hub as its only lighthouse and
// relay.
//
// The address comes from buildMeshZone for the same reason a machine's
// does — mesh DNS and the cert must agree, and this is where a collision
// with a machine or another device is caught before a cert bakes it in.
func (m *nebManager) deviceConfig(master []byte, d nebDevice) ([]byte, error) {
	if m.endpoint == "" {
		return nil, fmt.Errorf("mesh endpoint is not configured (--mesh-endpoint)")
	}
	machines, err := loadMachines(m.machinesDir())
	if err != nil {
		return nil, fmt.Errorf("loading machines: %w", err)
	}
	zone, err := buildMeshZone(master, m.subnet, machines, m.devices)
	if err != nil {
		return nil, fmt.Errorf("building mesh zone: %w", err)
	}
	addr, ok := zone[d.name]
	if !ok {
		return nil, fmt.Errorf("device %q is not in the mesh zone", d.name)
	}

	hubIP, err := nebderive.HubIP(m.subnet)
	if err != nil {
		return nil, err
	}
	caPEM, err := nebderive.CACertPEM(master)
	if err != nil {
		return nil, fmt.Errorf("deriving mesh CA: %w", err)
	}
	priv, pub := nebderive.DeviceKey(master, d.name)
	now := time.Now()
	crt, err := nebderive.HostCert(master, d.name, pub, addr, m.subnet,
		[]string{d.group}, now.Add(-nebClockSkew), now.Add(nebDeviceCertValidity))
	if err != nil {
		return nil, fmt.Errorf("minting cert for %q: %w", d.name, err)
	}
	crtPEM, err := crt.MarshalPEM()
	if err != nil {
		return nil, fmt.Errorf("marshalling cert for %q: %w", d.name, err)
	}

	cfg := nebConfigYAML{
		PKI: nebPKIYAML{
			CA:   string(caPEM),
			Cert: string(crtPEM),
			Key:  string(nebderive.HostKeyPEM(priv)),
		},
		StaticHostMap: map[string][]string{hubIP.String(): {m.endpoint}},
		Lighthouse: nebLighthouseYAML{
			Interval: 60,
			Hosts:    []string{hubIP.String()},
		},
		// Port 0: devices roam. A fixed port buys a node the
		// lighthouse-less LAN fallback (nebmachine.go), but a laptop that
		// changes networks daily gains nothing from it and can lose to
		// whatever else holds 4242 on a coffee-shop NAT.
		Listen: nebListenYAML{Host: "0.0.0.0", Port: 0},
		Tun:    &nebTunYAML{Dev: nebDeviceTunDev, MTU: nebNodeMTU},
		Punchy: nebPunchyYAML{Punch: true, Respond: true},
		Relay:  nebRelayYAML{UseRelays: true, Relays: []string{hubIP.String()}},
		Firewall: nebFirewallYAML{
			// Outbound open: a device is the thing that initiates. Inbound
			// needs only ICMP, because nebula's firewall is stateful —
			// replies to flows this device started are already allowed.
			OutboundAction: "drop",
			InboundAction:  "drop",
			Outbound:       []nebRuleYAML{{Port: "any", Proto: "any", Host: "any"}},
			Inbound:        []nebRuleYAML{{Port: "any", Proto: "icmp", Host: "any"}},
		},
		Logging: nebLoggingYAML{Level: "info", Format: "text"},
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling device config: %w", err)
	}
	return out, nil
}
