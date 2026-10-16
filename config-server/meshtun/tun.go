//go:build iroh

package meshtun

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/config-server/fakeip"
	"github.com/marnyg/talos-config/config-server/meshtls"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/protocol/facethttp"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// HTTPSPort is the one port of the fiction that is not a facet's
// natural port: a flow to <fake IP>:443 is terminated on the device
// (meshtls, ADR-0033) and rides the same facet a flow to :80 would —
// ingress-http on a gateway, hub-http on the hub. Presentation only:
// no kind advertises it, no policy row names it.
const HTTPSPort uint16 = 443

// httpPort is the natural port the TLS fiction stands in front of.
const httpPort uint16 = 80

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
	// TLS is the device's presentation root; nil ⇒ no :443, flows
	// there are refused as any non-facet port is.
	TLS *meshtls.Authority
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
	Stack    *fakeip.Stack // its Stats: what never reached a flow or the resolver
	Stats    Stats
	// Front is the :443 terminator, nil without Options.TLS.
	Front *meshtls.Front

	ctx    context.Context
	cancel context.CancelFunc
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
	if o.TLS != nil {
		front, err := meshtls.NewFront(meshtls.FrontOptions{
			Authority: o.TLS,
			Resolve: func(fqdn string) (meshtls.Target, error) {
				bare, ok := fakeip.InZone(fqdn)
				if !ok {
					return meshtls.Target{}, fmt.Errorf("%w: %q", nodeagent.ErrUnknownName, fqdn)
				}
				member, facet, err := a.Target(bare, httpPort)
				if err != nil {
					return meshtls.Target{}, err
				}
				return meshtls.Target{Member: member, Facet: facet}, nil
			},
			Open: func(ctx context.Context, tg meshtls.Target) (net.Conn, error) {
				raw, err := pool.Open(ctx, tg.Member, tg.Facet)
				if err != nil {
					return nil, err
				}
				return facethttp.Dialed(raw, tg.Facet, ""), nil
			},
			Log: logger,
		})
		if err != nil {
			cancel()
			return nil, err
		}
		t.Front = front
	}
	s, err := fakeip.NewStack(o.Link, t.flow, res.HandleUDP)
	if err != nil {
		cancel()
		if t.Front != nil {
			t.Front.Close()
		}
		return nil, err
	}
	t.Stack = s
	logger.Printf("tun: resolver on %s:53 for *.%s, upstream %q", fakeip.ResolverIP, fakeip.Zone, o.Upstreams)
	if t.Front != nil {
		logger.Printf("tun: https on <fake IP>:%d for *.%s, root %s", HTTPSPort, fakeip.Zone, o.TLS.RootPath())
	}
	return t, nil
}

// flow runs one accepted TCP flow: which vocabulary a port is read in
// depends on who the name is — hub.<zone>:80 is hub-http, <svc>.gw:80
// is ingress-http, cp1:50000 is apid (nodeagent.Target). :443 is the
// TLS fiction in front of the :80 facet (https).
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
	if dst.Port() == HTTPSPort && t.Front != nil {
		t.https(app, name)
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

// https hands one :443 flow to the front: TLS terminated here with a
// leaf for the name, requests proxied as plain HTTP over the stream to
// the member's HTTP facet (the one :80 would ride), X-Forwarded-Proto:
// https added. The front owns app once Serve has taken it.
func (t *Tun) https(app fakeip.Conn, name string) {
	fqdn := name + "." + strings.TrimSuffix(fakeip.Zone, ".")
	t0 := time.Now()
	tg, err := t.Front.Serve(t.ctx, app, fqdn)
	if err != nil {
		t.Stats.FlowErrors.Add(1)
		t.log.Printf("tun: https %s: %v", fqdn, err)
		return // flow's deferred Close ends it
	}
	t.log.Printf("tun: https %s/%s (%s): connection done, %s", tg.Member, tg.Facet, fqdn, time.Since(t0).Round(time.Millisecond))
}

// Close stops serving: flows in progress are cancelled, the stack is
// stopped (close the link first), the pool's connections dropped.
func (t *Tun) Close() {
	t.cancel()
	t.Stack.Close()
	if t.Front != nil {
		t.Front.Close()
	}
	t.Pool.Close()
}
