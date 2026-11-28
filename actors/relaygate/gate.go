// Package relaygate is the admission gate in front of a relay that
// serves sovereign actors (protocol ADR-0012, root ADR-0051).
//
// A relay's gate is a receiver R. Whether a key may hold a connection
// on the relay is cert.VerifyChain on the `relay` verb, rooted at R's
// own consents — the three callers are three chains:
//
//	member    R's consent names it           presents nothing
//	child     [P→C relay], from its kit      renewed on the beat
//	newborn   [P→"*" relay + postage], from  one birth window; the stamp
//	          its intro                      is over (R, the newborn's key)
//
// The stock relay asks one question at connect — "may this key?" — and
// carries nothing from the client that could answer it, so the answer
// has to be here already. An actor therefore PRESENTS its chain out of
// band (Presentation, POST AdmitPath); the Gate verifies it and
// remembers `id → exp`. That table is a cache of what grantees showed,
// never a registry: losing it refuses everyone until they present
// again, and nothing but a presented chain can put an id in it.
//
// Server wires a Gate to a relay process: the access hook the relay
// calls (it names the key that PROVED itself, so a chain admits its
// own key and nobody claiming to be it), and a reverse proxy that ends
// a connection when its credential does — which the relay alone would
// not, since it asks only at connect.
//
// C-free: net/http and protocol/cert only. The relay process, its
// supervision and the actor that owns R's key are the caller's.
package relaygate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/clock"
	"github.com/marnyg/talos-config/protocol/postage"
)

// Facet is the facet a `relay` cert names: the protocol's "#relay"
// (domain model, Relay).
const Facet = "#relay"

// DefaultMaxEntries bounds the presented-credential table. Refusal,
// never eviction (the lighthouse directory's rule, ADR-0007): a flood
// of valid presentations must not push a live actor out.
const DefaultMaxEntries = 4096

// MaxChain bounds the links one presentation may carry. Member → child
// is one; the bound is for the verifier's cost, not a protocol limit.
const MaxChain = 8

var (
	// ErrRefused wraps every reason a presentation does not admit its
	// id: the chain does not verify, or its postage is not paid.
	ErrRefused = errors.New("relaygate: refused")
	// ErrFull means the table is at MaxEntries with nothing expired.
	ErrFull = errors.New("relaygate: table full")
	// ErrBadPresentation is a presentation that cannot be read.
	ErrBadPresentation = errors.New("relaygate: bad presentation")
)

// Presentation is what an actor shows the gate: the key it will
// connect as, the links it holds (the gate prepends its own consent),
// and the stamp, if the chain demands postage.
//
// It is not authenticated and need not be: showing X's chain admits X.
type Presentation struct {
	ID    cert.ActorID
	Chain []cert.Cert
	Stamp string
}

type wirePresentation struct {
	ID    cert.ActorID      `json:"id"`
	Chain []json.RawMessage `json:"chain"`
	Stamp string            `json:"stamp,omitempty"`
}

// EncodePresentation renders p as the AdmitPath request body.
func EncodePresentation(p Presentation) ([]byte, error) {
	w := wirePresentation{ID: p.ID, Chain: make([]json.RawMessage, 0, len(p.Chain)), Stamp: p.Stamp}
	for _, c := range p.Chain {
		b, err := cert.Encode(c)
		if err != nil {
			return nil, err
		}
		w.Chain = append(w.Chain, b)
	}
	return json.Marshal(w)
}

// DecodePresentation parses an AdmitPath request body.
func DecodePresentation(data []byte) (Presentation, error) {
	var w wirePresentation
	if err := json.Unmarshal(data, &w); err != nil {
		return Presentation{}, fmt.Errorf("%w: %w", ErrBadPresentation, err)
	}
	if len(w.Chain) > MaxChain {
		return Presentation{}, fmt.Errorf("%w: %d links, at most %d", ErrBadPresentation, len(w.Chain), MaxChain)
	}
	p := Presentation{ID: w.ID, Stamp: w.Stamp}
	for i, raw := range w.Chain {
		c, err := cert.DecodeCert(raw)
		if err != nil {
			return Presentation{}, fmt.Errorf("%w: link %d: %w", ErrBadPresentation, i, err)
		}
		p.Chain = append(p.Chain, c)
	}
	return p, nil
}

// StampPreimage is what a newborn's stamp is over: this relay and this
// key. A stamp therefore admits one key at one relay, however many
// hands the wildcard link passes through.
func StampPreimage(relay, id cert.ActorID) []byte {
	return []byte("sap-relay-admit/v1|" + string(relay) + "|" + string(id))
}

// relayKey reports whether id can be a relay client at all: the relay
// authenticates Ed25519 endpoint keys and nothing else.
func relayKey(id cert.ActorID) bool {
	return strings.HasPrefix(string(id), "ed:") && id.Validate() == nil
}

// Gate decides admission for one relay. Configure the exported fields
// before first use; safe for concurrent use after.
type Gate struct {
	// Scheme checks the stamp a postage-bearing chain demands. nil ⇒
	// postage.Default.
	Scheme postage.Scheme
	// MaxEntries bounds the presented table. 0 ⇒ DefaultMaxEntries.
	MaxEntries int
	// Clock is the local clock in unix seconds. nil ⇒ time.Now.
	Clock func() int64

	self    cert.ActorID
	recv    cert.Receiver
	members map[cert.ActorID]int64 // consented keys → the consent's exp

	mark  clock.Mark
	mu    sync.Mutex
	shown map[cert.ActorID]int64 // presented credentials → exp
}

// New returns the gate of relay self, rooted at consents: self's own
// `relay` consents, as its actor minted them. A consent that names a
// key outright makes that key a member — admitted with nothing
// presented, until the consent expires. Consents are fixed for the
// gate's life: revoking a member, and with it every chain under it, is
// a new gate without that consent.
func New(self cert.ActorID, consents []cert.Cert) *Gate {
	return newAt(self, consents, nil)
}

// newAt is New on a given clock: members are judged at construction,
// so a test's clock has to be in place before it.
func newAt(self cert.ActorID, consents []cert.Cert, clock func() int64) *Gate {
	g := &Gate{
		Clock:   clock,
		self:    self,
		recv:    cert.Receiver{ID: self, Consents: consents},
		members: make(map[cert.ActorID]int64),
		shown:   make(map[cert.ActorID]int64),
	}
	now := g.now()
	for _, c := range consents {
		id := cert.ActorID(c.Aud)
		if c.Can != cert.VerbRelay || !relayKey(id) {
			continue
		}
		// The verifier's own judgment of "this consent admits its aud",
		// not a re-reading of its fields.
		eff, verified, err := cert.VerifyChain(g.recv, cert.VerbRelay, nil, nil, id, Facet, now)
		g.mark.ObserveAll(verified)
		if err == nil && eff.Exp > g.members[id] {
			g.members[id] = eff.Exp
		}
	}
	return g
}

// Self is the relay's id: the target a `relay` chain must name.
func (g *Gate) Self() cert.ActorID { return g.self }

// Members is the number of keys admitted by consent alone.
func (g *Gate) Members() int { return len(g.members) }

// now is the verifier's clock: max(local, low-water mark) — protocol
// invariant 9. The mark is fed with exactly what cert.VerifyChain
// reports as rooted here. Today that is the gate's own consents and
// nothing a caller presents: the verifier roots caller links only under
// an `invoke` consent (cert.rootedPrincipals), so a `relay` chain's
// links do not advance the mark (thread talos-config-w0ur). The floor a
// rolled-back clock cannot go under is therefore the newest consent's
// iat — the gate's start — not the last presentation.
func (g *Gate) now() int64 {
	local := time.Now().Unix()
	if g.Clock != nil {
		local = g.Clock()
	}
	return g.mark.Now(local)
}

func (g *Gate) scheme() postage.Scheme {
	if g.Scheme != nil {
		return g.Scheme
	}
	return postage.Default
}

func (g *Gate) max() int {
	if g.MaxEntries > 0 {
		return g.MaxEntries
	}
	return DefaultMaxEntries
}

// Present verifies p and, if it admits p.ID, remembers it until the
// chain expires. It returns that expiry. A later presentation never
// shortens an earlier one: both were valid grants.
func (g *Gate) Present(p Presentation) (exp int64, err error) {
	if !relayKey(p.ID) {
		return 0, fmt.Errorf("%w: id %q is not an ed: key", ErrBadPresentation, p.ID)
	}
	if len(p.Chain) > MaxChain {
		return 0, fmt.Errorf("%w: %d links, at most %d", ErrBadPresentation, len(p.Chain), MaxChain)
	}
	now := g.now()
	eff, verified, err := cert.VerifyChain(g.recv, cert.VerbRelay, p.Chain, nil, p.ID, Facet, now)
	g.mark.ObserveAll(verified)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if eff.Cav.Postage != "" {
		if err := g.scheme().Check(eff.Cav.Postage, StampPreimage(g.self, p.ID), p.Stamp); err != nil {
			return 0, fmt.Errorf("%w: %w", ErrRefused, err)
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	old, held := g.shown[p.ID]
	if !held && len(g.shown) >= g.max() {
		for id, e := range g.shown {
			if e <= now {
				delete(g.shown, id)
			}
		}
		if len(g.shown) >= g.max() {
			return 0, ErrFull
		}
	}
	if eff.Exp > old {
		g.shown[p.ID] = eff.Exp
		return eff.Exp, nil
	}
	return old, nil
}

// Admitted reports whether id may hold a connection now, and until
// when (unix seconds). The relay itself is always admitted — its actor
// homes on it. One map read per table: this is the path a refused
// client hits several times a second.
func (g *Gate) Admitted(id cert.ActorID) (until int64, ok bool) {
	if id == g.self {
		return math.MaxInt64, true
	}
	until = g.members[id]
	g.mu.Lock()
	if e := g.shown[id]; e > until {
		until = e
	}
	g.mu.Unlock()
	return until, until > g.now()
}

// Shown is the number of presented credentials held, expired or not.
func (g *Gate) Shown() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.shown)
}
