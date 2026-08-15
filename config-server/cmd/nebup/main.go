// Command nebup is the owner entrypoint to the nebula mesh. On first
// run it enrolls this device against the hub: the tool generates an
// X25519 keypair locally (the private key never leaves this disk),
// submits the pubkey with a requested name and group, an allowlisted
// wallet signs the v1 enrollment message (EIP-191 personal_sign — an
// ordinary auth message, NOT the fleet master message), and the hub
// returns a nebula config with pki.key left empty for nebup to splice
// in a reference to the sibling key file. Subsequent runs go straight
// to bringing nebula up.
//
// Two-file cache (ADR-0012):
//   - <name>.key — device-born identity. Survives -reenroll; only a
//     -rekey discards it. This is the whole membership credential; 0600.
//   - <name>.yml — disposable hub artifact (CA + cert + config). Any
//     -reenroll refreshes it.
//
// Enrollment mints no state on the hub: after a wipe, -rekey generates
// a fresh keypair and enrolls under the *same* name to keep the address
// stable. The hub does not remember that this device exists.
//
//	nebup                          # enroll if needed, then run nebula (foreground)
//	nebup -print                   # enroll if needed, print the config path, run nothing
//	nebup -reenroll                # discard the .yml artifact, re-sign, keep the .key
//	nebup -rekey                   # also discard the .key: new identity, new pubkey
//	nebup -group admins            # request admin group (default: admins)
//	nebup -paste                   # headless: paste the signature instead
//	nebup -dns off                 # no split DNS — mesh names won't resolve
//
// nebup sets up split DNS for the run: only *.mesh.internal is routed
// to the hub's overlay resolver, the rest of the machine's DNS is
// untouched.
//   - Linux (systemd-resolved): a per-link resolvectl config that dies
//     with the TUN device, so Ctrl-C is also the DNS teardown.
//   - macOS: an /etc/resolver/mesh.internal file. It outlives the TUN
//     device, so nebup removes it on exit — which is why the run absorbs
//     Ctrl-C (nebula still gets the same signal and shuts down) instead
//     of dying before the teardown runs.
//
// Other platforms fall back to overlay IPs (nebup -print).
//
// The approver at /status decides the *final* name and group, so what
// nebup sends is a proposal; the wallet signs whatever the approver
// ratified. In nebup direct (the wallet is local, requester = approver)
// the proposal and the signed values always agree.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"encoding/hex"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/devkey"
	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/walletsign"
)

func main() {
	log.SetFlags(0)
	var (
		hub      = flag.String("hub", "https://marnyg-talos-config.fly.dev", "hub base URL")
		name     = flag.String("name", "laptop", "device name (the approver may edit this on /status)")
		group    = flag.String("group", "admins", "requested group (admins|media); the approver decides the final value")
		reenroll = flag.Bool("reenroll", false, "discard the .yml artifact and re-sign with the SAME device key")
		rekey    = flag.Bool("rekey", false, "discard both the .key and the .yml: brand-new identity")
		printCfg = flag.Bool("print", false, "enroll if needed and print the config path instead of running nebula")
		paste    = flag.Bool("paste", false, "paste a signature instead of signing in the browser (headless)")
		dnsMode  = flag.String("dns", "auto", "mesh DNS: auto (split DNS for ."+nebderive.DNSZone+": resolvectl on Linux, /etc/resolver on macOS), off")
	)
	flag.Parse()

	dev := strings.ToLower(strings.TrimSpace(*name))
	if dev == "" {
		log.Fatal("-name must not be empty")
	}
	if *group != "admins" && *group != "media" {
		log.Fatalf("-group must be 'admins' or 'media', got %q", *group)
	}
	keyPath, cfgPath, err := cachePaths(dev)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		log.Fatal(err)
	}

	if *rekey {
		_ = os.Remove(keyPath)
		_ = os.Remove(cfgPath)
	}
	if *reenroll {
		_ = os.Remove(cfgPath)
	}

	if _, err := os.Stat(cfgPath); err != nil {
		priv, pub, err := loadOrCreateDeviceKey(keyPath)
		if err != nil {
			log.Fatal(err)
		}
		pubHex := hex.EncodeToString(pub[:])

		endpoint := strings.TrimRight(*hub, "/") + "/mesh/enroll"
		cfg, err := walletsign.MeshEnroll(endpoint, dev, *group, pubHex, "nebup", *paste)
		if err != nil {
			log.Fatal(err)
		}
		// The hub leaves pki.key empty; splice in an inline PEM of the
		// private key we just held onto. Kept self-contained (rather
		// than pointing at keyPath) so a printed config can still be
		// scp'd or QR'd to another host — the same portability property
		// the wg0 era had.
		spliced, err := devkey.SpliceKeyInline(cfg, priv)
		if err != nil {
			log.Fatalf("splicing device key into config: %v", err)
		}
		if err := os.WriteFile(cfgPath, spliced, 0o600); err != nil {
			log.Fatal(err)
		}
		// Enrolling under `sudo -E nebup` would leave the cache
		// root-owned, locking out a later non-root run. Hand it back to
		// the invoking user so both flows work; no-op when not under
		// sudo.
		if err := reownToSudoUser(filepath.Dir(cfgPath), keyPath, cfgPath); err != nil {
			log.Printf("warning: could not hand cache to invoking user (a later non-root nebup may re-enroll): %v", err)
		}
		log.Printf("enrolled %q (group requested: %s) — config cached at %s", dev, *group, cfgPath)
	}

	if *printCfg {
		fmt.Println(cfgPath)
		return
	}
	if err := runNebula(cfgPath, *dnsMode); err != nil {
		log.Fatal(err)
	}
}

// loadOrCreateDeviceKey reads keyPath if it exists, otherwise generates
// a fresh X25519 keypair and writes the private key (raw 32 bytes hex)
// to keyPath at 0600. Returns (priv, pub).
//
// Persisting the .key across -reenroll is the whole point of the two
// file cache: renewals reuse the same identity, so a device's address
// stays stable across ceremony repeats.
func loadOrCreateDeviceKey(keyPath string) (priv, pub [32]byte, err error) {
	if b, ferr := os.ReadFile(keyPath); ferr == nil {
		priv, pub, err = devkey.ParsePrivHex(string(b))
		if err != nil {
			return priv, pub, fmt.Errorf("%s is not a 32-byte hex X25519 private key (rerun with -rekey to regenerate)", keyPath)
		}
		return priv, pub, nil
	}
	if priv, pub, err = devkey.Generate(); err != nil {
		return priv, pub, err
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(priv[:])+"\n"), 0o600); err != nil {
		return priv, pub, fmt.Errorf("writing %s: %w", keyPath, err)
	}
	return priv, pub, nil
}

// cachePaths returns the two-file device cache paths:
// (<name>.key, <name>.yml) under ~/.config/talos-mesh/. The .key is
// device-born and persists across -reenroll; the .yml is hub-rendered
// and disposable.
func cachePaths(name string) (keyPath, cfgPath string, err error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("resolving config dir: %w", err)
	}
	base := filepath.Join(dir, "talos-mesh")
	return filepath.Join(base, name+".key"), filepath.Join(base, name+".yml"), nil
}

// reownToSudoUser hands paths back to the user behind a sudo invocation
// (SUDO_UID/SUDO_GID) so a cache written while root does not lock out a
// later non-root run. No-op when not running as root, or when running as
// genuine root rather than via sudo.
func reownToSudoUser(paths ...string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	uidStr, gidStr := os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID")
	if uidStr == "" || gidStr == "" {
		return nil
	}
	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return fmt.Errorf("SUDO_UID %q: %w", uidStr, err)
	}
	gid, err := strconv.Atoi(gidStr)
	if err != nil {
		return fmt.Errorf("SUDO_GID %q: %w", gidStr, err)
	}
	for _, p := range paths {
		if err := os.Chown(p, uid, gid); err != nil {
			return err
		}
	}
	return nil
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
	var stop func() // DNS teardown, nil when there is nothing to undo
	switch runtime.GOOS {
	case "linux":
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
	case "darwin":
		file, contents, note := darwinResolverPlan(raw, dnsMode, nebderive.DNSZone)
		if note != "" {
			log.Print(note)
		}
		if file != "" {
			if err := writeResolverFile(file, contents); err != nil {
				log.Printf("split DNS: %v", err)
			} else {
				stop = func() { removeResolverFile(file) }
			}
		}
	default:
		if dnsMode != "off" {
			log.Printf("mesh DNS disabled (split DNS unsupported on %s) — use overlay IPs (nebup -print; the address is in the cert)", runtime.GOOS)
		}
	}

	if stop != nil {
		defer stop()
		// Absorb Ctrl-C in nebup so the resolver-file teardown runs: the
		// same SIGINT still reaches nebula (shared process group), which
		// shuts down; cmd.Run returns; the deferred stop() cleans up.
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigs)
		go func() {
			for range sigs {
			}
		}()
	}
	// raw, not the linux-only adapted config: the tun-dev pin never
	// touches the static host map this reads.
	warnOverlayUnderlay(raw)

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
// hub's overlay address doubles as the mesh resolver, the host map
// carries its public endpoint) and writes (the TUN device name
// resolvectl needs as its handle).
type meshConfYAML struct {
	StaticHostMap map[string][]string `yaml:"static_host_map"`
	Lighthouse    struct {
		Hosts []string `yaml:"hosts"`
	} `yaml:"lighthouse"`
	Tun struct {
		Dev string `yaml:"dev"`
	} `yaml:"tun"`
}

// warnOverlayUnderlay checks which link would carry this machine's
// traffic to the hub's public endpoint and warns when that link is
// itself an overlay (Tailscale exit node, corporate VPN, …). Nebula
// happily uses such a link as underlay: everything hairpins through the
// VPN's exit, direct LAN paths are lost, and any punch measurement is
// invalid — this bit us twice while wg0 existed. A warning, not a
// refusal: full-tunnel VPN can be deliberate, and the mesh still works
// through it (relayed).
//
// Best-effort by design: no `ip` binary (Darwin), an unresolvable
// endpoint, or an unparsable route just skip the check — nebula's own
// errors cover those cases better.
func warnOverlayUnderlay(cfg []byte) {
	var mc meshConfYAML
	if err := yaml.Unmarshal(cfg, &mc); err != nil {
		return
	}
	for _, endpoints := range mc.StaticHostMap {
		for _, ep := range endpoints {
			host, _, err := net.SplitHostPort(ep)
			if err != nil {
				continue
			}
			ips, err := net.LookupIP(host)
			if err != nil || len(ips) == 0 {
				continue
			}
			ipBin, err := exec.LookPath("ip")
			if err != nil {
				return // no iproute2 (Darwin): skip the check
			}
			route, err := exec.Command(ipBin, "-o", "route", "get", ips[0].String()).Output()
			if err != nil {
				continue
			}
			dev := routeDev(string(route))
			if dev == "" {
				continue
			}
			detail, _ := exec.Command(ipBin, "-d", "-o", "link", "show", "dev", dev).Output()
			if overlayLink(dev, string(detail)) {
				log.Printf("WARNING: the route to the hub (%s → %s) goes over %q, which looks like another overlay (VPN/exit node).", ep, ips[0], dev)
				log.Printf("WARNING: nebula will use it as underlay — traffic hairpins through that tunnel and direct LAN paths are lost. Disconnect it for direct paths.")
			}
			return // one endpoint decides; the hub has exactly one
		}
	}
}

// routeDev extracts the outgoing device from `ip -o route get` output,
// e.g. "1.2.3.4 via 10.0.0.1 dev wlp3s0 src 10.0.0.42 uid 1000".
func routeDev(route string) string {
	fields := strings.Fields(route)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// overlayLink reports whether a link is itself a tunnel, judged by its
// `ip -d -o link show` detail (link kind: tun, wireguard, …) with the
// device name as a fallback for kinds iproute2 renders unhelpfully.
func overlayLink(dev, detail string) bool {
	for _, kind := range []string{" tun ", " tap ", " wireguard ", " ipip ", " gre ", " vti ", " ip6tnl ", " ppp "} {
		if strings.Contains(detail, kind) {
			return true
		}
	}
	for _, prefix := range []string{"tun", "tap", "wg", "tailscale", "nebula", "utun", "zt", "ppp", "vpn"} {
		if strings.HasPrefix(dev, prefix) {
			return true
		}
	}
	return false
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

// meshResolver reads the hub's overlay resolver address out of the
// cached config (its lighthouse address doubles as the mesh resolver).
// Pure. Returns ("", note) when the config carries no lighthouse host.
func meshResolver(cfg []byte) (hub, note string) {
	var mc meshConfYAML
	if err := yaml.Unmarshal(cfg, &mc); err != nil || len(mc.Lighthouse.Hosts) == 0 {
		return "", "mesh DNS disabled (no lighthouse address in the cached config — nebup -reenroll to refresh it)"
	}
	return mc.Lighthouse.Hosts[0], ""
}

// darwinResolverPlan decides the macOS /etc/resolver split-DNS action
// for this run. Pure. Returns the target file, its contents, and a user
// note. An empty file path means no split DNS (mode off, or the config
// names no resolver). No TUN device is pinned: a resolver file points
// at the hub's overlay IP, not a link, and Darwin only accepts utun.
func darwinResolverPlan(cfg []byte, mode, zone string) (file, contents, note string) {
	if mode == "off" {
		return "", "", ""
	}
	hub, note := meshResolver(cfg)
	if hub == "" {
		return "", "", note
	}
	file = filepath.Join("/etc/resolver", zone)
	contents = "# talos-mesh split DNS — managed by nebup, removed on exit\nnameserver " + hub + "\n"
	note = fmt.Sprintf("split DNS: *.%s → %s via %s (removed on exit)", zone, hub, file)
	return file, contents, note
}

// writeResolverFile installs the macOS resolver file, using sudo when
// nebup is not already root (same credential nebula's own sudo caches),
// then flushes the DNS cache so mDNSResponder picks it up.
func writeResolverFile(path, contents string) error {
	dir := filepath.Dir(path)
	if os.Geteuid() == 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	} else {
		if out, err := exec.Command("sudo", "mkdir", "-p", dir).CombinedOutput(); err != nil {
			return fmt.Errorf("mkdir %s: %v: %s", dir, err, strings.TrimSpace(string(out)))
		}
		c := exec.Command("sudo", "tee", path)
		c.Stdin = strings.NewReader(contents)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("write %s: %v: %s", path, err, strings.TrimSpace(string(out)))
		}
	}
	flushDarwinDNS()
	return nil
}

// removeResolverFile is the exit teardown: drop the resolver file and
// flush the cache. Best-effort — a leftover file would only re-route a
// dead resolver, which fails closed.
func removeResolverFile(path string) {
	var err error
	if os.Geteuid() == 0 {
		err = os.Remove(path)
	} else {
		err = exec.Command("sudo", "rm", "-f", path).Run()
	}
	if err != nil {
		log.Printf("split DNS: removing %s: %v", path, err)
		return
	}
	flushDarwinDNS()
	log.Printf("split DNS: removed %s", path)
}

// flushDarwinDNS nudges mDNSResponder to re-read /etc/resolver.
// Best-effort: modern macOS re-reads on change, and a stale cache only
// delays resolution briefly.
func flushDarwinDNS() {
	for _, args := range [][]string{
		{"dscacheutil", "-flushcache"},
		{"killall", "-HUP", "mDNSResponder"},
	} {
		if os.Geteuid() != 0 {
			args = append([]string{"sudo"}, args...)
		}
		_ = exec.Command(args[0], args[1:]...).Run()
	}
}
