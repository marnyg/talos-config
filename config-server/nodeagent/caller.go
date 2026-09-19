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
	"github.com/marnyg/talos-config/protocol/cert"
)

// ErrNotBeaten: no bundle yet, so nothing to present and no name map.
var ErrNotBeaten = errors.New("nodeagent: no bundle yet (first beat pending)")

// ErrUnknownName: the name map has no live entry for the name.
var ErrUnknownName = errors.New("nodeagent: name not in the name map")

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
func (a *Agent) Resolve(name string) ([]issuer.NameEntry, error) {
	b := a.Bundle()
	if b == nil {
		return nil, ErrNotBeaten
	}
	now := a.actor.Now()
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

// Dial connects to the member called name under facet, presenting this
// member's bundle. Candidates from Resolve are tried newest first; a
// refusal or a transport failure on one moves to the next, and the
// last error is returned when none admits.
func (a *Agent) Dial(ctx context.Context, name, facet string) (*irohtransport.Conn, error) {
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
	for _, e := range entries {
		id := cert.ActorID(e.Member.Aud)
		conn, derr := a.ep.DialConn(ctx, id, e.Location.Cav.Endpoints, alpn, raw)
		if derr == nil {
			return conn, nil
		}
		err = fmt.Errorf("%s (%s): %w", name, id, derr)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, err
}
