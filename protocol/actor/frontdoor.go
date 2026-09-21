package actor

import (
	"errors"

	"github.com/marnyg/talos-config/protocol/cert"
)

// FacetFrontdoor is the well-known public-reachability facet (sketch §
// "Public reachability is a default, revocable facet"; ADR-0007).
// Anyone may invoke it — the consent that admits them has aud "*" — but
// only at the cost its cav.postage names. Going dark is not re-minting
// it; exposure to an already-issued frontdoor cert lasts its tail.
const FacetFrontdoor = "#frontdoor"

// ErrNoPostage marks a frontdoor minted without a requirement: aud "*"
// without postage is malformed (VerifyChain rule 3 would never bind
// it), so the mint refuses rather than issue an unusable cert.
var ErrNoPostage = errors.New("actor: frontdoor requires a postage requirement")

// Frontdoor mints this actor's frontdoor consent — {iss: me, aud: "*",
// can: invoke, cav: {target: [me], facet: [FacetFrontdoor], postage:
// req}, exp: now+ttl} — the one chain root a stranger may present an
// EMPTY chain under. It is returned, not installed: the owner adds it
// to Consents (Hold) beside its other roots, registers a handler on
// FacetFrontdoor, and publishes the cert wherever it publishes its
// location (a lighthouse #publish carries both) so strangers can learn
// the price. req is a postage requirement string (postage.Require).
func (a *Actor) Frontdoor(req string, ttl int64) (cert.Cert, error) {
	if req == "" {
		return cert.Cert{}, ErrNoPostage
	}
	now := a.Now()
	return cert.Sign(cert.Cert{
		Aud: cert.AudAny,
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{
			Target:  []cert.ActorID{a.ID()},
			Facet:   []string{FacetFrontdoor},
			Postage: req,
		},
		Iat: now,
		Exp: now + ttl,
	}, a.Signer)
}
