// Package child is what a spawned actor does after its key exists and
// its transport is up (protocol ADR-0008/0009; sketch § Spawning):
// exchange the intro for the starter kit (spawn.Born), then live on
// the beat — re-#renew every cert the parent issued it, republish its
// reach-me-at — until the renewal edge to the parent is gone, at which
// point it exits: self-lapse. The parent's side of the same beat
// (spawn.Spawner's #renew decorator) turns each renewal into #extend
// at the provisioner, so a child that keeps renewing keeps its lease,
// and one whose parent stops answering lapses twice — here by exit,
// there by the provisioner's Sweep. Either alone is enough.
//
// Transport-free: cmd/child binds iroh and hands the actor in. Tests
// run it over actor.MemoryNetwork against a real spawn.Spawner.
//
// # What the beat renews
//
// Every held chain whose last link the parent issued to this actor —
// the mandated (parent, #renew) chain and whatever else the kit's
// Outfit added. One #renew per beat carries them all; each fresh cert
// replaces the old last link under every (target, facet) the old one
// sat under. Chains other actors issued are not renewed here (v0: the
// parent is the only correspondent a child is born with).
//
// # Self-lapse
//
// The edge is live while a held chain at (parent, #renew) has an
// unexpired last link. A refused or failed #renew is logged and
// retried next beat; the chain expiring is what ends the process
// (ErrLapsed) — revocation is expiry, on this side too.
package child

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/spawn"
)

// FacetPing is the one app facet a v0 child serves: the payload comes
// back verbatim. It exists so a parent can prove the P→C direction
// (the child's own consent, minted at Born) works — the acceptance
// run's probe, nothing more.
const FacetPing = "#ping"

// Defaults; numbers, not rules.
const (
	DefaultBeat        = 30 * time.Second
	DefaultLocationTTL = 10 * 60
	DefaultConsentTTL  = 24 * 3600
)

// ErrLapsed marks a Run that ended because no live chain to
// (parent, #renew) remains: the parent stopped re-issuing, or the
// child could not reach it before the chain expired.
var ErrLapsed = errors.New("child: renewal edge to the parent lapsed")

// Options configures Run. The zero value serves FacetPing, beats every
// DefaultBeat, publishes its location for DefaultLocationTTL seconds
// and consents the parent for DefaultConsentTTL seconds.
type Options struct {
	// Serves are the app facets the child answers on and consents the
	// parent to at Born. nil ⇒ [FacetPing]. Handlers for facets other
	// than FacetPing are the caller's to install before Run.
	Serves []string
	// ConsentTTL is the lifetime in seconds of the child's own consent
	// to the parent (spawn.Born); 0 ⇒ DefaultConsentTTL.
	ConsentTTL int64
	// Beat is the renewal interval; 0 ⇒ DefaultBeat.
	Beat time.Duration
	// LocationTTL is the lifetime in seconds of each published
	// reach-me-at; 0 ⇒ DefaultLocationTTL. Keep it well above Beat.
	LocationTTL int64
	// Log receives each beat's outcome. nil ⇒ slog.Default().
	Log *slog.Logger
}

func (o Options) serves() []string {
	if o.Serves == nil {
		return []string{FacetPing}
	}
	return o.Serves
}

func (o Options) consentTTL() int64 {
	if o.ConsentTTL > 0 {
		return o.ConsentTTL
	}
	return DefaultConsentTTL
}

func (o Options) beat() time.Duration {
	if o.Beat > 0 {
		return o.Beat
	}
	return DefaultBeat
}

func (o Options) locationTTL() int64 {
	if o.LocationTTL > 0 {
		return o.LocationTTL
	}
	return DefaultLocationTTL
}

func (o Options) log() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.Default()
}

// Ping serves FacetPing: the payload, back.
func Ping(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	return inv.Envelope.Payload, nil
}

// Run is the child's life on actor a: publish a location, Born(in),
// then beat until ctx ends (ctx.Err()) or the renewal edge lapses
// (ErrLapsed). a.Listen must be running (the parent calls in on the
// served facets, and #birth's reply is not enough to be reachable);
// Run itself is the only goroutine that touches a.Grants, as Send and
// Born require. FacetPing's handler is installed here when served.
func Run(ctx context.Context, a *actor.Actor, in spawn.Intro, o Options) error {
	log := o.log().With("parent", in.Parent, "me", a.ID())
	serves := o.serves()
	for _, f := range serves {
		if f == FacetPing {
			a.AcceptTable[FacetPing] = Ping
		}
	}
	if _, err := a.PublishLocation(o.locationTTL()); err != nil {
		return fmt.Errorf("child: location: %w", err)
	}
	kit, err := spawn.Born(ctx, a, in, serves, o.consentTTL())
	if err != nil {
		return fmt.Errorf("child: born: %w", err)
	}
	log.Info("child: born", "grants", len(kit.Grants), "locations", len(kit.Locations), "serves", serves)

	t := time.NewTicker(o.beat())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		now := a.Now()
		if !Live(a, in.Parent, now) {
			log.Info("child: lapsed")
			return ErrLapsed
		}
		if _, err := a.PublishLocation(o.locationTTL()); err != nil {
			log.Warn("child: location", "err", err)
		}
		n, err := Renew(ctx, a, in.Parent)
		if err != nil {
			log.Warn("child: renew failed", "err", err)
			continue
		}
		log.Info("child: renewed", "certs", n)
	}
}

// Live reports whether a holds a chain at (parent, #renew) whose last
// link is unexpired at now — the edge Run lives on.
func Live(a *actor.Actor, parent cert.ActorID, now int64) bool {
	chain := a.Grants[actor.GrantKey{Target: parent, Facet: actor.FacetRenew}]
	if len(chain) == 0 {
		return false
	}
	return chain[len(chain)-1].Exp > now
}

// Renew re-issues, in one #renew to parent, every held cert parent
// issued to a (the last link of a held chain with iss == parent, aud
// == me), and installs each fresh cert in place of the old one under
// every (target, facet) it was held at. Returns how many were
// re-issued; a per-item refusal is not an error (that cert stays until
// it expires). Call from the goroutine that owns a.Grants.
func Renew(ctx context.Context, a *actor.Actor, parent cert.ActorID) (int, error) {
	me := string(a.ID())
	var certs []cert.Cert
	var keys [][]actor.GrantKey // per cert: the grant keys it is the last link of
	index := make(map[string]int) // sig → position in certs
	for key, chain := range a.Grants {
		if len(chain) == 0 {
			continue
		}
		last := chain[len(chain)-1]
		if last.Iss != parent || last.Aud != me {
			continue
		}
		i, ok := index[string(last.Sig)]
		if !ok {
			i = len(certs)
			index[string(last.Sig)] = i
			certs = append(certs, last)
			keys = append(keys, nil)
		}
		keys[i] = append(keys[i], key)
	}
	if len(certs) == 0 {
		return 0, ErrLapsed
	}
	payload, err := actor.EncodeRenewRequest(certs, nil)
	if err != nil {
		return 0, err
	}
	rep, err := a.Send(ctx, parent, actor.FacetRenew, payload)
	if err != nil {
		return 0, err
	}
	fresh, errs, err := actor.DecodeRenewResponse(rep.Payload)
	if err != nil {
		return 0, err
	}
	if len(fresh) != len(certs) {
		return 0, fmt.Errorf("child: renew: %d results for %d items", len(fresh), len(certs))
	}
	n := 0
	for i := range certs {
		if errs[i] != nil {
			continue
		}
		for _, key := range keys[i] {
			chain := append([]cert.Cert(nil), a.Grants[key]...)
			chain[len(chain)-1] = fresh[i]
			a.Grant(key.Target, key.Facet, chain...)
		}
		n++
	}
	return n, nil
}
