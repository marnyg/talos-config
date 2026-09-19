package main

// The hub as a CALLER on the identity plane (talos-config-359.9.2,
// Mesh v3 P2.2): auto-bootstrap's apid dials leave the nebula netstack
// and become stream-facet connections like any admin device's. Nothing
// is special about the hub on this side — the ruling on 359.11.2
// (2026-09-18, 359.8.5 grill-design): the hub is an ORDINARY caller
// with a member cert it mints for itself {aud: hubkey, name: hub} and
// the grants the recipe compiles for that name (`{facet: apid, host:
// hub}` under node), signed by the same hot key under the same
// speak-as. No new authorize rule: the node's Authorize resolves the
// hub's certs through the wallet's speak-as exactly as it resolves a
// laptop's.
//
// The hub's self-issued member cert is never presented at #bundle, so
// it is never witnessed and never enters the name map: "hub" stays the
// well-known name the presentation layer answers from the hub record
// (nodeagent.HubName), not a member.
//
// The name map is the only directory (decision 2fc): a member the hub
// has not seen beat since its own start is unknown, not unreachable —
// the mirror of ipt7, settled by decision z2go (members re-beat when
// their connection to the hub dies), so the gap is one MinRebeat past
// the unseal.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// HubMemberName is the name the hub's self-minted member cert carries
// and the recipe's `host:` rows name it by.
const HubMemberName = "hub"

// errMemberUnknown: no member of that name has beaten this hub process
// (name map miss) — or none with a live location.
var errMemberUnknown = errors.New("hub: member not in the name map (no beat since the hub started)")

// facetClient is one outbound stream-facet connection as the untagged
// side sees it (irohtransport.Conn on the iroh side).
type facetClient interface {
	Peer() cert.ActorID
	Open(ctx context.Context) (io.ReadWriteCloser, error)
	Close() error
}

// facetDialer is what a wan endpoint offers beyond actor.Endpoint for
// outbound stream facets (hubiroh.go).
type facetDialer interface {
	DialFacet(ctx context.Context, id cert.ActorID, hints []string, alpn string, preamble []byte) (facetClient, error)
}

// callerBundle is the hub's presented bundle, minted per speak-as and
// re-minted before its grants run out. Safe-to-lose: a cache of what
// the hot key can re-sign any time (ADR-0019).
type callerBundle struct {
	forExp int64 // speak-as Exp the bundle was minted under
	bundle cert.Bundle
	minted int64
}

// present returns the bundle the hub shows on connect: its member cert
// and the recipe's grants for name "hub", under the current speak-as.
func (m *hubManager) present() (cert.Bundle, error) {
	sa := m.issuer.SpeakAs()
	if sa == nil {
		return cert.Bundle{}, issuer.ErrSealed
	}
	now := m.issuer.Actor.Now()
	m.caller.mu.Lock()
	defer m.caller.mu.Unlock()
	c := &m.caller.b
	if c.forExp == sa.Exp && now-c.minted < policy.GrantTTL/2 {
		return c.bundle, nil
	}
	if m.issuer.Policy == nil {
		return cert.Bundle{}, issuer.ErrNoPolicy
	}
	recipe, _, err := m.issuer.Policy()
	if err != nil {
		return cert.Bundle{}, fmt.Errorf("hub caller: policy: %w", err)
	}
	kit, err := m.issuer.Mint(m.issuer.ID(), HubMemberName, nil)
	if err != nil {
		return cert.Bundle{}, fmt.Errorf("hub caller: member: %w", err)
	}
	b := cert.Bundle{Member: kit.Member, SpeakAs: []cert.Cert{*sa}}
	for _, g := range policy.Compile(recipe, policy.Caller{Key: m.issuer.ID(), Name: HubMemberName}, now) {
		signed, err := cert.Sign(g, m.issuer.Actor.Signer)
		if err != nil {
			return cert.Bundle{}, fmt.Errorf("hub caller: grant: %w", err)
		}
		b.Grants = append(b.Grants, signed)
	}
	*c = callerBundle{forExp: sa.Exp, bundle: b, minted: now}
	return b, nil
}

// dialMember connects to the member named name on facet, presenting
// the hub's bundle. Candidates come from the name map, newest member
// cert first (two NodeIds may share a name across a re-key, decision
// 2fc); each dial is bounded by timeout. The caller owns the client.
func (m *hubManager) dialMember(ctx context.Context, name, facet string, timeout time.Duration) (facetClient, error) {
	d, ok := m.wan.(facetDialer)
	if !ok {
		return nil, errors.New("hub: no identity-plane endpoint (--iroh-relay unset)")
	}
	b, err := m.present()
	if err != nil {
		return nil, err
	}
	pre, err := cert.EncodeBundle(b)
	if err != nil {
		return nil, err
	}
	now := m.issuer.Actor.Now()
	var entries []issuer.NameEntry
	for _, e := range issuer.Lookup(m.issuer.NameMap(), name) {
		if e.Location != nil && e.Location.Exp > now {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: %s", errMemberUnknown, name)
	}
	issuer.SortNewest(entries)
	err = errMemberUnknown
	for _, e := range entries {
		id := cert.ActorID(e.Member.Aud)
		dctx, cancel := context.WithTimeout(ctx, timeout)
		c, derr := d.DialFacet(dctx, id, e.Location.Cav.Endpoints, policy.ALPN(facet), pre)
		timedOut := dctx.Err() != nil && ctx.Err() == nil
		cancel()
		if derr == nil {
			return c, nil
		}
		if timedOut {
			derr = fmt.Errorf("%w: no answer in %s", actor.ErrUnreachable, timeout)
		}
		err = fmt.Errorf("%s (%s): %w", name, id, derr)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, err
}

// facetStreamConn adapts one opened forward stream to net.Conn for a
// gRPC dialer. Deadlines are accepted and ignored, as on the accept
// side (streamNetConn): the stream's lifetime is the connection's.
type facetStreamConn struct {
	io.ReadWriteCloser
	peer cert.ActorID
}

func (c *facetStreamConn) LocalAddr() net.Addr              { return facetAddr(HubMemberName) }
func (c *facetStreamConn) RemoteAddr() net.Addr             { return facetAddr(string(c.peer)) }
func (c *facetStreamConn) SetDeadline(time.Time) error      { return nil }
func (c *facetStreamConn) SetReadDeadline(time.Time) error  { return nil }
func (c *facetStreamConn) SetWriteDeadline(time.Time) error { return nil }
