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
//
// Beside the master lives the hub's IDENTITY (ADR-0018, ADR-0024): a
// random per-process hubkey that holds no authority until the same
// wallet signs a speak-as cert to it. The unseal is therefore two
// EIP-191 signatures from one allowlisted wallet (decision ce8): one
// over MasterMessage (master/seed — the nebula plane), one over the
// Issuer's proposal (hubkey authority — the identity plane). They may
// arrive in one POST or separately; the second must come from the
// wallet that signed the first.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/config-server/boottoken"
	"github.com/marnyg/talos-config/config-server/enroll"
	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/masterderive"
	"github.com/marnyg/talos-config/config-server/mesh"
	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
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
	mesh *mesh.Manager

	// issuer is the hub's identity: the per-process hubkey and the
	// speak-as the wallet signs to it. Sealed independently of the
	// master (a dev-mode master unseal leaves it sealed; the nag window
	// re-seals it while the master stays held).
	issuer  *issuer.Issuer
	wallets []cert.ActorID // adminAddrs as eth: actor ids

	// enroll is the Enroll actor (ADR-0024): own key, consented to by
	// the Issuer for #mint-device, speaking to it over the in-process
	// actor network. The Issuer's inbox verifies the wallet's approval
	// inside every request; Enroll is a courier, not an authority.
	enroll *enroll.Enroll

	// wan is the Issuer's second wire (talos-config-e8d): the hub's own
	// iroh endpoint, bound with the hubkey so hubkey == EndpointId
	// (ADR-0024). nil ⇒ in-process only (tests, --iroh-relay unset).
	wan actor.Endpoint

	// publicURL is the HTTPS base members and nodes reach this hub at
	// (--iroh-relay: one hostname carries HTTPS and the relay, ADR-0022).
	// "" ⇒ no identity plane: served configs carry no agent document.
	publicURL string
	// bootSeen is the volatile single-use guard for boot tokens
	// (nodeenroll.go); safe to lose.
	bootSeen boottoken.Seen

	mu     sync.Mutex
	master []byte // nil while sealed
	wallet string // wallet that unsealed the master; "" if sealed or from env
}

// hubTransport binds the hubkey on a second wire beside the in-process
// network. The key is generated inside newHubManager (per process,
// never durable), so the binder receives it rather than the reverse.
type hubTransport func(priv ed25519.PrivateKey) (actor.Endpoint, error)

// locationTTL is the hub's reach-me-at lifetime. ADR-0001 sketches
// ≈ 1 h for roaming actors; the hub sits behind one fixed relay URL
// for the process's life, and a member beats days apart, so a record
// that outlives one grant runway (GrantTTL) spares every beat a
// /.well-known round trip. Refreshed well inside that.
const (
	locationTTL     = issuer.GrantTTL
	locationRefresh = 6 * time.Hour
)

func newHubManager(root string, adminAddrs []string, pinnedCAFP string, nm *mesh.Manager, wan hubTransport) (*hubManager, error) {
	// The hub's actors on one in-process network: the Issuer (hubkey,
	// random per process) listens; Enroll dials it. With wan set the
	// same key is also bound on iroh and the Issuer accepts from both
	// through one actor.Multi (talos-config-e8d): one actor, one inbox,
	// two wires.
	net := actor.NewMemoryNetwork()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("hubkey: %w", err)
	}
	hubID := cert.NewEdSigner(priv).ActorID()
	mem, err := net.Bind(hubID, "hub")
	if err != nil {
		return nil, err
	}
	var transport actor.Endpoint = mem
	var wanEp actor.Endpoint
	if wan != nil {
		if wanEp, err = wan(priv); err != nil {
			return nil, fmt.Errorf("hub endpoint: %w", err)
		}
		if transport, err = actor.NewMulti(mem, wanEp); err != nil {
			return nil, err
		}
	}
	iss := issuer.NewWithKey(priv, mesh.Groups(), transport, nil)
	// #bundle compiles talos/mesh-policy-v3.yaml and hands out
	// mesh-blocklist-v3.txt from the checkout on every beat: git as
	// compiler input, nothing cached (invariant 2).
	iss.Policy = issuer.FilePolicy(root)
	en, err := enroll.New(net, hubID, nil)
	if err != nil {
		return nil, err
	}
	if err := iss.Admit(en.ID()); err != nil {
		return nil, err
	}
	wallets := make([]cert.ActorID, 0, len(adminAddrs))
	for _, a := range adminAddrs {
		w := issuer.WalletID(a)
		if err := w.Validate(); err != nil {
			return nil, fmt.Errorf("admin address %q: %w", a, err)
		}
		wallets = append(wallets, w)
	}
	return &hubManager{root: root, adminAddrs: adminAddrs, pinnedCAFP: pinnedCAFP, mesh: nm, issuer: iss, wallets: wallets, enroll: en, wan: wanEp}, nil
}

// listen runs the Issuer's actor for the process's life. Sealed or
// not: its inbox refuses until the wallet's speak-as is held. With a
// wan endpoint it also keeps the hub's reach-me-at fresh: the record
// piggybacks on every reply, so a member that beat once knows where
// the hub is without any lookup service (ADR-0001 location records).
func (m *hubManager) listen(ctx context.Context) {
	if m.wan != nil {
		go m.publishLocation(ctx)
	}
	if err := m.issuer.Listen(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("issuer: listen: %v", err)
	}
}

func (m *hubManager) publishLocation(ctx context.Context) {
	t := time.NewTicker(locationRefresh)
	defer t.Stop()
	for {
		if loc, err := m.issuer.Actor.PublishLocation(locationTTL, m.wanTags()...); err != nil {
			log.Printf("issuer: reach-me-at: %v", err)
		} else {
			log.Printf("issuer: reach-me-at %v (until +%dd)", loc.Cav.Endpoints, locationTTL/issuer.Day)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// wanTags are the tags the hub publishes: the wan endpoint's relay
// tags only. The hub is relay-only by construction (ADR-0022: no QUIC
// address discovery, no inbound UDP on fly's shared address), so a
// direct iroh:udp= tag would name a 6PN-private socket nobody can use;
// the in-process mem: tag is not for anyone outside the process.
func (m *hubManager) wanTags() []string {
	if m.wan == nil {
		return nil
	}
	var out []string
	for _, tag := range m.wan.Endpoints() {
		if strings.HasPrefix(tag, irohTagRelay) {
			out = append(out, tag)
		}
	}
	return out
}

// irohTagRelay mirrors irohtransport.TagRelay without importing the
// cgo module into the untagged build.
const irohTagRelay = "iroh:relay="

// endpoints are the hub's advertised wan tags ("" when in-process only).
func (m *hubManager) endpoints() string {
	return strings.Join(m.wanTags(), " ")
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
	addr, err := ethsig.RecoverPersonalSign(masterderive.MasterMessage, sigHex)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	if !slices.Contains(m.adminAddrs, addr) {
		return fmt.Errorf("wallet %s not in allowlist", addr)
	}
	master, err := masterderive.MasterFromSignatureHex(sigHex)
	if err != nil {
		return err
	}
	if err := m.unsealWithMaster(master); err != nil {
		return err
	}
	m.mu.Lock()
	m.wallet = addr
	m.mu.Unlock()
	log.Printf("wallet %s unsealed the hub", addr)
	return nil
}

// masterWallet is the wallet whose signature derived the held master,
// "" while sealed or when the master came from the dev env.
func (m *hubManager) masterWallet() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wallet
}

// unsealIssuer accepts the wallet's EIP-191 signature over the Issuer's
// speak-as proposal. The signature selects the wallet among the
// allowlist (the proposal names it as iss); it must be the wallet that
// unsealed the master, when one did (ce8: one root, two signatures).
func (m *hubManager) unsealIssuer(sigHex string) (string, error) {
	if len(m.wallets) == 0 {
		return "", fmt.Errorf("no admin addresses configured; cannot verify speak-as signature")
	}
	candidates := m.wallets
	if mw := m.masterWallet(); mw != "" {
		candidates = []cert.ActorID{issuer.WalletID(mw)}
	}
	w, err := m.issuer.Unseal(sigHex, candidates)
	if err != nil {
		if errors.Is(err, issuer.ErrNotAllowed) && len(candidates) < len(m.wallets) {
			return "", fmt.Errorf("speak-as must be signed by %s, the wallet that unsealed the master: %w", candidates[0], err)
		}
		return "", err
	}
	addr := string(w)[len("eth:"):]
	log.Printf("wallet %s unsealed hub identity %s (speak-as until +%dd)", addr, m.issuer.Fingerprint(), m.issuer.Runway()/issuer.Day)
	return addr, nil
}

// identityLine renders the Issuer's state for /sealed and /status; warn
// is true when the owner must act (sealed or in the nag window).
func (m *hubManager) identityLine() (line string, warn bool) {
	fp := m.issuer.Fingerprint()
	wallet := strings.TrimPrefix(string(m.issuer.Wallet()), "eth:")
	days := m.issuer.Runway() / issuer.Day
	at := ""
	if eps := m.endpoints(); eps != "" {
		at = " · at " + eps
	}
	switch err := m.issuer.Serving(); {
	case errors.Is(err, issuer.ErrSealed):
		return "hubkey " + fp + " — SEALED: no speak-as held; sign the proposal above" + at, true
	case errors.Is(err, issuer.ErrNag):
		return fmt.Sprintf("hubkey %s speaks for %s — %d d left, NAG: re-sign the proposal above to renew%s", fp, wallet, days, at), true
	default:
		return fmt.Sprintf("hubkey %s speaks for %s — %d d left%s", fp, wallet, days, at), false
	}
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
		if err := m.mesh.UnsealWithMaster(master); err != nil {
			log.Printf("MESH DOWN: %v", err)
		}
	}
	return nil
}

// handleUnseal accepts the admin's signatures: `signature` over
// MasterMessage and/or `speakas_signature` over the Issuer's proposal.
// Either alone is fine (the page only asks for what is still sealed);
// with both, the master goes first so the speak-as is bound to the
// same wallet. A rejected speak-as after an accepted master is still a
// 403 — the master stays held (idempotent), the page re-asks.
func (s *server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.Error(w, "hub sealing disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	sig, saSig := r.FormValue("signature"), r.FormValue("speakas_signature")
	if sig == "" && saSig == "" {
		http.Error(w, "missing signature", http.StatusBadRequest)
		return
	}
	var done []string
	if sig != "" {
		if err := s.hub.unsealWithSignature(sig); err != nil {
			log.Printf("unseal rejected: %v", err)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		done = append(done, "hub unsealed")
	}
	if saSig != "" {
		if _, err := s.hub.unsealIssuer(saSig); err != nil {
			log.Printf("identity unseal rejected: %v", err)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		done = append(done, "hub identity unsealed ("+s.hub.issuer.Fingerprint()+")")
	}
	s.respondAction(w, r, strings.Join(done, "; "))
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
	nm := s.mesh()
	var meshErr error
	if nm != nil {
		_, _, meshErr = nm.State()
	}

	// Identity pages too, once this hub serves an identity plane
	// (--iroh-relay set; talos-config-tqr, flipped 2026-09-19 when the
	// first members beat at the hubkey): a sealed hubkey means no
	// #renew, no #bundle, no enrollment, and the nag window is the
	// runway running out. Without --iroh-relay there is no plane to
	// page for — the dev-mode env master leaves identity sealed forever.
	var identity string
	var identityWarn bool
	if s.hub != nil {
		identity, identityWarn = s.hub.identityLine()
		identityWarn = identityWarn && s.hub.publicURL != ""
	}

	if sealed || meshErr != nil || identityWarn {
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
	if s.hub != nil {
		fmt.Fprintln(w, "identity: "+identity)
	}

	switch {
	case nm == nil:
		fmt.Fprintln(w, "mesh: disabled")
	case nm.Up():
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
func (s *server) mesh() *mesh.Manager {
	if s.hub == nil {
		return nil
	}
	return s.hub.mesh
}

// wellKnownSpeakAsPath serves the hub's current speak-as over WAN HTTPS
// (ADR-0024 "cold cache after a deploy"): a member whose hub rotated
// its hubkey lacks exactly one cert — the wallet's new speak-as — and
// fetches it here before its first beat. The cert is wallet-signed and
// verified offline; web PKI is only the hint channel (invariant 4's
// permitted direction, invariant 5's single entrypoint). 503 while
// sealed: there is nothing true to say yet.
const wellKnownSpeakAsPath = "/.well-known/talos-hub/speak-as"

// wellKnownReachMeAtPath serves the hub's current reach-me-at beside
// it (talos-config-e8d): the same cold-cache case needs the NEW
// hubkey's location record too, since a location record is only valid
// signed by the actor it locates (checkLocation: iss == id) and a
// member's cached one names the dead key. Hubkey-signed; the speak-as
// from the sibling path is what makes that key worth listening to.
// 503 while the hub has no wan endpoint or has not published yet.
const wellKnownReachMeAtPath = "/.well-known/talos-hub/reach-me-at"

func (s *server) handleWellKnownSpeakAs(w http.ResponseWriter, _ *http.Request) {
	if s.hub == nil {
		http.NotFound(w, nil)
		return
	}
	sa := s.hub.issuer.SpeakAs()
	if sa == nil {
		http.Error(w, "sealed: the hub holds no speak-as", http.StatusServiceUnavailable)
		return
	}
	writeCert(w, *sa)
}

func (s *server) handleWellKnownReachMeAt(w http.ResponseWriter, _ *http.Request) {
	if s.hub == nil {
		http.NotFound(w, nil)
		return
	}
	loc := s.hub.issuer.Actor.CurrentLocation()
	if loc == nil {
		http.Error(w, "the hub has no published location", http.StatusServiceUnavailable)
		return
	}
	writeCert(w, *loc)
}

func writeCert(w http.ResponseWriter, c cert.Cert) {
	b, err := cert.Encode(c)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}
