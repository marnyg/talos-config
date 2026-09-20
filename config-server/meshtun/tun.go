//go:build iroh

package meshtun

import (
	"context"
	"errors"
	"log"
	"net/netip"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/fakeip"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// Options configures Start.
type Options struct {
	// Agent is the member whose directory gates the resolver and whose
	// Dial the flows ride.
	Agent *nodeagent.Agent
	// Link is the tun as a gvisor link: fakeip.NewTunLink (a
	// wireguard-go tun.Device, darwin) or fakeip.NewFDLink (an fd,
	// android). The caller owns it: close it before Tun.Close.
	Link stack.LinkEndpoint
	// Pool to open streams on; nil ⇒ a fresh one over Agent.
	Pool *Pool
	// Upstreams is where mesh.internal names NOT in the name map, and
	// every out-of-zone name, go (fakeip.ResolverOptions.Upstreams);
	// "" ⇒ NXDOMAIN in zone, drop out of zone.
	Upstreams string
	// DialControl runs on every upstream DNS socket (mobile:
	// VpnService.protect so the forwarder cannot loop into the tun).
	DialControl func(network, address string, c syscall.RawConn) error
	// Log; nil ⇒ log.Default().
	Log *log.Logger
}

// Stats are the presentation's counters, for a status surface.
type Stats struct {
	Flows      atomic.Int64
	FlowsOpen  atomic.Int64
	FlowErrors atomic.Int64
	// Byte counters move while a flow is running (see Pipe), so a
	// long-lived stream shows up in them before it ends.
	BytesIn  atomic.Int64 // member → client
	BytesOut atomic.Int64 // client → member
}

// Tun is a running presentation.
type Tun struct {
	Resolver *fakeip.Resolver
	Pool     *Pool
	Stats    Stats

	ctx    context.Context
	cancel context.CancelFunc
	stack  *fakeip.Stack
	log    *log.Logger
}

// Start builds the resolver and the stack over o.Link and serves flows
// until Close. The Directory is the agent's name map read by the zone
// rule (nodeagent.Zone): a member name resolves to a fake IP only while
// the map has a live entry for it, a service name `<svc>.<m>` only
// while m advertises a gateway facet — so a name still owned by nebula
// (jackett.cp1) is forwarded, not shadowed. A name the map lacks kicks
// a beat (rate-limited): a member enrolled since the last one resolves
// on the next query instead of the next beat.
func Start(o Options) (*Tun, error) {
	if o.Agent == nil || o.Link == nil {
		return nil, errors.New("meshtun: Agent and Link are required")
	}
	logger := o.Log
	if logger == nil {
		logger = log.Default()
	}
	a := o.Agent
	res, err := fakeip.NewResolver(fakeip.ResolverOptions{
		Directory: fakeip.DirectoryFunc(func(name string) bool {
			_, _, err := a.Zone(name)
			if errors.Is(err, nodeagent.ErrUnknownName) {
				a.Kick()
			}
			return err == nil
		}),
		Upstreams:   o.Upstreams,
		DialControl: o.DialControl,
	})
	if err != nil {
		return nil, err
	}
	pool := o.Pool
	if pool == nil {
		pool = NewPool(a, logger)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Tun{Resolver: res, Pool: pool, ctx: ctx, cancel: cancel, log: logger}
	s, err := fakeip.NewStack(o.Link, t.flow, res.HandleUDP)
	if err != nil {
		cancel()
		return nil, err
	}
	t.stack = s
	logger.Printf("tun: resolver on %s:53 for *.%s, upstream %q", fakeip.ResolverIP, fakeip.Zone, o.Upstreams)
	return t, nil
}

// flow runs one accepted TCP flow: which vocabulary a port is read in
// depends on who the name is — hub.<zone>:80 is hub-http, <svc>.gw:80
// is ingress-http, cp1:50000 is apid (nodeagent.Target).
func (t *Tun) flow(app fakeip.Conn, dst netip.AddrPort) {
	t.Stats.Flows.Add(1)
	t.Stats.FlowsOpen.Add(1)
	defer t.Stats.FlowsOpen.Add(-1)
	defer app.Close()
	name, ok := t.Resolver.NameFor(dst.Addr())
	if !ok {
		t.Stats.FlowErrors.Add(1)
		t.log.Printf("tun: flow to %s: not a name we minted", dst)
		return
	}
	member, facet, err := t.Pool.a.Target(name, dst.Port())
	if err != nil {
		t.Stats.FlowErrors.Add(1)
		t.log.Printf("tun: flow to %s (%s): %v", dst, name, err)
		return
	}
	raw, err := t.Pool.Open(t.ctx, member, facet)
	if err != nil {
		t.Stats.FlowErrors.Add(1)
		t.log.Printf("tun: %s/%s: %v", member, facet, err)
		return
	}
	t0 := time.Now()
	// The counters are handed to Pipe rather than added after it
	// returns: a flow that lives for a whole movie would otherwise
	// read as zero throughput on the status surface until it closed.
	in, out := Pipe(raw, app, &Counters{In: &t.Stats.BytesIn, Out: &t.Stats.BytesOut})
	t.log.Printf("tun: %s/%s (%s): stream done: %dB in, %dB out, %s", member, facet, name, in, out, time.Since(t0).Round(time.Millisecond))
}

// Close stops serving: flows in progress are cancelled, the stack is
// stopped (close the link first), the pool's connections dropped.
func (t *Tun) Close() {
	t.cancel()
	t.stack.Close()
	t.Pool.Close()
}
