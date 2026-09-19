package fakeip

import (
	"fmt"
	"net/netip"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wireguard/tun"
)

// Setup is the privileged step of the desktop daemon (decision fgr): it
// runs as root, takes no network input, and everything after it runs
// unprivileged. Creates a utun, assigns TunIP as a point-to-point
// address, routes FakeRange into it. That is the whole of what needs
// root: DNS is /etc/resolver/mesh.internal, declared statically by
// nix-darwin; the resolver itself answers on ResolverIP inside the tun.
//
// ifconfig/route are exec'd at their fixed paths rather than reimplemented
// over SIOCAIFADDR and the routing socket; same choice as Tailscale's
// router_darwin.
func Setup(mtu int) (dev tun.Device, name string, err error) {
	dev, err = tun.CreateTUN("utun", mtu)
	if err != nil {
		return nil, "", fmt.Errorf("create utun: %w", err)
	}
	name, err = dev.Name()
	if err != nil {
		_ = dev.Close()
		return nil, "", fmt.Errorf("utun name: %w", err)
	}
	ip := netip.MustParseAddr(TunIP)
	if err := run("/sbin/ifconfig", name, "inet", ip.String(), ip.String(), "netmask", "255.255.255.255", "up"); err != nil {
		_ = dev.Close()
		return nil, "", err
	}
	if err := run("/sbin/route", "-q", "-n", "add", "-inet", FakeRange, "-interface", name); err != nil {
		_ = dev.Close()
		return nil, "", err
	}
	return dev, name, nil
}

// RouteIntact reports whether FakeRange still routes via ifname. configd
// may flush interface routes on a network transition; the dropped
// daemon cannot re-add, so it exits and launchd restarts it as root.
//
// Asked as -net <prefix>: the host form (`route get 198.18.1.0`)
// answers with the default route for .0 addresses on macOS even while
// the /15 is installed and forwarding (seen 2026-09-19, Darwin 25.6).
// A missing route answers with destination: default, so both lines are
// checked.
func RouteIntact(ifname string) bool {
	out, err := exec.Command("/sbin/route", "-n", "get", "-inet", "-net", FakeRange).CombinedOutput()
	if err != nil {
		return false
	}
	wantDst := strings.TrimSuffix(FakeRange, "/15")
	dst, iface := "", ""
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "destination:":
			dst = f[1]
		case "interface:":
			iface = f[1]
		}
	}
	return dst == wantDst && iface == ifname
}

func run(bin string, args ...string) error {
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
