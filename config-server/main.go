// Command config-server serves composed Talos machine configs.
//
// It scans talos/machines/<mac>/ directories for meta.yaml (ip, config,
// patches) and optional patch.yaml, then serves composed configs on
// GET /config?mac=<mac> using the Talos machinery strategic-merge
// patcher — the same code path as `talosctl machineconfig patch`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"

	kmsapi "github.com/siderolabs/kms-client/api/kms"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/masterderive"
)

// masterKeyEnv is the dev/testing escape hatch that auto-unseals the
// hub. The "WG" is historical (see masterderive.MasterMessage): the
// value is the same 32-byte master the wallet signature derives, and
// renaming the variable would only invite a mismatch with deployed
// tooling.
const masterKeyEnv = "WG_MASTER_KEY"

// machine is the parsed form of talos/machines/<mac>/meta.yaml.
type machine struct {
	IP      string   `yaml:"ip"`
	Config  string   `yaml:"config"`
	Patches []string `yaml:"patches"`
	// MeshIP is the nebula overlay address override, for derived-address
	// collisions. Load-bearing beyond DNS: mesh certs bake the address,
	// so a collision must be resolvable without re-rooting anything.
	MeshIP string `yaml:"meshIP"`
	// Name is the machine's tunnel DNS label (<name>.<domain>);
	// defaults to the MAC with dashes.
	Name string `yaml:"name"`
	// UUID is the node's SMBIOS UUID (shown on /status at approval).
	// It is the durable KMS unseal allowlist: deleting it revokes.
	UUID string `yaml:"uuid"`
	// DiskEncryption injects systemDiskEncryption (KMS + recovery
	// passphrase) into the served config. Takes effect at install
	// time only — an existing machine needs a wipe to encrypt.
	DiskEncryption bool `yaml:"diskEncryption"`

	dir string // machines/<mac> directory, absolute
}

// talosRoot returns the talos/ directory of the enclosing git repo.
func talosRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "talos"), nil
}

// normalizeMAC lowercases and converts dashes to colons.
func normalizeMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(mac, "-", ":"))
}

// loadMachines scans machinesDir for <mac>/meta.yaml, returns MAC → machine.
func loadMachines(machinesDir string) (map[string]machine, error) {
	entries, err := os.ReadDir(machinesDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", machinesDir, err)
	}

	machines := make(map[string]machine)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(machinesDir, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading meta for %s: %w", e.Name(), err)
		}

		var m machine
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("parsing %s/meta.yaml: %w", e.Name(), err)
		}
		m.dir = dir
		machines[normalizeMAC(e.Name())] = m
	}
	return machines, nil
}

// buildConfig composes the base config with all patches for the
// machine, plus any literal extra patches (applied last; used for
// serve-time mesh/disk-key injection so key material never hits disk).
func buildConfig(root string, m machine, extra ...string) ([]byte, error) {
	base, err := os.ReadFile(filepath.Join(root, m.Config))
	if err != nil {
		return nil, fmt.Errorf("reading base config: %w", err)
	}

	patchRefs := make([]string, 0, len(m.Patches)+len(extra)+1)
	for _, p := range m.Patches {
		patchRefs = append(patchRefs, "@"+filepath.Join(root, p))
	}
	if machinePatch := filepath.Join(m.dir, "patch.yaml"); fileExists(machinePatch) {
		patchRefs = append(patchRefs, "@"+machinePatch)
	}
	patchRefs = append(patchRefs, extra...)

	patches, err := configpatcher.LoadPatches(patchRefs)
	if err != nil {
		return nil, fmt.Errorf("loading patches: %w", err)
	}

	out, err := configpatcher.Apply(configpatcher.WithBytes(base), patches)
	if err != nil {
		return nil, fmt.Errorf("applying patches: %w", err)
	}
	return out.Bytes()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// serveTimePatches renders the patches that never touch disk: everything
// keyed off the unsealed master. They share one shape — derive, and
// refuse the serve if the derivation says no, because a machine is
// better off retrying than installing a config it cannot use.
func (s *server) serveTimePatches(mac string, m machine, machines map[string]machine) ([]string, int, string) {
	if s.hub == nil {
		if m.DiskEncryption {
			// Disk keys derive from the master; without the hub there is none.
			log.Printf("refusing config for %s: diskEncryption requires the hub control channel", mac)
			return nil, http.StatusInternalServerError, "internal error"
		}
		return nil, http.StatusOK, ""
	}

	master := s.hub.current()
	if master == nil {
		// Serving a config without the master would strand the machine
		// outside the mesh (no identity, no disk keys) — refuse instead.
		log.Printf("refusing config for %s: hub is sealed", mac)
		return nil, http.StatusServiceUnavailable, "sealed: an admin must unseal the hub at /status"
	}

	var extra []string

	if m.DiskEncryption {
		if s.kmsAdvertise == "" {
			log.Printf("refusing config for %s: diskEncryption set but no --kms-advertise endpoint", mac)
			return nil, http.StatusInternalServerError, "internal error"
		}
		extra = append(extra, diskEncryptionPatch(master, mac, s.kmsAdvertise))
	}

	// Mesh identity. Derivation only needs the master, so this does not
	// care whether the hub's *own* nebula service came up — a node still
	// gets a correct config while the hub's mesh is down, and reaches the
	// mesh when the hub returns. A failure here is a repo mistake
	// (address collision, bad meshIP), not a runtime condition.
	if mesh := s.hub.mesh; mesh != nil {
		p, err := mesh.nebMachinePatch(master, mac, m, machines)
		if err != nil {
			log.Printf("error building mesh patch for %s: %v", mac, err)
			return nil, http.StatusInternalServerError, "internal error"
		}
		extra = append(extra, p)
	}

	return extra, http.StatusOK, ""
}

// server holds the immutable-per-request state.
type server struct {
	root string // talos/ directory

	store        *deviceflow.Store
	sessions     *sessionStore // SIWE sessions for /status
	requireAuth  bool
	clientID     string        // expected OAuth client_id ("" = accept any)
	adminToken   string        // break-glass fallback for approval/login
	adminAddrs   []string      // allowlisted wallet addresses (lowercase 0x)
	hub          *hubManager   // nil = seal/mesh machinery disabled entirely
	boot         *bootstrapper // nil unless --auto-bootstrap
	kms          *kmsServer    // nil unless KMS enabled
	kmsAdvertise string        // endpoint machines dial for disk unseal
	started      time.Time

	fetchMu sync.Mutex
	fetches map[string]time.Time // MAC -> last successful config serve
}

// recordFetch notes a successful config serve, for /status.
func (s *server) recordFetch(mac string) {
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	if s.fetches == nil {
		s.fetches = map[string]time.Time{}
	}
	s.fetches[mac] = time.Now()
}

func (s *server) lastFetch(mac string) (time.Time, bool) {
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	t, ok := s.fetches[mac]
	return t, ok
}

// composeFor builds the fully-injected config for mac: base config,
// repo patches, and the serve-time injections (mesh identity,
// certSANs, disk encryption). On failure it returns a
// non-200 status with a client-safe message; details go to the log.
func (s *server) composeFor(mac string) ([]byte, int, string) {
	machines, err := loadMachines(filepath.Join(s.root, "machines"))
	if err != nil {
		log.Printf("error loading machines: %v", err)
		return nil, http.StatusInternalServerError, "internal error"
	}

	m, ok := machines[mac]
	if !ok {
		return nil, http.StatusNotFound, fmt.Sprintf("no config for MAC %s", mac)
	}

	extra, status, msg := s.serveTimePatches(mac, m, machines)
	if status != http.StatusOK {
		return nil, status, msg
	}

	body, err := buildConfig(s.root, m, extra...)
	if err != nil {
		log.Printf("error building config for %s: %v", mac, err)
		return nil, http.StatusInternalServerError, "internal error"
	}
	return body, http.StatusOK, ""
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	mac := r.URL.Query().Get("mac")
	if mac == "" {
		http.Error(w, "missing mac parameter", http.StatusBadRequest)
		return
	}
	mac = normalizeMAC(mac)

	token := bearerToken(r)
	if s.requireAuth {
		if err := s.store.Validate(token, mac); err != nil {
			log.Printf("rejected config request for %s: %v", mac, err)
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	body, status, msg := s.composeFor(mac)
	if status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	if s.requireAuth {
		s.store.Consume(token) // single-use: burn only after a successful serve
	}
	s.recordFetch(mac)
	log.Printf("served config for %s", mac)
}

// handleTunnelConfig serves hub-composed configs over the overlay
// listener for `nix run .#apply`. No bearer token: the route is only
// reachable on the mesh (serveMeshHTTP, gated by derived admin device
// addresses under the cert-group firewall), and membership is
// wallet-rooted via enrollment. It does not consume device-flow tokens
// or record fetches — the machine itself is not fetching.
func (s *server) handleTunnelConfig(w http.ResponseWriter, r *http.Request) {
	mac := r.URL.Query().Get("mac")
	if mac == "" {
		http.Error(w, "missing mac parameter", http.StatusBadRequest)
		return
	}
	mac = normalizeMAC(mac)

	body, status, msg := s.composeFor(mac)
	if status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	_, _ = w.Write(body)
	log.Printf("served config for %s to admin peer %s over the tunnel", mac, r.RemoteAddr)
}

// mux wires all routes. Shared between main and the HTTP tests so the
// two can never drift.
func (s *server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", s.handleConfig)
	mux.HandleFunc("POST /device/code", s.handleDeviceCode)
	mux.HandleFunc("POST /token", s.handleToken)
	mux.HandleFunc("GET /verify", s.handleVerifyPage)
	mux.HandleFunc("POST /verify", s.handleVerifyPost)
	mux.HandleFunc("POST /unseal", s.handleUnseal)
	mux.HandleFunc("GET /mesh/enroll", s.handleMeshEnrollChallenge)
	mux.HandleFunc("POST /mesh/enroll", s.handleMeshEnroll)
	mux.HandleFunc("GET /mesh/tv", s.handleMeshTVPage)
	mux.HandleFunc("POST /mesh/tv", s.handleMeshTVStart)
	mux.HandleFunc("GET /mesh/tv/config", s.handleMeshTVConfig)
	mux.HandleFunc("GET /sealed", s.handleSealed)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("POST /status/login", s.handleStatusLogin)
	mux.HandleFunc("POST /status/logout", s.handleStatusLogout)
	return mux
}

func main() {
	var (
		port         = flag.Int("port", 8080, "listen port")
		bind         = flag.String("bind", "0.0.0.0", "bind address")
		root         = flag.String("root", "", "talos/ directory (default: <git root>/talos)")
		requireAuth  = flag.Bool("require-auth", false, "require OAuth device-flow bearer token on /config")
		clientID     = flag.String("client-id", "talos-pxe", "expected OAuth client_id (empty = accept any)")
		adminAddrs   = flag.String("admin-address", "", "comma-separated wallet addresses allowed to approve machines")
		meshPort     = flag.Int("mesh-port", 0, "nebula UDP listen port (0 = disabled; the hub starts sealed); the hub is the mesh lighthouse + relay")
		meshSubnet   = flag.String("mesh-subnet", "10.42.0.0/16", "mesh overlay CIDR; the hub takes the first host address (derived, not configurable)")
		meshHost     = flag.String("mesh-listen-host", "", "address nebula binds (default: fly-global-services on fly, 0.0.0.0 elsewhere)")
		meshEndpoint = flag.String("mesh-endpoint", "", "public host:port mesh members dial to reach the hub lighthouse (required with --mesh-port)")
		meshZone     = flag.String("mesh-dns-zone", meshDNSZone, "DNS zone the hub serves on the mesh (empty = no mesh DNS)")
		meshDevices  = flag.String("mesh-devices", "", "comma-separated owner devices in the mesh admins group (e.g. laptop,phone); identities derived from the master")
		meshMedia    = flag.String("mesh-media-devices", "", "comma-separated shared-space devices in the mesh media group (e.g. androidtv); reach media only, never node control surfaces")
		meshCAPin    = flag.String("mesh-ca-pin", "", "pinned expected mesh CA fingerprint (hex); unseal fails on mismatch (wrong wallet or message)")
		autoBoot     = flag.Bool("auto-bootstrap", false, "bootstrap the single declared control plane over the mesh when its etcd waits for it")
		kmsAdv       = flag.String("kms-advertise", "", "KMS endpoint machines dial for disk unseal (e.g. https://host:443); enables the KMS gRPC service (requires --mesh-port)")
		kmsPort      = flag.Int("kms-port", 8081, "dedicated plaintext-h2 gRPC listen port for the KMS service (0 = only the shared h2c port)")
	)
	flag.Parse()

	if *root == "" {
		r, err := talosRoot()
		if err != nil {
			log.Fatalf("cannot determine talos root (pass --root): %v", err)
		}
		*root = r
	}

	var addrs []string
	for _, a := range strings.Split(*adminAddrs, ",") {
		if strings.TrimSpace(a) == "" {
			continue
		}
		norm, err := ethsig.NormalizeAddress(a)
		if err != nil {
			log.Fatalf("--admin-address: %v", err)
		}
		addrs = append(addrs, norm)
	}

	adminToken := os.Getenv("CONFIG_SERVER_ADMIN_TOKEN")
	if *requireAuth && adminToken == "" && len(addrs) == 0 {
		log.Fatal("--require-auth needs --admin-address and/or CONFIG_SERVER_ADMIN_TOKEN (something must gate machine approval)")
	}

	var hub *hubManager
	var masterEnv string
	if *meshPort > 0 {
		subnet, err := netip.ParsePrefix(*meshSubnet)
		if err != nil {
			log.Fatalf("--mesh-subnet: %v", err)
		}
		if subnet.Addr() != subnet.Masked().Addr() {
			log.Fatalf("--mesh-subnet %s is not a network address (did you mean %s?)", subnet, subnet.Masked())
		}
		if *meshEndpoint == "" {
			log.Fatal("--mesh-port needs --mesh-endpoint (the public host:port members dial to find the lighthouse)")
		}
		listenHost := *meshHost
		if listenHost == "" {
			listenHost = resolveMeshListenHost()
		}
		devices, err := parseMeshDevices(*meshDevices, *meshMedia)
		if err != nil {
			log.Fatalf("--mesh-devices/--mesh-media-devices: %v", err)
		}
		mesh := newNebManager(*meshPort, subnet, listenHost, *meshEndpoint, *meshZone, *root, devices)
		hub = newHubManager(*root, addrs, *meshCAPin, mesh)
		log.Printf("mesh enabled: %s on udp/%d, binding %s (unseals with the hub)", subnet, *meshPort, listenHost)

		// Dev/testing escape hatch: the master env auto-unseals — but
		// the unseal itself runs after the server is built, so the
		// overlay /config route is wired before the mesh comes up.
		masterEnv = os.Getenv(masterKeyEnv)
		if masterEnv == "" {
			if len(addrs) == 0 {
				log.Fatal("a sealed hub needs --admin-address (a wallet must be able to unseal)")
			}
			log.Printf("hub SEALED: an admin must sign %q at /status to unseal", masterderive.MasterMessage)
		}
	}

	s := &server{
		root:         *root,
		store:        deviceflow.NewStore(),
		sessions:     newSessionStore(),
		requireAuth:  *requireAuth,
		clientID:     *clientID,
		adminToken:   adminToken,
		adminAddrs:   addrs,
		hub:          hub,
		kmsAdvertise: *kmsAdv,
		started:      time.Now(),
	}

	if hub != nil {
		// The overlay /config route serves hub-composed configs to admin
		// devices; wired here because the handler needs the full server.
		hub.mesh.tunnelConfig = http.HandlerFunc(s.handleTunnelConfig)
		if masterEnv != "" {
			master, err := masterderive.MasterFromHex(masterEnv)
			if err != nil {
				log.Fatalf("%s: %v", masterKeyEnv, err)
			}
			if err := hub.unsealWithMaster(master); err != nil {
				log.Fatalf("unsealing from %s: %v", masterKeyEnv, err)
			}
			log.Printf("hub unsealed from %s env (dev mode)", masterKeyEnv)
		}
	}

	if *kmsAdv != "" {
		if hub == nil {
			log.Fatal("--kms-advertise requires --mesh-port (disk keys derive from the same master)")
		}
		s.kms = newKMSServer(*root, hub)
		log.Printf("kms: serving disk unseal, advertised endpoint %s", *kmsAdv)
		if *kmsPort > 0 {
			// Dedicated listener for fly's gRPC port: the h2c handler
			// speaks both prior-knowledge h2 and the Upgrade dialect,
			// whichever the proxy uses.
			lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *bind, *kmsPort))
			if err != nil {
				log.Fatalf("kms listener: %v", err)
			}
			go func() { log.Fatal(http.Serve(lis, s.handler())) }()
			log.Printf("kms: grpc listening on %s:%d", *bind, *kmsPort)
		}
	}

	machines, err := loadMachines(filepath.Join(s.root, "machines"))
	if err != nil {
		log.Fatalf("loading machines: %v", err)
	}

	if *autoBoot {
		if hub == nil {
			log.Fatal("--auto-bootstrap requires --mesh-port (it dials nodes over the mesh)")
		}
		s.boot = newBootstrapper(*root, hub)
		go s.boot.run(context.Background())
	}

	addr := fmt.Sprintf("%s:%d", *bind, *port)
	log.Printf("serving configs on %s (auth required: %v)", addr, *requireAuth)
	for mac, m := range machines {
		log.Printf("  %s -> %s", mac, m.Config)
	}
	log.Fatal(http.ListenAndServe(addr, s.handler()))
}

// handler wraps the mux with the KMS gRPC service (h2c so fly's proxy
// can speak HTTP/2 cleartext to us; gRPC requests are told apart by
// their content type).
func (s *server) handler() http.Handler {
	if s.kms == nil {
		return s.mux()
	}
	grpcSrv := grpc.NewServer()
	kmsapi.RegisterKMSServiceServer(grpcSrv, s.kms)
	mux := s.mux()
	mixed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcSrv.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return h2c.NewHandler(mixed, &http2.Server{})
}
