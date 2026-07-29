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

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// machine is the parsed form of talos/machines/<mac>/meta.yaml.
type machine struct {
	IP      string   `yaml:"ip"`
	Config  string   `yaml:"config"`
	Patches []string `yaml:"patches"`
	WGIP    string   `yaml:"wgIP"` // optional explicit tunnel address (collision override)
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
// serve-time WireGuard injection so key material never hits disk).
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

// server holds the immutable-per-request state.
type server struct {
	root string // talos/ directory

	store        *authStore
	sessions     *sessionStore // SIWE sessions for /status
	requireAuth  bool
	clientID     string        // expected OAuth client_id ("" = accept any)
	adminToken   string        // break-glass fallback for approval/login
	adminAddrs   []string      // allowlisted wallet addresses (lowercase 0x)
	wgm          *wgManager    // nil = WireGuard disabled entirely
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
// repo patches, and the serve-time injections (wg0 control channel,
// certSANs, disk encryption). On failure it returns a non-200 status
// with a client-safe message; details go to the log.
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

	var extra []string
	if s.wgm != nil {
		wg := s.wgm.current()
		if wg == nil {
			// Serving a config without the tunnel would strand the
			// machine outside the control channel — refuse instead.
			log.Printf("refusing config for %s: wireguard is sealed", mac)
			return nil, http.StatusServiceUnavailable, "sealed: an admin must unseal the control channel at /status"
		}
		p, err := wg.machinePatch(mac, m)
		if err != nil {
			log.Printf("error building wg patch for %s: %v", mac, err)
			return nil, http.StatusInternalServerError, "internal error"
		}
		extra = append(extra, p)

		if m.DiskEncryption {
			if s.kmsAdvertise == "" {
				log.Printf("refusing config for %s: diskEncryption set but no --kms-advertise endpoint", mac)
				return nil, http.StatusInternalServerError, "internal error"
			}
			extra = append(extra, wg.diskEncryptionPatch(mac, s.kmsAdvertise))
		}
	} else if m.DiskEncryption {
		// Disk keys derive from the master; without WG there is none.
		log.Printf("refusing config for %s: diskEncryption requires the wireguard control channel", mac)
		return nil, http.StatusInternalServerError, "internal error"
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
		if err := s.store.validate(token, mac); err != nil {
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
		s.store.consume(token) // single-use: burn only after a successful serve
	}
	s.recordFetch(mac)
	log.Printf("served config for %s", mac)
}

// handleTunnelConfig serves hub-composed configs over the tunnel
// listener for `nix run .#apply`. No bearer token: the route is only
// reachable on the tunnel and gated to admin peer source addresses
// (serveTunnelHTTP), whose membership is wallet-rooted via /wg/enroll.
// It does not consume device-flow tokens or record fetches — the
// machine itself is not fetching.
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
	mux.HandleFunc("GET /wg/enroll", s.handleEnrollChallenge)
	mux.HandleFunc("POST /wg/enroll", s.handleEnroll)
	mux.HandleFunc("GET /sealed", s.handleSealed)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("POST /status/login", s.handleStatusLogin)
	mux.HandleFunc("POST /status/logout", s.handleStatusLogout)
	return mux
}

func main() {
	var (
		port        = flag.Int("port", 8080, "listen port")
		bind        = flag.String("bind", "0.0.0.0", "bind address")
		root        = flag.String("root", "", "talos/ directory (default: <git root>/talos)")
		requireAuth = flag.Bool("require-auth", false, "require OAuth device-flow bearer token on /config")
		clientID    = flag.String("client-id", "talos-pxe", "expected OAuth client_id (empty = accept any)")
		adminAddrs  = flag.String("admin-address", "", "comma-separated wallet addresses allowed to approve machines")
		wgPort      = flag.Int("wg-port", 0, "WireGuard UDP listen port (0 = disabled; starts sealed)")
		wgAddr      = flag.String("wg-address", "10.99.0.1/24", "server tunnel address with subnet")
		wgEndpoint  = flag.String("wg-endpoint", "", "public ip:port machines dial to reach the tunnel (required with --wg-port)")
		wgPubkey    = flag.String("wg-server-pubkey", "", "pinned expected server pubkey (base64 or hex); unseal fails on mismatch")
		wgAdmins    = flag.String("wg-admin-peers", "", "comma-separated named admin WG peers (e.g. laptop); keys derived from the master, config via /wg/enroll (wgup) or wgping -admin")
		wgDNS       = flag.String("wg-dns-domain", "talos.wg", "DNS domain the hub serves on the tunnel (empty = no tunnel DNS)")
		autoBoot    = flag.Bool("auto-bootstrap", false, "bootstrap the single declared control plane over the tunnel when its etcd waits for it")
		kmsAdv      = flag.String("kms-advertise", "", "KMS endpoint machines dial for disk unseal (e.g. https://host:443); enables the KMS gRPC service (requires --wg-port)")
		kmsPort     = flag.Int("kms-port", 8081, "dedicated plaintext-h2 gRPC listen port for the KMS service (0 = only the shared h2c port)")
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
		norm, err := normalizeAddress(a)
		if err != nil {
			log.Fatalf("--admin-address: %v", err)
		}
		addrs = append(addrs, norm)
	}

	adminToken := os.Getenv("CONFIG_SERVER_ADMIN_TOKEN")
	if *requireAuth && adminToken == "" && len(addrs) == 0 {
		log.Fatal("--require-auth needs --admin-address and/or CONFIG_SERVER_ADMIN_TOKEN (something must gate machine approval)")
	}

	var wgm *wgManager
	var wgMasterEnv string
	if *wgPort > 0 {
		if *wgEndpoint == "" {
			log.Fatal("--wg-port needs --wg-endpoint (the public ip:port machines dial)")
		}
		serverPrefix, err := netip.ParsePrefix(*wgAddr)
		if err != nil {
			log.Fatalf("--wg-address: %v", err)
		}
		var adminPeers []string
		for _, name := range strings.Split(*wgAdmins, ",") {
			if n := strings.TrimSpace(name); n != "" {
				adminPeers = append(adminPeers, n)
			}
		}
		wgm = newWGManager(*wgPort, serverPrefix, *wgEndpoint, *wgPubkey, *wgDNS, *root, addrs, adminPeers)

		// Dev/testing escape hatch: WG_MASTER_KEY auto-unseals — but
		// the unseal itself runs after the server is built, so the
		// tunnel /config route is wired before the tunnel comes up.
		wgMasterEnv = os.Getenv("WG_MASTER_KEY")
		if wgMasterEnv == "" {
			if len(addrs) == 0 {
				log.Fatal("sealed wireguard needs --admin-address (a wallet must be able to unseal)")
			}
			log.Printf("wireguard SEALED: an admin must sign %q at /status to unseal", wgderive.MasterMessage)
		}
	}

	s := &server{
		root:         *root,
		store:        newAuthStore(),
		sessions:     newSessionStore(),
		requireAuth:  *requireAuth,
		clientID:     *clientID,
		adminToken:   adminToken,
		adminAddrs:   addrs,
		wgm:          wgm,
		kmsAdvertise: *kmsAdv,
		started:      time.Now(),
	}

	if wgm != nil {
		// The tunnel /config route serves hub-composed configs to admin
		// peers; wired here because the handler needs the full server.
		wgm.tunnelConfig = http.HandlerFunc(s.handleTunnelConfig)
		if wgMasterEnv != "" {
			master, err := wgderive.MasterFromHex(wgMasterEnv)
			if err != nil {
				log.Fatalf("WG_MASTER_KEY: %v", err)
			}
			if err := wgm.unsealWithMaster(master); err != nil {
				log.Fatalf("unsealing from WG_MASTER_KEY: %v", err)
			}
			log.Print("wireguard unsealed from WG_MASTER_KEY env (dev mode)")
		}
	}

	if *kmsAdv != "" {
		if wgm == nil {
			log.Fatal("--kms-advertise requires --wg-port (disk keys derive from the same master)")
		}
		s.kms = newKMSServer(*root, wgm)
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
		if wgm == nil {
			log.Fatal("--auto-bootstrap requires --wg-port (it works over the tunnel)")
		}
		s.boot = newBootstrapper(*root, wgm)
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
