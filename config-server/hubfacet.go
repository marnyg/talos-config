package main

// The hub-http stream facet (decision itb, talos-config-359.8.2.4): the
// hub's HTTP surface reachable over the identity plane, so an admin on
// the irohup tun fetches hub-composed configs (`nix run .#apply`)
// without nebula. The hub is not a nodeagent — there is no Forward
// table and nothing to splice to — so the facet terminates in-process:
// every admitted stream is one HTTP connection to hubFacetMux.
//
// Receiver-side decision is the same cert.Authorize the node agent
// runs, with the hub as receiver: hubkey consents to the wallet for
// the hub's stream facets (target: hubkey, like a node's consent
// targets the node), the wallet's speak-as delegates to hubkey, the
// #bundle-compiled grant names group:admins (or media) on hub-http,
// and the member cert carries the caller's groups. What the facet
// admits is the recipe's relation; which ROUTE a member may see is the
// handler's business (requireGroup) — mesh-policy-v3.yaml grants
// hub-http to media too, and /config is not theirs.
//
// The iroh side (accepting connections, the Raw stream type) lives in
// hubiroh.go behind the `iroh` tag; this file only sees the facetConn
// interface so the decision tests C-free. The HTTP-over-streams
// plumbing is facethttp, shared with the gateway (P2.3).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"sync"

	"github.com/marnyg/talos-config/config-server/facethttp"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/cert"
)

// facetConn is one inbound stream-facet connection, as
// irohtransport.Conn presents it: a preamble to read, an ALPN and peer
// key to decide on, admit or refuse, then streams to accept.
type facetConn interface {
	ALPN() string
	Peer() cert.ActorID
	Preamble(ctx context.Context) ([]byte, error)
	Admit(ctx context.Context) error
	Refuse(ctx context.Context, reason string) error
	Accept(ctx context.Context) (io.ReadWriteCloser, error)
	Close() error
}

// facetAcceptor is what a wan endpoint offers beyond actor.Endpoint
// when it was bound with stream ALPNs (hubiroh.go).
type facetAcceptor interface {
	AcceptFacet(ctx context.Context) (facetConn, error)
}

// hubFacets is the hub's accept table: ALPN → facet over
// policy.Facets(KindHub).
var hubFacets = policy.AcceptTable(policy.KindHub)

// hubFacetALPNs lists the ALPN classes the wan endpoint must be bound
// with for the hub to receive them at all.
func hubFacetALPNs() []string {
	out := make([]string, 0, len(hubFacets))
	for alpn := range hubFacets {
		out = append(out, alpn)
	}
	slices.Sort(out)
	return out
}

// streamConsent is hubkey's consent to the wallet for the hub's stream
// facets, cached per speak-as: the wallet's authority over hubkey ends
// with the speak-as, so the consent inherits its Exp and is re-signed
// after every unseal. The beat consent (issuer.hold) covers #renew and
// #bundle with target: wallet; this one covers hub-http with target:
// hubkey, exactly the node agent's shape, so the same compiled grant
// (target "*") attenuates onto either receiver.
type streamConsent struct {
	mu      sync.Mutex
	forExp  int64 // speak-as Exp the cached consent was signed against
	consent cert.Cert
}

func (m *hubManager) streamConsent() (cert.Cert, error) {
	sa := m.issuer.SpeakAs()
	if sa == nil {
		return cert.Cert{}, errors.New("hub-http: sealed")
	}
	m.stream.mu.Lock()
	defer m.stream.mu.Unlock()
	if m.stream.forExp == sa.Exp && m.stream.consent.Aud == string(sa.Iss) {
		return m.stream.consent, nil
	}
	c, err := cert.Sign(cert.Cert{
		Aud: string(sa.Iss),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{
			Target:    []cert.ActorID{m.issuer.ID()},
			Facet:     policy.Facets(policy.KindHub),
			Delegable: true,
		},
		Iat: m.issuer.Actor.Now(),
		Exp: sa.Exp,
	}, m.issuer.Actor.Signer)
	if err != nil {
		return cert.Cert{}, fmt.Errorf("hub-http: signing consent: %w", err)
	}
	m.stream.forExp, m.stream.consent = sa.Exp, c
	return c, nil
}

// authorizeStream is the receiver's decision for one connection. Sealed
// ⇒ no consent ⇒ refused, like every other hub facet.
func (m *hubManager) authorizeStream(alpn string, peer cert.ActorID, b cert.Bundle) cert.Result {
	consents, speakAs := m.issuer.Actor.Authority()
	if c, err := m.streamConsent(); err == nil {
		consents = append(slices.Clone(consents), c)
	}
	var blocklist map[cert.ActorID]bool
	if m.issuer.Policy != nil {
		if _, bl, err := m.issuer.Policy(); err == nil {
			blocklist = make(map[cert.ActorID]bool, len(bl))
			for _, id := range bl {
				blocklist[id] = true
			}
		}
	}
	res := cert.Authorize(cert.Input{
		Receiver:    cert.Receiver{ID: m.issuer.ID(), Consents: consents, SpeakAs: speakAs},
		AcceptTable: hubFacets,
		Blocklist:   blocklist,
		Now:         m.issuer.Actor.Now(),
		ALPN:        alpn,
		Peer:        peer,
		Bundle:      b,
	})
	m.issuer.Actor.Observe(res.Verified)
	return res
}

// handleFacetConn decides one connection and, when admitted, hands
// each of its streams to ln as an HTTP connection attributed to the
// caller's identity.
func (m *hubManager) handleFacetConn(ctx context.Context, c facetConn, ln *facethttp.Listener) {
	defer c.Close()
	pre, err := c.Preamble(ctx)
	if err != nil {
		return
	}
	b, err := cert.DecodeBundle(pre)
	if err != nil {
		_ = c.Refuse(ctx, "malformed bundle")
		return
	}
	res := m.authorizeStream(c.ALPN(), c.Peer(), b)
	if !res.OK {
		log.Printf("hub-http: refused %s on %s", c.Peer(), c.ALPN())
		_ = c.Refuse(ctx, "not authorized")
		return
	}
	if err := c.Admit(ctx); err != nil {
		return
	}
	facet := hubFacets[c.ALPN()]
	log.Printf("hub-http: admitted %q %v (%s) → %s", res.Identity.Name, res.Identity.Groups, c.Peer(), facet)
	for {
		raw, err := c.Accept(ctx)
		if err != nil {
			return
		}
		if !ln.Push(ctx, raw, res.Identity, c.Peer()) {
			_ = raw.Close()
			return
		}
	}
}

// acceptFacets runs the accept loop for the life of ctx: every
// connection on a hub stream ALPN goes through handleFacetConn.
func (m *hubManager) acceptFacets(ctx context.Context, acc facetAcceptor, ln *facethttp.Listener) {
	for {
		c, err := acc.AcceptFacet(ctx)
		if err != nil {
			return
		}
		go m.handleFacetConn(ctx, c, ln)
	}
}

// serveHTTPFacet serves h over the hub-http facet until ctx ends. A
// no-op without a wan endpoint that accepts stream facets (tests
// without iroh, --iroh-relay unset).
func (m *hubManager) serveHTTPFacet(ctx context.Context, h http.Handler) {
	acc, ok := m.wan.(facetAcceptor)
	if !ok {
		return
	}
	srv, ln := facethttp.Server("hub-http", h)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = srv.Close()
	}()
	go m.acceptFacets(ctx, acc, ln)
	log.Printf("hub-http: serving %v over the identity plane", hubFacetALPNs())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		log.Printf("hub-http: %v", err)
	}
}

// requireGroup is the per-route gate over the facet's admission: the
// request must arrive over the facet AND its identity must carry
// group. Nothing off the facet passes — the handler behind it is
// mounted only on hubFacetMux, so there is no address-gated path to
// confuse with.
func requireGroup(group string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := facethttp.IdentityFrom(r.Context())
		if !ok || !slices.Contains(id.Groups, group) {
			http.Error(w, "forbidden: "+group+" only", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// hubFacetMux is what the hub-http facet serves. Only /config: /hosts
// and /policy never exist over the mesh (decision mdv — the beat is
// #renew + #bundle), and the public routes stay on the public listener.
func (s *server) hubFacetMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /config", requireGroup("admins", http.HandlerFunc(s.handleAdminConfig)))
	return mux
}
