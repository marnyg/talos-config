//go:build iroh

package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/marnyg/talos-config/config-server/nodeagent"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
)

// connPool keeps one connection per (member, facet), shared by every
// local client of that pair and redialed when it is gone (the node
// rebooted, idle timeout). Both presentations use it: a bridge is one
// pair for the life of a listener, the tun is whatever pairs flows
// name.
type connPool struct {
	a   *nodeagent.Agent
	log *log.Logger

	mu    sync.Mutex
	conns map[string]*irohtransport.Conn
}

func newConnPool(a *nodeagent.Agent, logger *log.Logger) *connPool {
	return &connPool{a: a, log: logger, conns: map[string]*irohtransport.Conn{}}
}

// open returns a fresh stream to name/facet: the cached connection if
// it is alive, else a redial. A stale connection (Alive but the peer is
// gone between Alive and Open) is dropped and dialed once more.
func (p *connPool) open(ctx context.Context, name, facet string) (*irohtransport.Raw, error) {
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

func (p *connPool) get(ctx context.Context, name, facet string) (*irohtransport.Conn, error) {
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
	c, err := dialMember(ctx, p.a, name, facet)
	if err != nil {
		return nil, err
	}
	p.conns[key] = c
	return c, nil
}

func (p *connPool) drop(name, facet string, c *irohtransport.Conn) {
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
func dialMember(ctx context.Context, a *nodeagent.Agent, name, facet string) (*irohtransport.Conn, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		t0 := time.Now()
		c, err := a.Dial(ctx, name, facet)
		if err == nil {
			log.Printf("%s/%s: connected to %s in %s", name, facet, c.Peer(), time.Since(t0).Round(time.Millisecond))
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

// halfCloser is a local TCP-like connection whose write side can be
// shut independently: *net.TCPConn, and the netstack's *gonet.TCPConn.
type halfCloser interface {
	net.Conn
	CloseWrite() error
}

// pipe splices local ↔ raw until both directions are done; half-closes
// propagate. Returns bytes from the peer and bytes sent to it.
func pipe(raw *irohtransport.Raw, local halfCloser) (in, out int64) {
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
