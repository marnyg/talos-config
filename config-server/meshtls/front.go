package meshtls

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// Target is what a name means as an HTTP destination: the member and
// the facet its port-80 traffic rides (nodeagent.Target(name, 80)).
type Target struct {
	Member string
	Facet  string
}

// FrontOptions configures NewFront.
type FrontOptions struct {
	// Authority mints the leaves.
	Authority *Authority
	// Resolve maps a bare FQDN (workouts.gw.mesh.internal) to its HTTP
	// target, or errors (unknown name, a kind with no HTTP facet).
	Resolve func(name string) (Target, error)
	// Open returns a fresh plaintext HTTP connection to the target —
	// one stream on the pooled connection to (member, facet), wrapped
	// as net.Conn (protocol/facethttp.Dialed).
	Open func(ctx context.Context, t Target) (net.Conn, error)
	// Log; nil ⇒ log.Default().
	Log *log.Logger
}

// Front terminates TLS for flows to <fake IP>:443 and reverse-proxies
// each request as plain HTTP over the identity stream, Host untouched
// (the gateway and nginx route by it), X-Forwarded-Proto: https added.
// One http.Server for every flow, fed by a channel listener; one
// http.Transport per target so idle streams are reused per member.
type Front struct {
	auth *Authority
	res  func(string) (Target, error)
	open func(context.Context, Target) (net.Conn, error)
	log  *log.Logger

	ln  *listener
	srv *http.Server

	mu         sync.Mutex
	transports map[Target]*http.Transport
}

// NewFront builds and starts the front; Close stops it.
func NewFront(o FrontOptions) (*Front, error) {
	if o.Authority == nil || o.Resolve == nil || o.Open == nil {
		return nil, errors.New("meshtls: Authority, Resolve and Open are required")
	}
	logger := o.Log
	if logger == nil {
		logger = log.Default()
	}
	f := &Front{auth: o.Authority, res: o.Resolve, open: o.Open, log: logger, ln: newListener(), transports: map[Target]*http.Transport{}}
	rp := &httputil.ReverseProxy{
		Transport: f,
		Rewrite: func(r *httputil.ProxyRequest) {
			fc, _ := r.In.Context().Value(flowKey{}).(*flowConn)
			// Pool key only: the transport for this target ignores the
			// address and opens a stream.
			r.SetURL(&url.URL{Scheme: "http", Host: fc.target.Facet + "." + fc.target.Member})
			r.Out.Host = r.In.Host
			r.SetXForwarded() // Proto: https (r.In.TLS is set), Host, For
			// The gateway forwards anything but X-Forwarded-For and the
			// identity headers; nginx (use-forwarded-headers) believes
			// Proto and Port from it. Without Port an app would pair
			// https with nginx's own :80.
			r.Out.Header.Set("X-Forwarded-Port", "443")
		},
		// Flush as bytes arrive: the hop is kernel-local, and a long
		// poll or event stream must not sit in a buffer here.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Printf("tls: %s %s%s: %v", r.Method, r.Host, r.URL.Path, err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	f.srv = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Value(flowKey{}).(*flowConn); !ok {
				http.Error(w, "not a terminated flow", http.StatusForbidden)
				return
			}
			rp.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       5 * time.Minute,
		// The flow's name and target ride the connection: the
		// *tls.Conn the server sees wraps our flowConn.
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if tc, ok := c.(*tls.Conn); ok {
				if fc, ok := tc.NetConn().(*flowConn); ok {
					return context.WithValue(ctx, flowKey{}, fc)
				}
			}
			return ctx
		},
		// http/1.1 only (Authority.Config offers nothing else); a
		// non-nil empty map keeps Serve from wiring h2.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ErrorLog:     logger,
	}
	go func() {
		if err := f.srv.Serve(f.ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			logger.Printf("tls: serve: %v", err)
		}
	}()
	return f, nil
}

// Serve terminates one flow addressed to name and serves it until the
// client is done. It resolves the target first — a name with no HTTP
// facet, or none the directory knows, is an error before any
// handshake — then hands the connection to the server and blocks until
// the server has closed it. The caller must not close app on success.
func (f *Front) Serve(ctx context.Context, app net.Conn, name string) (Target, error) {
	t, err := f.res(name)
	if err != nil {
		return Target{}, err
	}
	fc := &flowConn{Conn: app, name: name, target: t, done: make(chan struct{})}
	tc := tls.Server(fc, f.auth.Config(name))
	if !f.ln.push(ctx, tc) {
		return t, errors.New("meshtls: front closed")
	}
	select {
	case <-fc.done:
	case <-ctx.Done():
		_ = tc.Close()
		<-fc.done
	}
	return t, nil
}

// RoundTrip implements http.RoundTripper over the per-target transports.
func (f *Front) RoundTrip(r *http.Request) (*http.Response, error) {
	fc, ok := r.Context().Value(flowKey{}).(*flowConn)
	if !ok {
		return nil, errors.New("meshtls: request off a flow")
	}
	return f.transport(fc.target).RoundTrip(r)
}

func (f *Front) transport(t Target) *http.Transport {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tr := f.transports[t]; tr != nil {
		return tr
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return f.open(ctx, t)
		},
		// The receiver bounds the pooled connection (gateway
		// ConnMaxAge 1 h); an idle stream is cheap but a stale one
		// costs a retried request, so keep few and drop them soon.
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true, // pass the browser's Accept-Encoding through
		ForceAttemptHTTP2:   false,
	}
	f.transports[t] = tr
	return tr
}

// Close stops the server and drops idle upstream streams. Flows in
// progress are closed.
func (f *Front) Close() {
	_ = f.ln.Close()
	_ = f.srv.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, tr := range f.transports {
		tr.CloseIdleConnections()
	}
}

type flowKey struct{}

// flowConn is the accepted flow with its name and target attached, and
// a done channel the server's Close trips.
type flowConn struct {
	net.Conn
	name   string
	target Target
	once   sync.Once
	done   chan struct{}
}

func (c *flowConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.done) })
	return err
}

// listener is a net.Listener fed by Serve.
type listener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newListener() *listener {
	return &listener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *listener) push(ctx context.Context, c net.Conn) bool {
	select {
	case l.conns <- c:
		return true
	case <-l.closed:
		return false
	case <-ctx.Done():
		return false
	}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *listener) Addr() net.Addr { return addr("tls-front") }

type addr string

func (a addr) Network() string { return "talos-mesh" }
func (a addr) String() string  { return string(a) }
