// Package lighthouse is the protocol's rendezvous actor (sketch §
// Reachability; protocol ADR-0007): an ORDINARY actor with two facets,
// a #publish that members invoke with their signed location record
// (and frontdoor cert) and a #lookup that returns them. Nothing here is
// a verifier special case — the lighthouse is one more consumer of
// actor.Actor's accept table.
//
// # What "network" means
//
// A lighthouse L founds nothing by itself. It CONSENTS `publish
// {target: [L], facet: [#publish]}` to a founder F (delegable); F mints
// publish-caps `{iss: F, aud: member, can: publish, cav: {target: [L],
// facet: [#publish]}}`. Holding such a cap is what "being in F's
// network" means; a network bundle is {L's endpoints, L's id, your
// cap}. The chain a member presents is [F→member]; L prepends its own
// consent and folds — cert.VerifyChain under verb `publish`, bound by
// actor.Actor.Verbs. Several founders = several consents on one L.
//
// #lookup is an ordinary invoke facet: per-network policy is WHO holds
// a grant to it — F issues members `invoke {target: [L], facet:
// [#lookup]}` for a members-only directory, or L consents `aud "*"` with
// postage for a public one. Policy is configuration, never code.
//
// # Directory
//
// The directory is volatile (invariant 12) and keyed by the PUBLISHER'S
// identity — the envelope signer, which the chain bound. A record is
// {reach-me-at, frontdoor?}; both must be issued by that identity,
// verify and be live. Expired records are evicted on read; a newer
// record (by iat) replaces an older one. Nothing is pushed: clients
// look up, and the record's own signature is what they trust (open
// problem 6 — a lying lighthouse can withhold, never forge).
//
// The directory is bounded (MaxRecords): a NEW publisher is refused
// ErrDirectoryFull once the cap is reached, while re-publishing by a
// member already listed always succeeds. Refusal, never eviction: an
// evicting cap would let a founder who over-issues publish-caps (or a
// captured founder) push live members out; refusal makes over-issuance
// hurt only the newest member, and shows up as an error on its beat.
package lighthouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Facets a lighthouse serves. #publish binds verb publish; #lookup is
// an ordinary invoke facet.
const (
	FacetPublish = "#publish"
	FacetLookup  = "#lookup"
)

// DefaultMaxRecords is the directory cap New installs (ADR-0007,
// chosen 2026-09-23). One record is a location cert plus an optional
// frontdoor — a few hundred bytes — so 4096 is ~1 MB: generous for the
// actor families and small networks v0 serves, small enough that a
// founder must issue thousands of caps before the newest member is
// refused. Set Lighthouse.MaxRecords before Listen to change it.
const DefaultMaxRecords = 4096

var (
	// ErrBadRecord marks a published or looked-up record that fails
	// its rule (issuer, verb, expiry, signature, frontdoor shape).
	ErrBadRecord = errors.New("lighthouse: invalid record")
	// ErrDirectoryFull marks a publish by a NEW publisher when the
	// directory already holds MaxRecords live records.
	ErrDirectoryFull = errors.New("lighthouse: directory full")
)

// Record is one directory entry: the actor's location and, if it
// published one, its frontdoor consent (how a stranger may reach it
// and at what price).
type Record struct {
	Loc       cert.Cert
	Frontdoor *cert.Cert
}

// wireRecord is Record on the wire (cert wire JSON).
type wireRecord struct {
	Loc       json.RawMessage `json:"loc,omitempty"`
	Frontdoor json.RawMessage `json:"frontdoor,omitempty"`
}

// PublishRequest is the #publish payload. Loc may be omitted when the
// envelope already piggybacks the publisher's record (actor.Send does)
// — the lighthouse then publishes that one.
type PublishRequest = wireRecord

// LookupRequest is the #lookup payload.
type LookupRequest struct {
	IDs []cert.ActorID `json:"ids"`
}

// LookupResponse is the #lookup reply body: id → record for every id
// with a live record; others are absent.
type LookupResponse struct {
	Records map[cert.ActorID]wireRecord `json:"records"`
}

// Lighthouse is the directory behind one actor's #publish/#lookup.
type Lighthouse struct {
	// MaxRecords caps the number of publishers listed; DefaultMaxRecords
	// from New. A value <= 0 means unbounded.
	MaxRecords int

	a   *actor.Actor
	mu  sync.Mutex
	dir map[cert.ActorID]Record
}

// New registers the two facets on a (and binds #publish to verb
// publish) and returns the directory. Call before a.Listen.
func New(a *actor.Actor) *Lighthouse {
	l := &Lighthouse{a: a, dir: make(map[cert.ActorID]Record), MaxRecords: DefaultMaxRecords}
	a.Verbs[FacetPublish] = cert.VerbPublish
	a.AcceptTable[FacetPublish] = l.publish
	a.AcceptTable[FacetLookup] = l.lookup
	return l
}

// CheckFrontdoor is the shape rule for a published frontdoor consent:
// issued by id, verb invoke, aud "*", a postage requirement (aud "*"
// without one binds nobody), target names id, facet names
// actor.FacetFrontdoor, unexpired at now, signature verifies.
func CheckFrontdoor(id cert.ActorID, c cert.Cert, now int64) error {
	switch {
	case c.Iss != id:
		return fmt.Errorf("%w: frontdoor iss %s is not %s", ErrBadRecord, c.Iss, id)
	case c.Can != cert.VerbInvoke:
		return fmt.Errorf("%w: frontdoor can=%q", ErrBadRecord, c.Can)
	case c.Aud != cert.AudAny:
		return fmt.Errorf("%w: frontdoor aud %q is not %q", ErrBadRecord, c.Aud, cert.AudAny)
	case c.Cav.Postage == "":
		return fmt.Errorf("%w: frontdoor without postage", ErrBadRecord)
	case len(c.Cav.Target) != 1 || c.Cav.Target[0] != id:
		return fmt.Errorf("%w: frontdoor target %v is not [%s]", ErrBadRecord, c.Cav.Target, id)
	case len(c.Cav.Facet) != 1 || c.Cav.Facet[0] != actor.FacetFrontdoor:
		return fmt.Errorf("%w: frontdoor facet %v", ErrBadRecord, c.Cav.Facet)
	case c.Exp <= now:
		return fmt.Errorf("%w: frontdoor expired", ErrBadRecord)
	}
	if err := cert.Verify(c); err != nil {
		return fmt.Errorf("%w: frontdoor: %w", ErrBadRecord, err)
	}
	return nil
}

// present reports whether a raw JSON field carries a value (not absent,
// not null).
func present(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// decodeRecord validates a wire record as id's at now.
func decodeRecord(id cert.ActorID, w wireRecord, fallback *cert.Cert, now int64) (Record, error) {
	var rec Record
	switch {
	case present(w.Loc):
		loc, err := cert.DecodeCert(w.Loc)
		if err != nil {
			return Record{}, fmt.Errorf("%w: loc: %w", ErrBadRecord, err)
		}
		rec.Loc = loc
	case fallback != nil:
		rec.Loc = *fallback
	default:
		return Record{}, fmt.Errorf("%w: no location record", ErrBadRecord)
	}
	if err := actor.CheckLocation(id, rec.Loc, now); err != nil {
		return Record{}, fmt.Errorf("%w: %w", ErrBadRecord, err)
	}
	if present(w.Frontdoor) {
		fd, err := cert.DecodeCert(w.Frontdoor)
		if err != nil {
			return Record{}, fmt.Errorf("%w: frontdoor: %w", ErrBadRecord, err)
		}
		if err := CheckFrontdoor(id, fd, now); err != nil {
			return Record{}, err
		}
		rec.Frontdoor = &fd
	}
	return rec, nil
}

func encodeRecord(rec Record) (wireRecord, error) {
	loc, err := cert.Encode(rec.Loc)
	if err != nil {
		return wireRecord{}, err
	}
	w := wireRecord{Loc: loc}
	if rec.Frontdoor != nil {
		if w.Frontdoor, err = cert.Encode(*rec.Frontdoor); err != nil {
			return wireRecord{}, err
		}
	}
	return w, nil
}

// publish serves #publish: the chain (verb publish) already bound the
// signer; the payload's record must be that signer's.
func (l *Lighthouse) publish(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	var w wireRecord
	if len(inv.Envelope.Payload) > 0 {
		if err := json.Unmarshal(inv.Envelope.Payload, &w); err != nil {
			return nil, fmt.Errorf("%w: payload: %w", ErrBadRecord, err)
		}
	}
	rec, err := decodeRecord(inv.From, w, inv.Envelope.Loc, l.a.Now())
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.dir[inv.From]; ok {
		if cur.Loc.Iat > rec.Loc.Iat {
			return nil, nil // an older record does not replace a newer one
		}
	} else if l.MaxRecords > 0 && len(l.dir) >= l.MaxRecords {
		// A new publisher: make room only from expired entries, then
		// refuse rather than evict a live member.
		l.evictExpiredLocked(l.a.Now())
		if len(l.dir) >= l.MaxRecords {
			return nil, ErrDirectoryFull
		}
	}
	l.dir[inv.From] = rec
	return nil, nil
}

// evictExpiredLocked drops every record whose location has expired at
// now. Caller holds l.mu.
func (l *Lighthouse) evictExpiredLocked(now int64) {
	for id, rec := range l.dir {
		if rec.Loc.Exp <= now {
			delete(l.dir, id)
		}
	}
}

// lookup serves #lookup over the published directory.
func (l *Lighthouse) lookup(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	var req LookupRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("lighthouse: payload: %w", err)
	}
	resp := LookupResponse{Records: make(map[cert.ActorID]wireRecord, len(req.IDs))}
	for id, rec := range l.Records(req.IDs...) {
		w, err := encodeRecord(rec)
		if err != nil {
			return nil, err
		}
		resp.Records[id] = w
	}
	return json.Marshal(resp)
}

// Records returns the live records for ids (all when none given),
// evicting expired ones under the actor's effective clock.
func (l *Lighthouse) Records(ids ...cert.ActorID) map[cert.ActorID]Record {
	now := l.a.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(ids) == 0 {
		for id := range l.dir {
			ids = append(ids, id)
		}
	}
	out := make(map[cert.ActorID]Record, len(ids))
	for _, id := range ids {
		rec, ok := l.dir[id]
		if !ok {
			continue
		}
		if rec.Loc.Exp <= now {
			delete(l.dir, id)
			continue
		}
		if rec.Frontdoor != nil && rec.Frontdoor.Exp <= now {
			rec.Frontdoor = nil
			l.dir[id] = rec
		}
		out[id] = rec
	}
	return out
}

// ---- client side ----------------------------------------------------------

// Publish invokes lh#publish from a with a's current location record
// (a.CurrentLocation; the envelope piggybacks it, so the payload only
// carries the frontdoor) and an optional frontdoor consent. a must hold
// a publish-cap chain under Grants[{lh, FacetPublish}].
func Publish(ctx context.Context, a *actor.Actor, lh cert.ActorID, frontdoor *cert.Cert) error {
	var w wireRecord
	if frontdoor != nil {
		raw, err := cert.Encode(*frontdoor)
		if err != nil {
			return err
		}
		w.Frontdoor = raw
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = a.Send(ctx, lh, FacetPublish, payload)
	return err
}

// Lookup invokes lh#lookup from a for ids, validates every returned
// record as its id's (the lighthouse is not trusted — the signatures
// are), caches each location in a, and returns the records. A record
// that fails its rule is dropped, not fatal: one bad entry must not
// hide the good ones.
func Lookup(ctx context.Context, a *actor.Actor, lh cert.ActorID, ids ...cert.ActorID) (map[cert.ActorID]Record, error) {
	payload, err := json.Marshal(LookupRequest{IDs: ids})
	if err != nil {
		return nil, err
	}
	rep, err := a.Send(ctx, lh, FacetLookup, payload)
	if err != nil {
		return nil, err
	}
	var resp LookupResponse
	if err := json.Unmarshal(rep.Payload, &resp); err != nil {
		return nil, fmt.Errorf("lighthouse: reply: %w", err)
	}
	now := a.Now()
	out := make(map[cert.ActorID]Record, len(resp.Records))
	for id, w := range resp.Records {
		rec, err := decodeRecord(id, w, nil, now)
		if err != nil {
			continue
		}
		if err := a.UpdateLocation(id, &rec.Loc); err != nil {
			continue
		}
		out[id] = rec
	}
	return out, nil
}
