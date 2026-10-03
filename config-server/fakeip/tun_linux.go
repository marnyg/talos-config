//go:build linux && !android

// Android holds its tun as an fd from VpnService (fdlink_linux.go);
// the desktop setup below is for a host with ip(8) and root.

package fakeip

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wireguard/tun"
)

// LinuxTunName is the interface the linux desktop daemon creates. Fixed
// (not kernel-allocated like darwin's utunN) so a systemd unit, a
// firewall rule or a resolvectl call can name it ahead of time.
const LinuxTunName = "talosmesh0"

// Setup is the linux half of the desktop daemon's privileged step
// (darwin: utun_darwin.go, decision fgr): runs as root, takes no
// network input, and everything after it runs unprivileged. Creates
// the tun, assigns TunIP, routes FakeRange into it, and — where
// systemd-resolved is present — points the zone at ResolverIP as a
// per-link setting, so split DNS lives and dies with the interface
// (the linux stand-in for darwin's static /etc/resolver/mesh.internal).
// Without resolved the tun still works by IP; the caller is told to
// wire the zone to ResolverIP itself.
//
// ip(8) is exec'd rather than reimplemented over netlink; same choice
// as the darwin half (ifconfig/route) and Tailscale's router_linux.
func Setup(mtu int) (dev tun.Device, name string, err error) {
	ipBin, err := exec.LookPath("ip")
	if err != nil {
		return nil, "", errors.New("fakeip: ip(8) not on PATH (iproute2)")
	}
	dev, err = tun.CreateTUN(LinuxTunName, mtu)
	if err != nil {
		return nil, "", fmt.Errorf("create tun %s: %w", LinuxTunName, err)
	}
	name, err = dev.Name()
	if err != nil {
		_ = dev.Close()
		return nil, "", fmt.Errorf("tun name: %w", err)
	}
	for _, args := range [][]string{
		{"addr", "add", TunIP + "/32", "dev", name},
		{"link", "set", name, "up"},
		{"route", "add", FakeRange, "dev", name},
	} {
		if err := run(ipBin, args...); err != nil {
			_ = dev.Close()
			return nil, "", err
		}
	}
	if err := setupResolved(name); err != nil {
		_ = dev.Close()
		return nil, "", err
	}
	return dev, name, nil
}

// setupResolved declares the zone on the link in systemd-resolved: all
// queries under mesh.internal go to ResolverIP (which answers inside
// the tun), nothing else is touched. A routing-only domain (~zone) so
// the link never becomes a default resolver. No resolvectl → not an
// error: the zone must then be wired by hand (HasResolved lets the
// caller say so).
func setupResolved(ifname string) error {
	rc, err := exec.LookPath("resolvectl")
	if err != nil {
		return nil
	}
	zone := strings.TrimSuffix(Zone, ".")
	if err := run(rc, "dns", ifname, ResolverIP); err != nil {
		return err
	}
	return run(rc, "domain", ifname, "~"+zone)
}

// HasResolved reports whether Setup could declare the zone (systemd-
// resolved's resolvectl on PATH). False means the operator must point
// Zone at ResolverIP in whatever resolver the host runs.
func HasResolved() bool {
	_, err := exec.LookPath("resolvectl")
	return err == nil
}

// RouteIntact reports whether FakeRange still routes via ifname — a
// network manager may flush routes on a transition; the dropped daemon
// cannot re-add, so it exits and systemd restarts it as root.
func RouteIntact(ifname string) bool {
	ipBin, err := exec.LookPath("ip")
	if err != nil {
		return false
	}
	out, err := exec.Command(ipBin, "-4", "route", "show", FakeRange).CombinedOutput()
	if err != nil {
		return false
	}
	// "198.18.0.0/15 dev talosmesh0 scope link" — one line per route.
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == FakeRange && f[1] == "dev" && f[2] == ifname {
			return true
		}
	}
	return false
}

func run(bin string, args ...string) error {
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
