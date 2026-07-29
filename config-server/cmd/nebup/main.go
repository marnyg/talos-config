// Command nebup is the owner entrypoint to the nebula mesh — the wgup
// pattern, one overlay over. On first run it enrolls this device against
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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	if err := runNebula(path); err != nil {
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
func runNebula(path string) error {
	bin, err := exec.LookPath("nebula")
	if err != nil {
		return fmt.Errorf("nebula not found on PATH (it is in this repo's devshell): %w", err)
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
