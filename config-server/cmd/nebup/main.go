// Command nebup is the owner entrypoint to the nebula mesh. On first
// run it enrolls this device against
// the hub: the hub issues a single-use challenge, the owner signs it with
// an allowlisted wallet (EIP-191 personal_sign — an ordinary auth
// message, NOT the fleet master message), and the hub returns a
// self-contained nebula config, cached locally (0600). Subsequent runs go
// straight to bringing nebula up.
//
// Enrollment mints no state anywhere: the identity is derived from
// (master, device name), so -reenroll after a wipe returns the same key
// and the same overlay address. The hub does not remember that this
// device exists.
//
//	nebup                          # enroll if needed, then run nebula (foreground)
//	nebup -print                   # enroll if needed, print the config path, run nothing
//	nebup -reenroll                # discard the cached config, enroll again
//	nebup -name androidtv -print   # admin-mediated: enroll a device that cannot sign,
//	                               # then transfer the printed file into its client
//	nebup -paste                   # headless: paste the signature instead
//	nebup -dns off                 # no split DNS — mesh names won't resolve
//
// On Linux with systemd-resolved, nebup sets up split DNS for the run:
// only *.mesh.internal is routed to the hub's overlay resolver, the
// rest of the machine's DNS is untouched. The per-link resolved config
// dies with the TUN device, so Ctrl-C is also the DNS teardown.
//
// The device name must be declared on the hub (MESH_DEVICES for owner
// devices, MESH_MEDIA_DEVICES for shared-space appliances). The hub
// decides the group from that declaration, never the client — which is
// what keeps a TV in a shared room off the nodes.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/walletsign"
)

func main() {
	log.SetFlags(0)
	var (
		hub      = flag.String("hub", "https://marnyg-talos-config.fly.dev", "hub base URL")
		name     = flag.String("name", "laptop", "device name (must be declared on the hub)")
		reenroll = flag.Bool("reenroll", false, "discard the cached config and enroll again")
		printCfg = flag.Bool("print", false, "enroll if needed and print the config path instead of running nebula")
		paste    = flag.Bool("paste", false, "paste a signature instead of signing in the browser (headless)")
		dnsMode  = flag.String("dns", "auto", "mesh DNS: auto (split DNS for ."+nebderive.DNSZone+" via resolvectl when available), off")
	)
	flag.Parse()

	dev := strings.ToLower(strings.TrimSpace(*name))
	if dev == "" {
		log.Fatal("-name must not be empty")
	}
	path, err := confPath(dev)
	if err != nil {
		log.Fatal(err)
	}

	if *reenroll {
		_ = os.Remove(path)
	}
	if _, err := os.Stat(path); err != nil {
		endpoint := strings.TrimRight(*hub, "/") + "/mesh/enroll"
		cfg, err := walletsign.Enroll(endpoint, dev, "nebup", *paste)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			log.Fatal(err)
		}
		// 0600: the file carries the device's private key. It is the
		// whole membership credential — anything that can read it is
		// this device on the mesh.
		if err := os.WriteFile(path, cfg, 0o600); err != nil {
			log.Fatal(err)
		}
		log.Printf("enrolled %q — config cached at %s", dev, path)
	}

	if *printCfg {
		fmt.Println(path)
		return
	}
	if err := runNebula(path, *dnsMode); err != nil {
		log.Fatal(err)
	}
}

// confPath is where the device's nebula config is cached.
func confPath(name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return filepath.Join(dir, "talos-mesh", name+".yml"), nil
}

// runNebula runs nebula in the foreground, via sudo when not already
// root: nebula needs a TUN device, and there is no daemon to talk to.
// Foreground on purpose — Ctrl-C is the disconnect, which is the right
// shape for a laptop and for the dogfooding window. A permanent setup
// wants a service unit pointed at the same cached config (nebup -print).
func runNebula(path, dnsMode string) error {
	bin, err := exec.LookPath("nebula")
	if err != nil {
		return fmt.Errorf("nebula not found on PATH (it is in this repo's devshell): %w", err)
	}

	// Split DNS happens on the run path only — never on -print — so a
	// config printed for transfer to another device stays as the hub
	// rendered it (portable: no Linux dev name baked in).
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	adapted, dev, hub, note := adaptSplitDNS(raw, dnsMode, haveResolvectl())
	if note != "" {
		log.Print(note)
	}
	if string(adapted) != string(raw) {
		if err := os.WriteFile(path, adapted, 0o600); err != nil {
			return err
		}
	}
	if dev != "" {
		go pointSplitDNS(dev, hub, nebderive.DNSZone)
	}

	argv := []string{bin, "-config", path}
	if os.Geteuid() != 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	log.Printf("running: %s (Ctrl-C to disconnect)", strings.Join(argv, " "))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// nebTunDev is the interface name pinned into the cached config when
// split DNS is active. Same name the nodes use (nebmachine.go
// nebNodeTunDev): one name fleet-wide. The hub leaves device configs
// nameless because Darwin only accepts utun[0-9]+; pinning is safe here
// because resolvectl's presence proves this is a systemd Linux.
const nebTunDev = "nebula0"

// meshConfYAML is the slice of the hub-rendered config nebup reads (the
// hub's overlay address doubles as the mesh resolver) and writes (the
// TUN device name resolvectl needs as its handle).
type meshConfYAML struct {
	Lighthouse struct {
		Hosts []string `yaml:"hosts"`
	} `yaml:"lighthouse"`
	Tun struct {
		Dev string `yaml:"dev"`
	} `yaml:"tun"`
}

// adaptSplitDNS decides whether this run gets split DNS and pins the
// TUN device name into the config if it needs one. Pure function.
// Returns the (possibly rewritten) config, the device and hub-resolver
// handles for pointSplitDNS ("" = no split DNS), and a note for the
// user. Idempotent: an already-pinned config passes through unchanged.
func adaptSplitDNS(cfg []byte, mode string, haveResolvectl bool) (out []byte, dev, hub, note string) {
	if mode == "off" {
		return cfg, "", "", ""
	}
	if !haveResolvectl {
		return cfg, "", "", "mesh DNS disabled (no resolvectl for split DNS) — use overlay IPs (nebup -print; the address is in the cert)"
	}
	var mc meshConfYAML
	if err := yaml.Unmarshal(cfg, &mc); err != nil || len(mc.Lighthouse.Hosts) == 0 {
		return cfg, "", "", "mesh DNS disabled (no lighthouse address in the cached config — nebup -reenroll to refresh it)"
	}
	hub = mc.Lighthouse.Hosts[0]
	if mc.Tun.Dev != "" {
		return cfg, mc.Tun.Dev, hub, ""
	}
	out, err := pinTunDev(cfg, nebTunDev)
	if err != nil {
		return cfg, "", "", "mesh DNS disabled (" + err.Error() + ")"
	}
	return out, nebTunDev, hub, ""
}

// pinTunDev inserts `dev: <name>` into the config's tun block by text
// (a yaml round-trip would reflow the whole file, PEM blocks included).
// The result is parse-verified before it is trusted.
func pinTunDev(cfg []byte, dev string) ([]byte, error) {
	lines := strings.Split(string(cfg), "\n")
	for i, l := range lines {
		if strings.TrimRight(l, " ") != "tun:" {
			continue
		}
		indent := "    " // yaml.v3 default, matching the hub's renderer
		if i+1 < len(lines) {
			if t := strings.TrimLeft(lines[i+1], " "); t != "" && len(lines[i+1]) > len(t) {
				indent = lines[i+1][:len(lines[i+1])-len(t)]
			}
		}
		out := strings.Join(append(lines[:i+1:i+1], append([]string{indent + "dev: " + dev}, lines[i+1:]...)...), "\n")
		var mc meshConfYAML
		if err := yaml.Unmarshal([]byte(out), &mc); err != nil || mc.Tun.Dev != dev {
			return nil, fmt.Errorf("pinning tun device name failed to parse back")
		}
		return []byte(out), nil
	}
	return nil, fmt.Errorf("no tun block in the cached config")
}

// pointSplitDNS waits for the TUN device to exist, then routes the mesh
// zone — and only the mesh zone — to the hub's overlay resolver. No
// teardown: systemd-resolved drops per-link config with the link, so
// nebula's exit is the cleanup.
func pointSplitDNS(dev, hub, zone string) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.InterfaceByName(dev); err != nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		// sudo reuses the credential nebula's own sudo just cached.
		for _, args := range [][]string{
			{"resolvectl", "dns", dev, hub},
			{"resolvectl", "domain", dev, zone},
		} {
			if os.Geteuid() != 0 {
				args = append([]string{"sudo"}, args...)
			}
			if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
				log.Printf("split DNS: %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
				return
			}
		}
		log.Printf("split DNS: *.%s → %s on %s (rest of your DNS untouched)", zone, hub, dev)
		return
	}
	log.Printf("split DNS: %s never appeared — names under .%s will not resolve", dev, zone)
}

// haveResolvectl reports whether systemd-resolved's CLI is on PATH.
// Package var so tests can pin it.
var haveResolvectl = func() bool {
	_, err := exec.LookPath("resolvectl")
	return err == nil
}
