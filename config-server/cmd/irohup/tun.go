//go:build iroh && (darwin || (linux && !android))

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/fakeip"
	"github.com/marnyg/talos-config/config-server/meshtls"
	"github.com/marnyg/talos-config/config-server/meshtun"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"golang.zx2c4.com/wireguard/tun"
)

// tlsDir is the presentation root's home under the state dir: the key
// beside the member key (same custody), the cert world-readable for
// the trust-store install (ADR-0033).
const tlsDir = "tls"

// The desktop presentation (359.9.6, decision fgr): one process,
// launched as root (launchd on darwin, systemd on linux), that does
// its privileged work in privilegedSetup and nothing else as root.
// The OS halves live in fakeip (utun_darwin.go / tun_linux.go); what
// is here is the same on both. The fiction is <fake IP>:<port> with the
// facet's natural port (policy.FacetPort), so talosconfig/kubeconfig
// endpoints read as cp1.mesh.internal:50000 / :6443, nothing renumbered.

const (
	tunMTU        = 1500
	routeCheckEvy = 10 * time.Second
)

// tunSetup is what privilegedSetup hands to the unprivileged rest.
type tunSetup struct {
	dev    tun.Device
	ifname string
}

// privilegedSetup is the one function that runs as root. It takes no
// network input: create the utun and route the fake range into it
// (fakeip.Setup), mint the device's HTTPS root if there is none and
// put it in the system trust store (trustRoot; ADR-0033 — the root is
// this device's presentation artifact, so the daemon that owns it is
// the right party to trust it, once), make sure the state dir exists
// and belongs to the service user, then drop to that user for good.
// After it returns, euid != 0 is asserted; anything privileged added
// later fails here at startup rather than silently in production.
func privilegedSetup(stateDir, runAs, label string) (*tunSetup, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("-tun needs root at start (launchd/systemd, or sudo); it drops to -user after the tun is up")
	}
	u, err := user.Lookup(runAs)
	if err != nil {
		return nil, fmt.Errorf("-user %q: %w (declare the service user: nix-darwin users.users + users.knownUsers, or NixOS users.users.<name>.isSystemUser)", runAs, err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if uid == 0 {
		return nil, errors.New("-user must not be root")
	}

	dev, ifname, err := fakeip.Setup(tunMTU)
	if err != nil {
		return nil, err
	}
	// The state dir is the service user's, not root's: the beat writes
	// renewed certs into it, and the key in it is what the login user
	// must not read (decision fgr, constraint a).
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		_ = dev.Close()
		return nil, err
	}
	// The root is minted here (as root, chowned below with the rest)
	// so the trust-store install can happen in the same privileged
	// breath; serveTun reloads it as the service user.
	auth, err := meshtls.Load(filepath.Join(stateDir, tlsDir), label)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	if err := trustRoot(auth.RootPath()); err != nil {
		// Not fatal: https warns until the user does it by hand (the
		// note says how); everything else works.
		log.Printf("https: could not trust the device root automatically: %v", err)
		trustNote(auth.RootPath(), log.Default())
	}
	if err := chownTree(stateDir, uid, gid); err != nil {
		_ = dev.Close()
		return nil, err
	}
	if err := chownTree(filepath.Dir(stateDir), uid, gid); err != nil { // nebula files live beside it
		_ = dev.Close()
		return nil, err
	}

	// Setuid on darwin is process-wide (XNU keeps credentials on the
	// proc); verified empirically 2026-09-19, see fgr. On linux
	// credentials are per thread, and Go's syscall.Setuid applies the
	// call to every thread (AllThreadsSyscall, Go 1.16+), so the same
	// three calls drop the whole process there too.
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return nil, fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return nil, fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return nil, fmt.Errorf("setuid: %w", err)
	}
	if os.Geteuid() == 0 || os.Getuid() == 0 {
		return nil, errors.New("still root after the drop; refusing to start the network side")
	}
	if err := syscall.Setuid(0); err == nil {
		return nil, errors.New("could regain root after the drop; refusing to start")
	}
	log.Printf("tun %s up: %s/32, %s routed; running as %s (uid %d)", ifname, fakeip.TunIP, fakeip.FakeRange, runAs, uid)
	dnsNote(ifname)
	return &tunSetup{dev: dev, ifname: ifname}, nil
}

// chownTree makes dir and everything under it the user's. Only the
// top level is ever more than a few files.
func chownTree(dir string, uid, gid int) error {
	return filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// serveTun runs the presentation (meshtun) over the utun until ctx
// ends or the tun path breaks. The device root for :443 was minted
// (and trusted) by privilegedSetup; this reloads it unprivileged.
func serveTun(ctx context.Context, t *tunSetup, a *nodeagent.Agent, pool *meshtun.Pool, stateDir, label string, logger *log.Logger) error {
	if os.Geteuid() == 0 {
		return errors.New("serveTun as root: privilegedSetup must run first")
	}
	auth, err := meshtls.Load(filepath.Join(stateDir, tlsDir), label)
	if err != nil {
		return err
	}
	link, err := fakeip.NewTunLink(t.dev, 512)
	if err != nil {
		return err
	}
	mt, err := meshtun.Start(meshtun.Options{Agent: a, Link: link, Pool: pool, TLS: auth, Log: logger})
	if err != nil {
		link.Close()
		return err
	}
	defer func() { link.Close(); mt.Close() }()

	tick := time.NewTicker(routeCheckEvy)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-link.Done():
			return fmt.Errorf("tun %s: %w", t.ifname, link.Err())
		case <-tick.C:
			if !fakeip.RouteIntact(t.ifname) {
				return fmt.Errorf("route %s via %s is gone (network transition?)", fakeip.FakeRange, t.ifname)
			}
		}
	}
}
