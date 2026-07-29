// Command wgup is the admin entrypoint to the WireGuard control
// channel — `tailscale up`, wallet edition. On first run it enrolls
// this device against the config server: the server issues a
// single-use challenge, the admin signs it with an allowlisted wallet
// (EIP-191 personal_sign — an ordinary auth message, NOT the fleet
// master message), and the server returns the derived wg-quick config,
// which is cached locally (0600) and brought up with wg-quick.
// Subsequent runs skip straight to wg-quick.
//
// Signing defaults to the browser: wgup serves a one-shot page on
// 127.0.0.1, opens it, and the in-page wallet (e.g. MetaMask) signs
// the challenge and posts the signature back — nothing to copy.
// -paste falls back to pasting a signature (e.g. cast wallet sign)
// for headless machines.
//
//	wgup                       # enroll if needed, then wg-quick up
//	wgup -down                 # tear the tunnel down
//	wgup -reenroll             # discard the cached config, enroll again
//	wgup -print                # enroll if needed, print config path, don't bring up
//	wgup -name phone -print    # enroll another declared device
//	wgup -paste                # headless: paste the signature instead
//
// Offline fallback (server unreachable, or you hold the master
// signature anyway): wgping -admin <name> -sig <sig> -wgquick.
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
		server   = flag.String("server", "https://marnyg-talos-config.fly.dev", "config server base URL")
		name     = flag.String("name", "laptop", "device name (must be declared in the server's WG_ADMIN_PEERS)")
		down     = flag.Bool("down", false, "tear the tunnel down")
		reenroll = flag.Bool("reenroll", false, "discard the cached config and enroll again")
		print    = flag.Bool("print", false, "enroll if needed and print the config path instead of bringing the tunnel up")
		paste    = flag.Bool("paste", false, "paste a signature instead of signing in the browser (headless)")
		dnsMode  = flag.String("dns", "auto", "tunnel DNS: auto (split DNS via resolvectl when available, else none), keep (wg-quick DNS= line — routes ALL queries through the hub), off")
	)
	flag.Parse()

	dev := strings.ToLower(strings.TrimSpace(*name))
	path, err := confPath(dev)
	if err != nil {
		log.Fatal(err)
	}

	if *down {
		if _, err := os.Stat(path); err != nil {
			log.Fatalf("no cached config at %s — nothing to tear down", path)
		}
		if err := wgQuick("down", path); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *reenroll {
		_ = os.Remove(path)
	}
	if _, err := os.Stat(path); err != nil {
		cfg, err := enroll(strings.TrimRight(*server, "/"), dev, *paste)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
			log.Fatal(err)
		}
		log.Printf("enrolled %q — config cached at %s", dev, path)
	}

	// Adapt the DNS line for this machine — never on -down, so the
	// teardown always matches the config the tunnel came up with.
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	adapted, note := adaptDNS(string(raw), *dnsMode, haveResolvectl())
	if note != "" {
		log.Print(note)
	}
	if adapted != string(raw) {
		if err := os.WriteFile(path, []byte(adapted), 0o600); err != nil {
			log.Fatal(err)
		}
	}

	if *print {
		fmt.Println(path)
		return
	}
	if err := wgQuick("up", path); err != nil {
		log.Fatal(err)
	}
	log.Printf("tunnel up — hub at hub.talos.wg (10.99.0.1); wgup -down to disconnect")
}

// confPath is where the device's wg-quick config is cached. The file
// name doubles as the interface name (wg-quick), so it is kept short.
func confPath(name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return filepath.Join(dir, "talos-wg", "talos-"+name+".conf"), nil
}

// enroll runs the challenge → wallet signature → config exchange
// (walletsign, shared with nebup) and returns the wg-quick config.
func enroll(server, name string, paste bool) (string, error) {
	cfg, err := walletsign.Enroll(server+"/wg/enroll", name, "wgup", paste)
	return string(cfg), err
}

// adaptDNS rewrites the server-rendered `DNS = <hub>, <domain>` line
// for this machine. wg-quick implements DNS= exclusively (resolvconf
// -x): it hijacks ALL name resolution into the hub, which only
// answers the tunnel zone — breaking normal networking, and breaking
// it completely while the server is sealed. Split DNS via
// systemd-resolved routes only the tunnel domain through the tunnel.
// Idempotent: an already-adapted config passes through unchanged.
func adaptDNS(cfg, mode string, haveResolvectl bool) (out, note string) {
	lines := strings.Split(cfg, "\n")
	idx := -1
	var hub, domain string
	for i, l := range lines {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "DNS ="); ok {
			if ip, dom, found := strings.Cut(v, ","); found {
				hub, domain = strings.TrimSpace(ip), strings.TrimSpace(dom)
			} else {
				hub = strings.TrimSpace(v)
			}
			idx = i
			break
		}
	}
	if idx == -1 || mode == "keep" {
		return cfg, ""
	}
	if mode == "auto" && haveResolvectl && domain != "" {
		lines[idx] = fmt.Sprintf("PostUp = resolvectl dns %%i %s; resolvectl domain %%i %s", hub, domain)
		return strings.Join(lines, "\n"),
			fmt.Sprintf("split DNS: only *.%s resolves via the hub (%s); the rest of your DNS is untouched", domain, hub)
	}
	// off, or auto without a split-DNS-capable resolver: no tunnel DNS.
	out = strings.Join(append(lines[:idx], lines[idx+1:]...), "\n")
	if mode != "off" {
		note = "tunnel DNS disabled (no resolvectl for split DNS) — use tunnel IPs, or -dns keep to route ALL DNS through the hub"
	}
	return out, note
}

// haveResolvectl reports whether systemd-resolved's CLI is on PATH.
// Package var so tests can pin it.
var haveResolvectl = func() bool {
	_, err := exec.LookPath("resolvectl")
	return err == nil
}

// wgQuick runs wg-quick, via sudo when not already root.
func wgQuick(verb, path string) error {
	argv := []string{"wg-quick", verb, path}
	if os.Geteuid() != 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
