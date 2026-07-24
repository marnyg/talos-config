// Command config-server serves composed Talos machine configs.
//
// It scans talos/machines/<mac>/ directories for meta.yaml (ip, config,
// patches) and optional patch.yaml, then serves composed configs on
// GET /config?mac=<mac> using the Talos machinery strategic-merge
// patcher — the same code path as `talosctl machineconfig patch`.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"

	"github.com/marnyg/talos-config/config-server/wgderive"
)

// machine is the parsed form of talos/machines/<mac>/meta.yaml.
type machine struct {
	IP      string   `yaml:"ip"`
	Config  string   `yaml:"config"`
	Patches []string `yaml:"patches"`
	WGIP    string   `yaml:"wgIP"` // optional explicit tunnel address (collision override)

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

	store       *authStore
	requireAuth bool
	clientID    string     // expected OAuth client_id ("" = accept any)
	adminToken  string     // break-glass fallback for /verify approval
	adminAddrs  []string   // allowlisted wallet addresses (lowercase 0x)
	wgm         *wgManager // nil = WireGuard disabled entirely
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

	machines, err := loadMachines(filepath.Join(s.root, "machines"))
	if err != nil {
		log.Printf("error loading machines: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	m, ok := machines[mac]
	if !ok {
		http.Error(w, fmt.Sprintf("no config for MAC %s", mac), http.StatusNotFound)
		return
	}

	var extra []string
	if s.wgm != nil {
		wg := s.wgm.current()
		if wg == nil {
			// Serving a config without the tunnel would strand the
			// machine outside the control channel — refuse instead.
			log.Printf("refusing config for %s: wireguard is sealed", mac)
			http.Error(w, "sealed: an admin must unseal the control channel at /verify", http.StatusServiceUnavailable)
			return
		}
		p, err := wg.machinePatch(mac, m)
		if err != nil {
			log.Printf("error building wg patch for %s: %v", mac, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		extra = append(extra, p)
	}

	body, err := buildConfig(s.root, m, extra...)
	if err != nil {
		log.Printf("error building config for %s: %v", mac, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	if s.requireAuth {
		s.store.consume(token) // single-use: burn only after a successful serve
	}
	log.Printf("served config for %s", mac)
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
		log.Fatal("--require-auth needs --admin-address and/or CONFIG_SERVER_ADMIN_TOKEN (something must gate /verify)")
	}

	var wgm *wgManager
	if *wgPort > 0 {
		if *wgEndpoint == "" {
			log.Fatal("--wg-port needs --wg-endpoint (the public ip:port machines dial)")
		}
		serverPrefix, err := netip.ParsePrefix(*wgAddr)
		if err != nil {
			log.Fatalf("--wg-address: %v", err)
		}
		wgm = newWGManager(*wgPort, serverPrefix, *wgEndpoint, *wgPubkey, *root, addrs)

		if env := os.Getenv("WG_MASTER_KEY"); env != "" {
			// Dev/testing escape hatch: auto-unseal from an explicit
			// master key. Production runs sealed (no secret at rest).
			master, err := wgderive.MasterFromHex(env)
			if err != nil {
				log.Fatalf("WG_MASTER_KEY: %v", err)
			}
			if err := wgm.unsealWithMaster(master); err != nil {
				log.Fatalf("unsealing from WG_MASTER_KEY: %v", err)
			}
			log.Print("wireguard unsealed from WG_MASTER_KEY env (dev mode)")
		} else {
			if len(addrs) == 0 {
				log.Fatal("sealed wireguard needs --admin-address (a wallet must be able to unseal)")
			}
			log.Printf("wireguard SEALED: an admin must sign %q at /verify to unseal", wgderive.MasterMessage)
		}
	}

	s := &server{
		root:        *root,
		store:       newAuthStore(),
		requireAuth: *requireAuth,
		clientID:    *clientID,
		adminToken:  adminToken,
		adminAddrs:  addrs,
		wgm:         wgm,
	}

	machines, err := loadMachines(filepath.Join(s.root, "machines"))
	if err != nil {
		log.Fatalf("loading machines: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", s.handleConfig)
	mux.HandleFunc("POST /device/code", s.handleDeviceCode)
	mux.HandleFunc("POST /token", s.handleToken)
	mux.HandleFunc("GET /verify", s.handleVerifyPage)
	mux.HandleFunc("POST /verify", s.handleVerifyPost)
	mux.HandleFunc("POST /unseal", s.handleUnseal)
	mux.HandleFunc("GET /sealed", s.handleSealed)

	addr := fmt.Sprintf("%s:%d", *bind, *port)
	log.Printf("serving configs on %s (auth required: %v)", addr, *requireAuth)
	for mac, m := range machines {
		log.Printf("  %s -> %s", mac, m.Config)
	}
	log.Fatal(http.ListenAndServe(addr, mux))
}
