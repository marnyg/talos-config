package relaygate

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

const (
	// AdmitPath is where an actor presents its chain: POST, the body an
	// EncodePresentation, the reply `{"exp": <unix>}`.
	AdmitPath = "/relay/admit"
	// RelayPath is the relay protocol's WebSocket upgrade.
	RelayPath = "/relay"
	// HookPath is where Service serves Hook, on its loopback listener.
	HookPath = "/access"
	// HookIDHeader carries the connecting key on the relay's access-hook
	// request. iroh-relay 1.1.0's doc comment says X-Iroh-Endpoint-Id;
	// the binary sends this.
	HookIDHeader = "X-Iroh-NodeId"

	// maxPresentation bounds an AdmitPath body: MaxChain certs fit in a
	// fraction of it.
	maxPresentation = 64 << 10
	// recheck caps how long a proxied connection goes without the gate
	// being asked again, whatever its credential's expiry says.
	recheck = time.Minute
)

// probePaths are the relay's unauthenticated net-report probes. They
// carry no claim and reach the relay as they are.
var probePaths = []string{"/ping", "/generate_204"}

// Claim is the bearer an actor binds its endpoint with
// (irohtransport.Options.RelayToken): the id the connection is for. It
// is how the proxy — which cannot see the key the relay authenticates —
// knows whose expiry to hold a connection to. A claim is not a
// credential: the hook admits only the key that proved itself.
//
// It is unsigned (ADR-0012, not decided): an actor that is itself
// admitted could claim another id and keep ITS connection past its own
// expiry, until it next reconnects.
func Claim(id cert.ActorID) string { return string(id) }

// claimOf reads the claim off an upgrade request.
func claimOf(r *http.Request) (cert.ActorID, bool) {
	v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	id := cert.ActorID(strings.TrimSpace(v))
	return id, ok && relayKey(id)
}

// Server wires a Gate to one relay process.
type Server struct {
	Gate *Gate
	// Backend is the relay's plain-HTTP listener (loopback).
	Backend *url.URL
	// HookToken is the bearer the relay presents on its access-hook
	// requests (IROH_RELAY_HTTP_BEARER_TOKEN on the child). Required:
	// Hook refuses everything without it.
	HookToken string
	// Log receives one line per refusal class and per cut. nil ⇒ none.
	Log *slog.Logger
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Public is the relay's whole public surface: the upgrade (admitted
// claims only, ended at expiry), the probes, and AdmitPath. Nothing
// else the relay binary serves is reachable.
func (s *Server) Public() http.Handler {
	rp := httputil.NewSingleHostReverseProxy(s.Backend)
	// The relay is on loopback: no proxy from the environment, a short
	// dial, and nothing that would time out a long-lived upgrade.
	rp.Transport = &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	// 503 while the relay is down (it is restarting): an iroh client
	// then retries its home relay rather than giving it up.
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if !errors.Is(err, context.Canceled) {
			s.log().Warn("relaygate: relay unreachable", "path", r.URL.Path, "err", err)
		}
		http.Error(w, "relay unavailable", http.StatusServiceUnavailable)
	}
	mux := http.NewServeMux()
	for _, p := range probePaths {
		mux.Handle(p, rp)
	}
	mux.HandleFunc(AdmitPath, s.admit)
	mux.HandleFunc(RelayPath, func(w http.ResponseWriter, r *http.Request) {
		id, ok := claimOf(r)
		if !ok {
			http.Error(w, "no claim", http.StatusUnauthorized)
			return
		}
		if _, ok := s.Gate.Admitted(id); !ok {
			http.Error(w, "not admitted", http.StatusUnauthorized)
			return
		}
		// The connection lives while the credential does. ReverseProxy
		// closes the backend when this context ends.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go s.hold(ctx, cancel, id)
		r = r.WithContext(ctx)
		r.Header.Del("Authorization") // the claim is for the gate, not the relay
		rp.ServeHTTP(w, r)
	})
	return mux
}

// hold ends the connection for id (cancel) once the gate stops
// admitting it. A renewal presented before the deadline extends it:
// the gate is asked again at each deadline, and at least every recheck.
func (s *Server) hold(ctx context.Context, cancel context.CancelFunc, id cert.ActorID) {
	for {
		until, ok := s.Gate.Admitted(id)
		if !ok {
			s.log().Info("relaygate: credential expired, connection ended", "id", id)
			cancel()
			return
		}
		wait := recheck
		if left := until - s.Gate.now(); left < int64(recheck/time.Second) {
			wait = time.Duration(max(left, 1)) * time.Second
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// admit is AdmitPath.
func (s *Server) admit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST a presentation", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPresentation))
	if err != nil {
		http.Error(w, "presentation too large", http.StatusRequestEntityTooLarge)
		return
	}
	p, err := DecodePresentation(body)
	if err != nil {
		http.Error(w, "bad presentation", http.StatusBadRequest)
		return
	}
	exp, err := s.Gate.Present(p)
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"exp": exp})
	case errors.Is(err, ErrBadPresentation):
		http.Error(w, "bad presentation", http.StatusBadRequest)
	case errors.Is(err, ErrFull):
		w.Header().Set("Retry-After", "60")
		http.Error(w, "table full", http.StatusServiceUnavailable)
	default:
		// The reason stays here: the endpoint must not explain to a
		// stranger how close a forged chain came.
		s.log().Debug("relaygate: presentation refused", "id", p.ID, "err", err)
		http.Error(w, "refused", http.StatusForbidden)
	}
}

// Hook is the relay's access hook ([access.http] url): "may the key
// that just proved itself hold a connection?" — 200 `true`, or 403
// `false`. Serve it on a loopback listener of its own; it is a
// contract between two processes of one machine, and the bearer keeps
// any other local process from using it as a membership oracle.
func (s *Server) Hook() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.HookToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.HookToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := cert.ActorID("ed:" + strings.ToLower(strings.TrimSpace(r.Header.Get(HookIDHeader))))
		w.Header().Set("Content-Type", "text/plain")
		if _, ok := s.Gate.Admitted(id); !relayKey(id) || !ok {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "false")
			return
		}
		_, _ = io.WriteString(w, "true")
	})
}
