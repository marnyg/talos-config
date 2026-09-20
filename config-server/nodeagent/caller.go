//go:build iroh

package nodeagent

// The caller side of a member (talos-config-359.8.4): what it presents
// on connect and how it finds another member by name. Bundle on
// connect (domain model): the caller's member cert, the invoke grants
// the last #bundle compiled for it, and the speak-as certs that
// resolve their issuers to the Owner — everything the receiver's
// Authorize needs, offline. The name map from the same #bundle is the
// only directory: name → NodeId (the member's cert, Owner-signed) and
// NodeId → reach-me-at (the member's own, self-signed).

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/policy"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Present is the bundle this member shows on connect. The speak-as
// list carries the Kit's (resolving the member cert's issuer) and the
// bundle's (resolving the grants' issuer) — one cert when the hub has
// not rotated between them, two when it has.
func (a *Agent) Present() (cert.Bundle, error) {
	a.mu.Lock()
	kit, b := a.kit, a.bundle
	a.mu.Unlock()
	if kit == nil {
		return cert.Bundle{}, errors.New("nodeagent: not enrolled")
	}
	if b == nil {
		return cert.Bundle{}, ErrNotBeaten
	}
	speakAs := []cert.Cert{kit.SpeakAs}
	if b.SpeakAs.Aud != kit.SpeakAs.Aud {
		speakAs = append(speakAs, b.SpeakAs)
	}
	return cert.Bundle{Member: kit.Member, Grants: slices.Clone(b.Grants), SpeakAs: speakAs}, nil
}

// Resolve finds name in the last name map: the candidate NodeIds with
// a live reach-me-at, newest member cert first (two may coexist across
// a re-key — decision 2fc: pick by iat or dial both; Dial does both).
// HubName resolves to the hub itself (talos-config-359.8.2.4).
func (a *Agent) Resolve(name string) ([]issuer.NameEntry, error) {
	now := a.actor.Now()
	if name == HubName {
		a.mu.Lock()
		h := a.hub
		a.mu.Unlock()
		if h == nil {
			return nil, ErrNotBeaten
		}
		if h.ReachMeAt.Exp <= now || h.SpeakAs.Exp <= now {
			return nil, fmt.Errorf("%w: hub record expired", ErrUnknownName)
		}
		// The entry's Member is a stand-in naming the hubkey: Dial reads
		// only Aud and Iat from it, and the hub's authority is its
		// speak-as, verified on the receiver's side of every stream.
		return []issuer.NameEntry{{
			Member:   cert.Cert{Aud: string(h.ID()), Iat: h.SpeakAs.Iat, Exp: h.SpeakAs.Exp},
			Location: &h.ReachMeAt,
		}}, nil
	}
	b := a.Bundle()
	if b == nil {
		return nil, ErrNotBeaten
	}
	var out []issuer.NameEntry
	for _, e := range issuer.Lookup(b.NameMap, name) {
		if e.Location == nil || e.Location.Exp <= now || e.Member.Exp <= now {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	slices.SortFunc(out, func(x, y issuer.NameEntry) int { return int(y.Member.Iat - x.Member.Iat) })
	return out, nil
}

// Zone reads a presentation name by the zone rule (zone.go) over this
// member's directory.
func (a *Agent) Zone(name string) (member string, kind policy.Kind, err error) {
	return zone(name, a.Resolve)
}

// Target is what a flow to <name>:<port> means, as (member, facet).
func (a *Agent) Target(name string, port uint16) (member, facet string, err error) {
	return target(name, port, a.Resolve)
}

// Dial connects to the member called name under facet, presenting this
// member's bundle. Candidates from Resolve are tried newest first; a
// refusal or a transport failure on one moves to the next, and the
// last error is returned when none admits.
//
// The directory Resolve reads is a beat old. A name it lacks, or a
// candidate nobody answers at, is evidence it is stale — the hub
// redeployed under a new key (ipt7), a member enrolled, re-keyed or
// moved since — so Dial re-beats (rate-limited, MinRebeat) and tries
// once more on the fresh copy before giving up. A refusal is the
// receiver's answer, not staleness, and is returned as is.
func (a *Agent) Dial(ctx context.Context, name, facet string) (*irohtransport.Conn, error) {
	conn, err := a.dial(ctx, name, facet)
	if err == nil || ctx.Err() != nil || !stale(err) {
		return conn, err
	}
	if !a.rebeat(ctx) {
		return nil, err
	}
	return a.dial(ctx, name, facet)
}

// stale: the failures a fresh beat may cure.
func stale(err error) bool {
	return errors.Is(err, ErrUnknownName) || errors.Is(err, actor.ErrUnreachable)
}

func (a *Agent) dial(ctx context.Context, name, facet string) (*irohtransport.Conn, error) {
	pre, err := a.Present()
	if err != nil {
		return nil, err
	}
	raw, err := cert.EncodeBundle(pre)
	if err != nil {
		return nil, err
	}
	entries, err := a.Resolve(name)
	if err != nil {
		return nil, err
	}
	alpn := policy.ALPN(facet)
	timeout := a.dialTimeout()
	for _, e := range entries {
		id := cert.ActorID(e.Member.Aud)
		dctx, cancel := context.WithTimeout(ctx, timeout)
		conn, derr := a.ep.DialConn(dctx, id, e.Location.Cav.Endpoints, alpn, raw)
		timedOut := dctx.Err() != nil && ctx.Err() == nil
		cancel()
		if derr == nil {
			return conn, nil
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
