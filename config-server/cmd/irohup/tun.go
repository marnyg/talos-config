//go:build iroh && darwin

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
	"github.com/marnyg/talos-config/config-server/meshtun"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"golang.zx2c4.com/wireguard/tun"
)

// The desktop presentation (359.9.6, decision fgr): one process,
// launched as root, that does its privileged work in privilegedSetup
// and nothing else as root. The fiction is <fake IP>:<port> with the
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
// (fakeip.Setup), make sure the state dir exists and belongs to the
// service user, then drop to that user for good. After it returns,
// euid != 0 is asserted; anything privileged added later fails here at
// startup rather than silently in production.
func privilegedSetup(stateDir, runAs string) (*tunSetup, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("-tun needs root at start (launchd, or sudo); it drops to -user after the utun is up")
	}
	u, err := user.Lookup(runAs)
	if err != nil {
		return nil, fmt.Errorf("-user %q: %w (nix-darwin users.users + users.knownUsers)", runAs, err)
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
	if err := chownTree(stateDir, uid, gid); err != nil {
		_ = dev.Close()
		return nil, err
	}
	if err := chownTree(filepath.Dir(stateDir), uid, gid); err != nil { // nebula files live beside it
		_ = dev.Close()
		return nil, err
	}

	// Setuid on darwin is process-wide (XNU keeps credentials on the
	// proc, unlike linux); verified empirically 2026-09-19, see fgr.
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
// ends or the tun path breaks.
func serveTun(ctx context.Context, t *tunSetup, a *nodeagent.Agent, pool *meshtun.Pool, logger *log.Logger) error {
	if os.Geteuid() == 0 {
		return errors.New("serveTun as root: privilegedSetup must run first")
	}
	link, err := fakeip.NewTunLink(t.dev, 512)
	if err != nil {
		return err
	}
	mt, err := meshtun.Start(meshtun.Options{Agent: a, Link: link, Pool: pool, Log: logger})
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
