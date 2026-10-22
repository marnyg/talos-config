// Command vmpower is the win2k25 VM's power switch (talos-config-f0jl):
// a two-button page behind the mesh, so parking the 24 GiB guest — and
// handing its passed-through RTX 3070 back to whatever wants the GPU
// (spike dsuj) — is one click and not a git edit plus a push.
//
// It talks to the kube API with its own ServiceAccount token over plain
// net/http: no client-go, no go.sum, nothing to vendor. The authority
// it needs is two verbs on one object in one namespace (Role in
// `vms`, k8s/apps/vmpower/deployment.yaml), and it holds nothing else.
//
// The power state IS spec.runStrategy (Always | Halted). ArgoCD is told
// to ignore that one field for VirtualMachines (decision 4440,
// k8s/apps/argocd/config.yaml), which is what makes a live button
// possible at all — everything else about the VM stays git-owned, and
// dropping that jsonPointer puts the old "git is the switch" behaviour
// back on the next sync.
//
// What it deliberately does not do: auto-stop when idle. Every signal
// for "is anybody using this VM" lives outside this process (Guacamole's
// active-connection count needs Guacamole's login; the guest has no
// agent API to ask), and a wrong guess kills a 24 GiB session mid-work.
// That is a separate decision, not a default (f0jl's note).
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// The two values spec.runStrategy can take that matter here.
const (
	Always = "Always" // desired: running
	Halted = "Halted" // desired: gone
)

// vm is the slice of a KubeVirt VirtualMachine this page reads.
type vm struct {
	Spec struct {
		RunStrategy string `json:"runStrategy"`
	} `json:"spec"`
	Status struct {
		PrintableStatus string `json:"printableStatus"`
		Ready           bool   `json:"ready"`
		Created         bool   `json:"created"`
	} `json:"status"`
}

// state is what the page polls and what an action returns.
type state struct {
	VM       string `json:"vm"`
	Status   string `json:"status"`   // KubeVirt's printableStatus, verbatim
	Running  bool   `json:"running"`  // the guest is up
	Busy     bool   `json:"busy"`     // a transition is in flight
	Strategy string `json:"strategy"` // Always | Halted
	Error    string `json:"error,omitempty"`
}

type api struct {
	base      string
	namespace string
	name      string
	tokenFile string
	hc        *http.Client
}

func newAPI(base, namespace, name, tokenFile, caFile string) (*api, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("ca file has no certificates")
	}
	return &api{
		base:      strings.TrimSuffix(base, "/"),
		namespace: namespace,
		name:      name,
		tokenFile: tokenFile,
		hc: &http.Client{
			Timeout:   20 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
	}, nil
}

// do is the whole kube client: one bearer token, JSON in and out.
// The token is re-read every call because kubelet rotates the projected
// volume and a cached one expires after roughly an hour.
func (a *api) do(ctx context.Context, method, path string, patch any) (int, []byte, error) {
	tok, err := os.ReadFile(a.tokenFile)
	if err != nil {
		return 0, nil, fmt.Errorf("read token: %w", err)
	}
	var body io.Reader
	ctype := ""
	if patch != nil {
		b, err := json.Marshal(patch)
		if err != nil {
			return 0, nil, err
		}
		body = strings.NewReader(string(b))
		ctype = "application/merge-patch+json"
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, out, err
}

func (a *api) path() string {
	return fmt.Sprintf("/apis/kubevirt.io/v1/namespaces/%s/virtualmachines/%s", a.namespace, a.name)
}

// read fetches the VM and flattens it into the page's state.
func (a *api) read(ctx context.Context) (state, error) {
	code, body, err := a.do(ctx, http.MethodGet, a.path(), nil)
	if err != nil {
		return state{}, err
	}
	if code != http.StatusOK {
		return state{}, fmt.Errorf("get vm: HTTP %d: %s", code, snippet(body))
	}
	var v vm
	if err := json.Unmarshal(body, &v); err != nil {
		return state{}, fmt.Errorf("decode vm: %w", err)
	}
	s := state{
		VM:       a.name,
		Status:   v.Status.PrintableStatus,
		Running:  v.Status.Ready && v.Status.Created,
		Strategy: v.Spec.RunStrategy,
	}
	if s.Status == "" {
		s.Status = "Unknown"
	}
	switch s.Status {
	case "Starting", "Stopping", "Terminating", "Provisioning", "Restarting":
		s.Busy = true
	}
	return s, nil
}

// setRunStrategy is the power switch. Merge-patch rather than the
// `/start`+`/stop` subresources: one verb, one code path, and the
// operator's runStrategy semantics are exactly what the page shows.
func (a *api) setRunStrategy(ctx context.Context, want string) error {
	code, body, err := a.do(ctx, http.MethodPatch, a.path(), map[string]any{"spec": map[string]any{"runStrategy": want}})
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("patch vm: HTTP %d: %s", code, snippet(body))
	}
	return nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// Identity headers the gateway sets on every proxied request
// (config-server/gateway: anything a caller sent under them is
// stripped first). HeaderName is the device's durable name: the
// only honest answer to "who clicked".
const meshNameHeader = "X-Mesh-Name"

// actionHeader is what the page's fetch sends with every POST. A
// cross-origin fetch cannot add it without a CORS preflight, and
// nothing here answers OPTIONS, so a cross-site POST never reaches the
// patch. Belt to the Sec-Fetch-Site braces in sameSite.
const actionHeader = "X-Vmpower"

// sameSite is the CSRF guard. The mesh gate is ambient (an admin
// device opens this page as it is, no cookie, no login), so without
// this any web page open in a browser on such a device could POST
// /stop with a cross-site fetch. Two independent checks, either of
// which refuses: Sec-Fetch-Site (every current browser sends it; only
// same-origin / none are the page itself or a typed URL) and the
// custom header above.
func sameSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none", "":
		// "" is a non-browser caller (curl); the header check below
		// still applies to it.
	default:
		return false
	}
	return r.Header.Get(actionHeader) != ""
}

// who names the caller for the log: the device name the gateway set,
// or the socket peer when the request did not come through it.
func who(r *http.Request) string {
	if n := r.Header.Get(meshNameHeader); n != "" {
		return n
	}
	return r.RemoteAddr
}

type server struct {
	api *api
	rdp string
}

func (s *server) stateJSON(w http.ResponseWriter, st state, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(st)
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	st, err := s.api.read(r.Context())
	if err != nil {
		log.Printf("state: %v", err)
		s.stateJSON(w, state{VM: s.api.name, Status: "Unknown", Error: err.Error()}, http.StatusBadGateway)
		return
	}
	s.stateJSON(w, st, http.StatusOK)
}

// act handles both buttons: read, decide whether the request is already
// satisfied, patch, and answer with the state right after.
func (s *server) act(want string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if !sameSite(r) {
			log.Printf("%s: refused cross-site %s (Sec-Fetch-Site=%q)", who(r), want, r.Header.Get("Sec-Fetch-Site"))
			http.Error(w, "forbidden: not from the page", http.StatusForbidden)
			return
		}
		ctx := r.Context()
		cur, err := s.api.read(ctx)
		if err != nil {
			log.Printf("%s: read: %v", want, err)
			s.stateJSON(w, state{VM: s.api.name, Status: "Unknown", Error: err.Error()}, http.StatusBadGateway)
			return
		}
		if cur.Strategy == want {
			s.stateJSON(w, cur, http.StatusOK) // already there; not an error
			return
		}
		if err := s.api.setRunStrategy(ctx, want); err != nil {
			log.Printf("%s: %v", want, err)
			cur.Error = err.Error()
			s.stateJSON(w, cur, http.StatusBadGateway)
			return
		}
		log.Printf("%s: runStrategy -> %s (was %s, status %s)", who(r), want, cur.Strategy, cur.Status)
		// The operator reacts asynchronously; re-read so the page shows
		// Starting/Stopping instead of the state it asked from.
		if next, err := s.api.read(ctx); err == nil {
			cur = next
		}
		s.stateJSON(w, cur, http.StatusOK)
	}
}

func main() {
	listen := flag.String("listen", ":8080", "listen address")
	namespace := flag.String("namespace", "vms", "namespace of the VirtualMachine")
	name := flag.String("vm", "win2k25", "VirtualMachine name")
	base := flag.String("api", "https://kubernetes.default.svc", "kube API base URL")
	tokenFile := flag.String("token-file", "/var/run/secrets/kubernetes.io/serviceaccount/token", "ServiceAccount token file")
	caFile := flag.String("ca-file", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", "kube API CA file")
	rdp := flag.String("rdp-url", "http://guac.gw.mesh.internal/", "where the RDP page lives, for the link")
	flag.Parse()

	a, err := newAPI(*base, *namespace, *name, *tokenFile, *caFile)
	if err != nil {
		log.Fatalf("vmpower: %v", err)
	}
	s := &server{api: a, rdp: *rdp}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page(*name, *rdp))
	})
	mux.HandleFunc("/state", s.getState)
	mux.HandleFunc("/start", s.act(Always))
	mux.HandleFunc("/stop", s.act(Halted))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// Every request is one short JSON round trip (the kube client
		// itself gives up at 20 s); nginx keeps upstream connections
		// open between the page's 3 s polls.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("vmpower: %s/%s, listening on %s", *namespace, *name, *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("vmpower: %v", err)
	}
}
