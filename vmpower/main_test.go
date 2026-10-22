package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeKube is the one object the binary touches, with enough state to
// see what it did: the current runStrategy and every patch received.
type fakeKube struct {
	mu       sync.Mutex
	strategy string
	status   string
	ready    bool
	patches  []string
	lastAuth string
}

func (f *fakeKube) handler(t *testing.T) http.Handler {
	const path = "/apis/kubevirt.io/v1/namespaces/vms/virtualmachines/win2k25"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		f.lastAuth = r.Header.Get("Authorization")
		switch r.Method {
		case http.MethodGet:
		case http.MethodPatch:
			if ct := r.Header.Get("Content-Type"); ct != "application/merge-patch+json" {
				t.Errorf("patch content-type %q", ct)
			}
			var p struct {
				Spec struct {
					RunStrategy string `json:"runStrategy"`
				} `json:"spec"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Errorf("decode patch: %v", err)
			}
			f.patches = append(f.patches, p.Spec.RunStrategy)
			f.strategy = p.Spec.RunStrategy
			// The operator reacts asynchronously; mimic the first
			// observable step so the page sees Busy.
			if p.Spec.RunStrategy == Halted {
				f.status = "Stopping"
			} else {
				f.status = "Starting"
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"spec":   map[string]any{"runStrategy": f.strategy},
			"status": map[string]any{"printableStatus": f.status, "ready": f.ready, "created": f.ready},
		})
	})
}

// newTestServer wires a server against the fake, using the fake's own
// TLS cert as the "cluster CA" and a token file on disk.
func newTestServer(t *testing.T, f *fakeKube) (*server, *httptest.Server) {
	t.Helper()
	ks := httptest.NewTLSServer(f.handler(t))
	t.Cleanup(ks.Close)
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	if err := os.WriteFile(tok, []byte("tok-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := newAPI(ks.URL, "vms", "win2k25", tok, writeCA(t, dir, ks))
	if err != nil {
		t.Fatal(err)
	}
	return &server{api: a, rdp: "http://guac/"}, ks
}

func writeCA(t *testing.T, dir string, ks *httptest.Server) string {
	t.Helper()
	cert := ks.Certificate()
	pem := "-----BEGIN CERTIFICATE-----\n" + base64Lines(cert.Raw) + "-----END CERTIFICATE-----\n"
	p := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(p, []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) state {
	t.Helper()
	var st state
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return st
}

func post(path string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

var fromPage = map[string]string{"Sec-Fetch-Site": "same-origin", "X-Vmpower": "1", "X-Mesh-Name": "laptop"}

func TestStateMapsRunningAndBusy(t *testing.T) {
	f := &fakeKube{strategy: Always, status: "Running", ready: true}
	s, _ := newTestServer(t, f)

	rr := httptest.NewRecorder()
	s.getState(rr, httptest.NewRequest(http.MethodGet, "/state", nil))
	st := decode(t, rr)
	if rr.Code != 200 || !st.Running || st.Busy || st.Strategy != Always || st.Status != "Running" {
		t.Fatalf("running: %d %+v", rr.Code, st)
	}
	if !strings.HasPrefix(f.lastAuth, "Bearer tok-1") || strings.HasSuffix(f.lastAuth, "\n") {
		t.Fatalf("token not read/trimmed: %q", f.lastAuth)
	}

	f.status, f.ready = "Stopping", false
	rr = httptest.NewRecorder()
	s.getState(rr, httptest.NewRequest(http.MethodGet, "/state", nil))
	if st := decode(t, rr); !st.Busy || st.Running {
		t.Fatalf("stopping: %+v", st)
	}

	f.status = ""
	rr = httptest.NewRecorder()
	s.getState(rr, httptest.NewRequest(http.MethodGet, "/state", nil))
	if st := decode(t, rr); st.Status != "Unknown" {
		t.Fatalf("empty status: %+v", st)
	}
}

func TestStopPatchesThenStartIsNoopWhenAlreadyThere(t *testing.T) {
	f := &fakeKube{strategy: Always, status: "Running", ready: true}
	s, _ := newTestServer(t, f)

	rr := httptest.NewRecorder()
	s.act(Halted)(rr, post("/stop", fromPage))
	st := decode(t, rr)
	if rr.Code != 200 || st.Strategy != Halted || !st.Busy || st.Status != "Stopping" {
		t.Fatalf("stop: %d %+v", rr.Code, st)
	}
	if len(f.patches) != 1 || f.patches[0] != Halted {
		t.Fatalf("patches: %v", f.patches)
	}

	// Same value again: answered with the state, no second patch.
	rr = httptest.NewRecorder()
	s.act(Halted)(rr, post("/stop", fromPage))
	if rr.Code != 200 || len(f.patches) != 1 {
		t.Fatalf("repeat stop: %d patches=%v", rr.Code, f.patches)
	}

	rr = httptest.NewRecorder()
	s.act(Always)(rr, post("/start", fromPage))
	if st := decode(t, rr); rr.Code != 200 || st.Strategy != Always || len(f.patches) != 2 {
		t.Fatalf("start: %d %+v patches=%v", rr.Code, st, f.patches)
	}
}

func TestCrossSitePostIsRefused(t *testing.T) {
	f := &fakeKube{strategy: Always, status: "Running", ready: true}
	s, _ := newTestServer(t, f)

	cases := map[string]map[string]string{
		"cross-site fetch":         {"Sec-Fetch-Site": "cross-site", "X-Vmpower": "1"},
		"same-site subdomain":      {"Sec-Fetch-Site": "same-site", "X-Vmpower": "1"},
		"no header, same-origin":   {"Sec-Fetch-Site": "same-origin"},
		"no header, old browser":   {},
		"form post from elsewhere": {"Sec-Fetch-Site": "cross-site", "Content-Type": "application/x-www-form-urlencoded"},
	}
	for name, hdr := range cases {
		rr := httptest.NewRecorder()
		s.act(Halted)(rr, post("/stop", hdr))
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", name, rr.Code)
		}
	}
	if len(f.patches) != 0 {
		t.Fatalf("a refused request patched: %v", f.patches)
	}

	// curl with the header is fine: the gate in front decides who may
	// reach the page at all; this guard is only about browsers.
	rr := httptest.NewRecorder()
	s.act(Halted)(rr, post("/stop", map[string]string{"X-Vmpower": "1"}))
	if rr.Code != 200 {
		t.Fatalf("curl-style: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	s.act(Halted)(rr, httptest.NewRequest(http.MethodGet, "/stop", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /stop: %d", rr.Code)
	}
}

func TestKubeDownIsBadGatewayWithError(t *testing.T) {
	f := &fakeKube{strategy: Always, status: "Running", ready: true}
	s, ks := newTestServer(t, f)
	ks.Close()

	rr := httptest.NewRecorder()
	s.getState(rr, httptest.NewRequest(http.MethodGet, "/state", nil))
	if st := decode(t, rr); rr.Code != http.StatusBadGateway || st.Error == "" || st.Status != "Unknown" {
		t.Fatalf("down: %d %+v", rr.Code, st)
	}
}

func TestPageCarriesNameAndLink(t *testing.T) {
	p := page("win2k25", "http://guac.gw.mesh.internal/")
	for _, want := range []string{"<title>win2k25 power</title>", `href="http://guac.gw.mesh.internal/"`, `"X-Vmpower": "1"`, "confirm("} {
		if !strings.Contains(p, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func base64Lines(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	var sb strings.Builder
	for len(enc) > 64 {
		sb.WriteString(enc[:64])
		sb.WriteByte('\n')
		enc = enc[64:]
	}
	sb.WriteString(enc)
	sb.WriteByte('\n')
	return sb.String()
}
