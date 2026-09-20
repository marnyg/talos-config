// Package gateway is the in-cluster gateway (Mesh v3 P2.3,
// talos-config-359.9.3): the member actor in front of Kubernetes
// Services, the mesh side of ingress (revises ADR-0009) and the
// successor of ADR-0007's cert-group + source-address inference.
//
// It is the node agent runtime under Kind gateway. What differs is the
// accept table: `ingress-http` is terminated here — every admitted
// stream is one HTTP connection, reverse-proxied to ingress-nginx with
// the Host header untouched (nginx keeps routing by it; auth_request
// and the SIWE→OIDC bridge keep gating at the app layer, ADR-0010) and
// the caller's verified identity injected as headers; `jellyfin` is a
// raw splice to the Jellyfin Service for the TV apps (P2.4).
//
// Authorization is the network layer only: cert.Authorize over the
// caller's bundle, rooted in this gateway's own consent, decides once
// per connection (nodeagent.Agent.Authorize), and the connection is
// bounded (ConnMaxAge) so a cert's expiry has a ceiling. No
// per-session login here; device custody is access for the cert's
// lifetime or until the blocklist. The headers COMPLEMENT SIWE — an
// app may map them (oauth2-proxy can), none is required to trust them
// for a session. Past this process the identity is ambient: the
// headers are only as good as the path they arrived on (talos-config-1gv:
// ingress-nginx honours them from the pod network alone until it
// leaves hostNetwork).
package gateway

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/facethttp"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/protocol/cert"
)

// The identity headers: the caller's key, the durable name and the
// groups from its member cert (the only source Authorize reads them
// from). Anything a caller sent under these names is dropped first.
const (
	HeaderNode   = "X-Mesh-Node"
	HeaderName   = "X-Mesh-Name"
	HeaderGroups = "X-Mesh-Groups"
)

// Headers lists the identity headers, for the strip on the way in.
var Headers = []string{HeaderNode, HeaderName, HeaderGroups}

// DefaultConnMaxAge is the admitted-connection bound (domain model:
// the gateway bounds stream lifetime so expiry has a ceiling).
const DefaultConnMaxAge = time.Hour

// Proxy is the ingress-http handler: a reverse proxy to upstream
// (ingress-nginx's Service) that keeps the inbound Host and sets the
// identity headers from the connection's admitted identity. A request
// that did not arrive over a facet — no identity in its context — is
// refused: this handler is only ever mounted behind one, and a mount
// elsewhere must fail closed rather than proxy anonymously.
func Proxy(upstream *url.URL) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Host = r.In.Host // nginx routes by it
			r.SetXForwarded()      // X-Forwarded-Host/Proto for oauth2-proxy's redirects
			// RemoteAddr is the caller's key ("ed:…"), which SplitHostPort
			// cheerfully splits at the colon: no address to forward. The
			// key travels as HeaderNode; nginx adds its own -For.
			r.Out.Header.Del("X-Forwarded-For")
			for _, h := range Headers {
				r.Out.Header.Del(h)
			}
			id, _ := facethttp.IdentityFrom(r.In.Context())
			r.Out.Header.Set(HeaderNode, string(id.Key))
			r.Out.Header.Set(HeaderName, id.Name)
			r.Out.Header.Set(HeaderGroups, strings.Join(id.Groups, ","))
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("gateway: %s %s%s: %v", r.Method, r.Host, r.URL.Path, err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := facethttp.IdentityFrom(r.Context()); !ok {
			http.Error(w, "forbidden: not over a facet", http.StatusForbidden)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

// HTTPFacet serves h over admitted streams of facet: the returned
// StreamHandler goes into nodeagent.Options.Serve, and stop ends the
// server. One http.Server per facet, fed by facethttp.Listener.
func HTTPFacet(facet string, h http.Handler, logger *log.Logger) (handler nodeagent.StreamHandler, stop func()) {
	srv, ln := facethttp.Server(facet, h)
	if logger != nil {
		srv.ErrorLog = logger
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Printf("gateway: %s: %v", facet, err)
		}
	}()
	handler = func(ctx context.Context, stream io.ReadWriteCloser, id cert.Identity, peer cert.ActorID) {
		if !ln.Push(ctx, stream, id, peer) {
			_ = stream.Close()
		}
	}
	stop = func() {
		_ = ln.Close()
		_ = srv.Close()
	}
	return handler, stop
}
