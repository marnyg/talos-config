//go:build iroh && darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/fakeip"
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

// serveTun runs the netstack over the utun until ctx ends or the tun
// path breaks. The Directory is the agent's name map read by the zone
// rule (nodeagent.Zone): a member name resolves to a fake IP only
// while the map has a live entry for it, a service name `<svc>.<m>`
// only while m advertises a gateway facet — so a name still owned by
// nebula (jackett.cp1) is forwarded, not shadowed. A name the map
// lacks kicks a beat (rate-limited): a member enrolled since the last
// one resolves on the next query instead of the next beat.
func serveTun(ctx context.Context, t *tunSetup, a *nodeagent.Agent, pool *connPool, upstream string, logger *log.Logger) error {
	if os.Geteuid() == 0 {
		return errors.New("serveTun as root: privilegedSetup must run first")
	}
	res, err := fakeip.NewResolver(fakeip.ResolverOptions{
		Directory: fakeip.DirectoryFunc(func(name string) bool {
			_, _, err := a.Zone(name)
			if errors.Is(err, nodeagent.ErrUnknownName) {
				a.Kick()
			}
			return err == nil
		}),
		Upstreams: upstream,
	})
	if err != nil {
		return err
	}
	link, err := fakeip.NewTunLink(t.dev, 512)
	if err != nil {
		return err
	}
	flow := func(app fakeip.Conn, dst netip.AddrPort) {
		defer app.Close()
		name, ok := res.NameFor(dst.Addr())
		if !ok {
			logger.Printf("tun: flow to %s: not a name we minted", dst)
			return
		}
		// Which vocabulary a port is read in depends on who the name
		// is: hub.<zone>:80 is hub-http, <svc>.gw:80 is ingress-http,
		// cp1:50000 is apid (nodeagent.Target).
		member, facet, err := a.Target(name, dst.Port())
		if err != nil {
			logger.Printf("tun: flow to %s (%s): %v", dst, name, err)
			return
		}
		raw, err := pool.open(ctx, member, facet)
		if err != nil {
			logger.Printf("tun: %s/%s: %v", member, facet, err)
			return
		}
		t0 := time.Now()
		in, out := pipe(raw, app)
		logger.Printf("tun: %s/%s (%s): stream done: %dB in, %dB out, %s", member, facet, name, in, out, time.Since(t0).Round(time.Millisecond))
	}
	s, err := fakeip.NewStack(link, flow, res.HandleUDP)
	if err != nil {
		link.Close()
		return err
	}
	defer func() { link.Close(); s.Close() }()
	logger.Printf("tun: resolver on %s:53 for *.%s, upstream %q", fakeip.ResolverIP, fakeip.Zone, upstream)

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
