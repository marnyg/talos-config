package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/driver"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/provisioner"
)

const t0 int64 = 1_700_000_000

var image = "ghcr.io/x/child@sha256:" + strings.Repeat("a", 64)

// apiServer is the slice of the Jobs API the driver uses, in memory:
// create, get, merge-patch, delete, list by label. It checks the
// bearer token and the request shapes the way the real server does.
type apiServer struct {
	mu       sync.Mutex
	jobs     map[string]map[string]any
	deleted  []string
	patches  []map[string]any
	requests []string
	// startTime, if set, is stamped on every job as the controller does.
	startTime *time.Time
}

func newAPI(t *testing.T) (*apiServer, *httptest.Server) {
	s := &apiServer{jobs: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *apiServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	const base = "/apis/batch/v1/namespaces/sap/jobs"
	if !strings.HasPrefix(r.URL.Path, base) {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodPost && name == "":
		var j map[string]any
		if err := json.Unmarshal(body, &j); err != nil {
			http.Error(w, `{"message":"bad json"}`, http.StatusBadRequest)
			return
		}
		md := j["metadata"].(map[string]any)
		n := md["name"].(string)
		if _, ok := s.jobs[n]; ok {
			http.Error(w, `{"message":"jobs.batch \"`+n+`\" already exists"}`, http.StatusConflict)
			return
		}
		if v := j["spec"].(map[string]any)["activeDeadlineSeconds"].(float64); v <= 0 {
			http.Error(w, `{"message":"activeDeadlineSeconds must be positive"}`, http.StatusUnprocessableEntity)
			return
		}
		md["creationTimestamp"] = time.Unix(t0, 0).UTC().Format(time.RFC3339)
		if s.startTime != nil {
			j["status"] = map[string]any{"startTime": s.startTime.UTC().Format(time.RFC3339)}
		}
		s.jobs[n] = j
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(j)
	case r.Method == http.MethodGet && name == "":
		if r.URL.Query().Get("labelSelector") != provisioner.LabelLease {
			http.Error(w, `{"message":"unexpected selector"}`, http.StatusBadRequest)
			return
		}
		items := []any{}
		for _, j := range s.jobs {
			items = append(items, j)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	case r.Method == http.MethodGet:
		j, ok := s.jobs[name]
		if !ok {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(j)
	case r.Method == http.MethodPatch:
		if r.Header.Get("Content-Type") != "application/merge-patch+json" {
			http.Error(w, `{"message":"unsupported media type"}`, http.StatusUnsupportedMediaType)
			return
		}
		j, ok := s.jobs[name]
		if !ok {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		var p map[string]any
		_ = json.Unmarshal(body, &p)
		s.patches = append(s.patches, p)
		for k, v := range p["spec"].(map[string]any) {
			j["spec"].(map[string]any)[k] = v
		}
		_ = json.NewEncoder(w).Encode(j)
	case r.Method == http.MethodDelete:
		if _, ok := s.jobs[name]; !ok {
			http.Error(w, `{"message":"jobs.batch \"`+name+`\" not found"}`, http.StatusNotFound)
			return
		}
		delete(s.jobs, name)
		s.deleted = append(s.deleted, name)
		_, _ = w.Write([]byte(`{"status":"Success"}`))
	default:
		http.Error(w, `{"message":"method"}`, http.StatusMethodNotAllowed)
	}
}

func (s *apiServer) job(t *testing.T, name string) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[name]
	if !ok {
		t.Fatalf("no job %s", name)
	}
	return j
}

func newDriver(t *testing.T, srv *httptest.Server) *Driver {
	t.Helper()
	d, err := New(Config{Host: srv.URL, Token: "tok", Namespace: "sap", Now: func() int64 { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestStartShape: the Job carries the lease label, the owner
// annotation, the digest image, the intro env, the deadline from the
// driver's clock, restricted pod security, and no SA token.
func TestStartShape(t *testing.T) {
	api, srv := newAPI(t)
	d := newDriver(t, srv)
	h, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image, Params: []byte(`{"nonce":"n"}`), Until: t0 + 600})
	if err != nil {
		t.Fatal(err)
	}
	if h != "sap-0011" {
		t.Fatalf("handle %q", h)
	}
	j := api.job(t, "sap-0011")
	md := j["metadata"].(map[string]any)
	if md["labels"].(map[string]any)[provisioner.LabelLease] != "0011" || md["annotations"].(map[string]any)[provisioner.LabelOwner] != "ed:ab" {
		t.Fatalf("metadata %v", md)
	}
	spec := j["spec"].(map[string]any)
	if spec["activeDeadlineSeconds"].(float64) != 600 || spec["backoffLimit"].(float64) != 0 || spec["ttlSecondsAfterFinished"].(float64) != float64(DefaultTTLAfterFinished) {
		t.Fatalf("spec %v", spec)
	}
	pod := spec["template"].(map[string]any)["spec"].(map[string]any)
	if pod["restartPolicy"] != "Never" || pod["automountServiceAccountToken"] != false {
		t.Fatalf("pod %v", pod)
	}
	if pod["securityContext"].(map[string]any)["runAsNonRoot"] != true {
		t.Fatalf("pod securityContext %v", pod["securityContext"])
	}
	c := pod["containers"].([]any)[0].(map[string]any)
	if c["image"] != image {
		t.Fatalf("image %v", c["image"])
	}
	env := c["env"].([]any)[0].(map[string]any)
	if env["name"] != driver.ParamsEnv || env["value"] != `{"nonce":"n"}` {
		t.Fatalf("env %v", env)
	}
	if c["securityContext"].(map[string]any)["allowPrivilegeEscalation"] != false {
		t.Fatalf("container securityContext %v", c["securityContext"])
	}

	// Shape rules the driver enforces before the API sees anything.
	if _, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0022", Image: "child:latest", Until: t0 + 600}); err == nil {
		t.Fatal("image by tag accepted")
	}
	// A deadline already past still submits (floored at 1 s): the
	// provisioner checked until > now; the platform lapses it at once.
	if _, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0033", Owner: "ed:ab", Image: image, Until: t0 - 5}); err != nil {
		t.Fatal(err)
	}
	if v := api.job(t, "sap-0033")["spec"].(map[string]any)["activeDeadlineSeconds"].(float64); v != 1 {
		t.Fatalf("floored deadline %v", v)
	}
	// A second Start for the same lease is the platform's conflict.
	_, err = d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image, Until: t0 + 600})
	var ae *apiError
	if !errors.As(err, &ae) || ae.Code != http.StatusConflict {
		t.Fatalf("duplicate start: %v", err)
	}
}

// TestExtend: the patch is until − startTime when the controller has
// stamped one, until − creationTimestamp before that; shortening is
// allowed; a Job that vanished is the API's 404.
func TestExtend(t *testing.T) {
	api, srv := newAPI(t)
	d := newDriver(t, srv)
	h, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image, Until: t0 + 600})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Extend(context.Background(), h, t0+7200); err != nil {
		t.Fatal(err)
	}
	if v := api.job(t, "sap-0011")["spec"].(map[string]any)["activeDeadlineSeconds"].(float64); v != 7200 {
		t.Fatalf("deadline from creationTimestamp %v", v)
	}

	st := time.Unix(t0+30, 0)
	api.mu.Lock()
	api.jobs["sap-0011"]["status"] = map[string]any{"startTime": st.UTC().Format(time.RFC3339)}
	api.mu.Unlock()
	if err := d.Extend(context.Background(), h, t0+7200); err != nil {
		t.Fatal(err)
	}
	if v := api.job(t, "sap-0011")["spec"].(map[string]any)["activeDeadlineSeconds"].(float64); v != 7170 {
		t.Fatalf("deadline from startTime %v", v)
	}
	if err := d.Extend(context.Background(), h, t0+60); err != nil {
		t.Fatal(err)
	}
	if v := api.job(t, "sap-0011")["spec"].(map[string]any)["activeDeadlineSeconds"].(float64); v != 30 {
		t.Fatalf("shortened deadline %v", v)
	}

	err = d.Extend(context.Background(), "sap-gone", t0+7200)
	var ae *apiError
	if !errors.As(err, &ae) || ae.Code != http.StatusNotFound {
		t.Fatalf("extend of a vanished job: %v", err)
	}
}

// TestKillAndList: Kill deletes with background propagation and treats
// an already-gone Job as done; List returns the unfinished labelled
// Jobs with lease, owner, image and handle, and skips finished ones.
func TestKillAndList(t *testing.T) {
	api, srv := newAPI(t)
	d := newDriver(t, srv)
	for _, l := range []string{"0011", "0022", "0033"} {
		if _, err := d.Start(context.Background(), provisioner.StartSpec{Lease: l, Owner: cert.ActorID("ed:" + l), Image: image, Until: t0 + 600}); err != nil {
			t.Fatal(err)
		}
	}
	api.mu.Lock()
	api.jobs["sap-0033"]["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True"}}}
	api.mu.Unlock()

	if err := d.Kill(context.Background(), "sap-0022"); err != nil {
		t.Fatal(err)
	}
	if err := d.Kill(context.Background(), "sap-0022"); err != nil {
		t.Fatalf("kill of a gone job: %v", err)
	}
	api.mu.Lock()
	deleted := append([]string(nil), api.deleted...)
	api.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != "sap-0022" {
		t.Fatalf("deleted %v", deleted)
	}

	got, err := d.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (provisioner.Running{Lease: "0011", Owner: "ed:0011", Image: image, Handle: "sap-0011"}) {
		t.Fatalf("list %+v", got)
	}
}

// TestAuthAndErrors: the bearer token is sent; a refusal is surfaced
// with the API's message, never swallowed.
func TestAuthAndErrors(t *testing.T) {
	_, srv := newAPI(t)
	d, err := New(Config{Host: srv.URL, Token: "wrong", Namespace: "sap"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image, Until: time.Now().Unix() + 600})
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("unauthorised start: %v", err)
	}
	if _, err := d.List(context.Background()); err == nil {
		t.Fatal("unauthorised list succeeded")
	}
	if _, err := New(Config{Host: srv.URL}); err == nil {
		t.Fatal("config without a namespace accepted")
	}
	if _, err := New(Config{Host: srv.URL, Namespace: "sap", CA: []byte("not pem")}); err == nil {
		t.Fatal("non-PEM CA accepted")
	}
}

// TestInCluster: without the env this is not a cluster.
func TestInCluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if _, err := InCluster(); err == nil {
		t.Fatal("InCluster outside a cluster succeeded")
	}
}
