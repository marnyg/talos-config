package actor

import (
	"errors"
	"fmt"

	"github.com/marnyg/talos-config/protocol/cert"
)

// Location records (reach-me-at, ADR-0001): a cert {iss: P, aud: "*",
// can: reach-me-at, cav.endpoints: [tagged strings], iat, exp ≈ 1 h}.
// Distribution is by piggyback: every Envelope and Reply this actor
// sends carries its current record (Send / signReply attach it); every
// record received rides envelope.Verify / VerifyReply's one fail-closed
// rule (bad loc rejects the whole message) and, when good, lands here
// as id → latest valid record. The cache is volatile.

var (
	// ErrBadLocation marks a reach-me-at record that fails the rule
	// (verb, issuer, expiry, signature) on the local UpdateLocation path.
	ErrBadLocation = errors.New("actor: invalid location record")
)

// checkLocation is the same rule envelope.checkLoc applies on the wire,
// for records that arrive by another path (tests, bootstrap bundles).
func checkLocation(id cert.ActorID, loc cert.Cert, now int64) error {
	switch {
	case loc.Can != cert.VerbReachMeAt:
		return fmt.Errorf("%w: can=%q", ErrBadLocation, loc.Can)
	case loc.Iss != id:
		return fmt.Errorf("%w: iss %s is not %s", ErrBadLocation, loc.Iss, id)
	case loc.Exp <= now:
		return fmt.Errorf("%w: expired", ErrBadLocation)
	}
	if err := cert.Verify(loc); err != nil {
		return fmt.Errorf("%w: %w", ErrBadLocation, err)
	}
	return nil
}

// updateLocationLocked stores loc as id's latest record unless a newer
// one (by iat) is already cached. Caller holds mu and has validated loc.
func (a *Actor) updateLocationLocked(id cert.ActorID, loc cert.Cert, now int64) {
	if cur, ok := a.locs[id]; ok && cur.Iat > loc.Iat && cur.Exp > now {
		return
	}
	a.locs[id] = loc
}

// UpdateLocation validates and caches a reach-me-at record for id.
func (a *Actor) UpdateLocation(id cert.ActorID, loc *cert.Cert) error {
	if loc == nil {
		return fmt.Errorf("%w: nil", ErrBadLocation)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if err := checkLocation(id, *loc, now); err != nil {
		return err
	}
	a.updateLocationLocked(id, *loc, now)
	return nil
}

// GetLocation returns id's cached record, or nil when none is cached or
// the cached one has expired under the effective clock (expired records
// are evicted on read).
func (a *Actor) GetLocation(id cert.ActorID) *cert.Cert {
	a.mu.Lock()
	defer a.mu.Unlock()
	loc, ok := a.locs[id]
	if !ok {
		return nil
	}
	if loc.Exp <= a.now() {
		delete(a.locs, id)
		return nil
	}
	out := loc
	return &out
}

// hintsLocked returns the dial hints for id from its cached record.
// Caller holds mu.
func (a *Actor) hintsLocked(id cert.ActorID) []string {
	loc, ok := a.locs[id]
	if !ok || loc.Exp <= a.now() {
		return nil
	}
	return append([]string(nil), loc.Cav.Endpoints...)
}

// CurrentLocation returns the actor's own current reach-me-at record
// (nil until PublishLocation / SetLocation), the value piggybacked on
// every outbound Envelope and Reply.
func (a *Actor) CurrentLocation() *cert.Cert {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loc == nil {
		return nil
	}
	out := *a.loc
	return &out
}

// SetLocation installs a signed reach-me-at record as the actor's own.
// It must be issued by this actor and unexpired.
func (a *Actor) SetLocation(loc cert.Cert) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := checkLocation(a.ID(), loc, a.now()); err != nil {
		return err
	}
	a.loc = &loc
	return nil
}

// PublishLocation mints and installs a fresh reach-me-at record with
// lifetime ttl seconds over the given endpoints. With no endpoints
// given, the Transport's Endpoints() are used when it is an Endpoint.
// It does not push the record anywhere: piggyback does that on the
// next message; lighthouses (#publish) are out of scope here.
func (a *Actor) PublishLocation(ttl int64, endpoints ...string) (cert.Cert, error) {
	if len(endpoints) == 0 {
		ep, ok := a.Transport.(Endpoint)
		if !ok {
			return cert.Cert{}, errors.New("actor: no endpoints and transport is not an Endpoint")
		}
		endpoints = ep.Endpoints()
	}
	now := a.Now()
	loc, err := cert.Sign(cert.Cert{
		Aud: cert.AudAny,
		Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Endpoints: append([]string(nil), endpoints...)},
		Iat: now,
		Exp: now + ttl,
	}, a.Signer)
	if err != nil {
		return cert.Cert{}, err
	}
	if err := a.SetLocation(loc); err != nil {
		return cert.Cert{}, err
	}
	return loc, nil
}
