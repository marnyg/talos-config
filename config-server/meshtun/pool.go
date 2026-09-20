//go:build iroh

// Package meshtun binds the fake-IP fiction (fakeip) to a member
// (nodeagent): the presentation every IP-speaking client of the
// identity plane gets, on a desktop utun (cmd/irohup -tun) or an
// Android VpnService (mobile). One shape, two links:
//
//	tun → fakeip.Stack → { UDP/53: fakeip.Resolver gated by the zone rule (nodeagent.Zone);
//	                       TCP to <fake IP>:<port>: nodeagent.Target → one stream on a
//	                                                 pooled connection to (member, facet) }
//
// Extracted from cmd/irohup (359.9.6) for the Android app (359.9.4) so
// the two presentations cannot drift: the zone rule, the port
// vocabulary and the pool are read from one place.
package meshtun

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/config-server/nodeagent"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
)

// Pool keeps one connection per (member, facet), shared by every local
// client of that pair and redialed when it is gone (the node rebooted,
// idle timeout). Both presentations use it: a bridge is one pair for
// the life of a listener, the tun is whatever pairs flows name.
type Pool struct {
	a   *nodeagent.Agent
	log *log.Logger

	mu    sync.Mutex
	conns map[string]*irohtransport.Conn
}

// NewPool wraps a's Dial. logger nil ⇒ log.Default().
func NewPool(a *nodeagent.Agent, logger *log.Logger) *Pool {
	if logger == nil {
		logger = log.Default()
	}
	return &Pool{a: a, log: logger, conns: map[string]*irohtransport.Conn{}}
}

// Open returns a fresh stream to name/facet: the cached connection if
// it is alive, else a redial. A stale connection (Alive but the peer is
// gone between Alive and Open) is dropped and dialed once more.
func (p *Pool) Open(ctx context.Context, name, facet string) (*irohtransport.Raw, error) {
	c, err := p.get(ctx, name, facet)
	if err != nil {
		return nil, err
	}
	raw, err := c.Open(ctx)
	if err == nil {
		return raw, nil
	}
	p.drop(name, facet, c)
	if c, err = p.get(ctx, name, facet); err != nil {
		return nil, err
	}
	return c.Open(ctx)
}

// Paths reports each live pooled connection as
// "<member>/<facet>: <path>[, <path>…]" (irohtransport.Conn.Paths),
// sorted: the evidence for whether media is riding a LAN-direct path
// or the relay.
func (p *Pool) Paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.conns))
	for k, c := range p.conns {
		if !c.Alive() {
			continue
		}
		out = append(out, k+": "+strings.Join(c.Paths(), ", "))
	}
	slices.Sort(out)
	return out
}

// Close drops every pooled connection.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, c := range p.conns {
		_ = c.Close()
		delete(p.conns, k)
	}
}

func (p *Pool) get(ctx context.Context, name, facet string) (*irohtransport.Conn, error) {
	key := name + "/" + facet
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.conns[key]; c != nil {
		if c.Alive() {
			return c, nil
		}
		p.log.Printf("%s: connection gone — redialing", key)
		_ = c.Close()
		delete(p.conns, key)
	}
	c, err := dialMember(ctx, p.a, name, facet, p.log)
	if err != nil {
		return nil, err
	}
	p.conns[key] = c
	return c, nil
}

func (p *Pool) drop(name, facet string, c *irohtransport.Conn) {
	key := name + "/" + facet
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conns[key] == c {
		_ = c.Close()
		delete(p.conns, key)
	}
}

// dialMember waits out the first beat (the name map arrives with it)
// and connects with the bundle; ErrNotBeaten is a wait, anything else
// is the answer.
func dialMember(ctx context.Context, a *nodeagent.Agent, name, facet string, logger *log.Logger) (*irohtransport.Conn, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		t0 := time.Now()
		c, err := a.Dial(ctx, name, facet)
		if err == nil {
			logger.Printf("%s/%s: connected to %s in %s", name, facet, c.Peer(), time.Since(t0).Round(time.Millisecond))
			return c, nil
		}
		if !errors.Is(err, nodeagent.ErrNotBeaten) || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// HalfCloser is a local TCP-like connection whose write side can be
// shut independently: *net.TCPConn, and the netstack's *gonet.TCPConn
// (fakeip.Conn).
type HalfCloser interface {
	net.Conn
	CloseWrite() error
}

// Pipe splices local ↔ raw until both directions are done; half-closes
// propagate. Returns bytes from the peer and bytes sent to it.
func Pipe(raw *irohtransport.Raw, local HalfCloser) (in, out int64) {
	defer raw.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, _ = io.Copy(raw, local)
		_ = raw.CloseWrite()
	}()
	in, _ = io.Copy(local, raw)
	_ = local.CloseWrite()
	<-done
	return in, out
}
