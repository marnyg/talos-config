package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/driver"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/provisioner"
)

var image = "ghcr.io/x/child@sha256:" + strings.Repeat("a", 64)

// engine is the slice of the Engine API the driver uses, in memory.
type engine struct {
	mu         sync.Mutex
	images     map[string]bool
	containers map[string]map[string]any // name → create body + state
	pulls      []string
	removed    []string
	requests   []string
	pullFails  bool
	startFails bool
}

func newEngine(t *testing.T) (*engine, *httptest.Server) {
	e := &engine{images: map[string]bool{}, containers: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(srv.Close)
	return e, srv
}

func (e *engine) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests = append(e.requests, r.Method+" "+r.URL.RequestURI())
	p := strings.TrimPrefix(r.URL.Path, "/"+APIVersion)
	if p == r.URL.Path {
		http.Error(w, `{"message":"unversioned"}`, http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodPost && p == "/images/create":
		ref := r.URL.Query().Get("fromImage") + "@" + r.URL.Query().Get("tag")
		e.pulls = append(e.pulls, ref)
		w.WriteHeader(http.StatusOK)
		if e.pullFails {
			_, _ = w.Write([]byte(`{"status":"Pulling"}` + "\n" + `{"error":"manifest unknown"}` + "\n"))
			return
		}
		e.images[ref] = true
		_, _ = w.Write([]byte(`{"status":"Pulling"}` + "\n" + `{"status":"Downloaded"}` + "\n"))
	case r.Method == http.MethodPost && p == "/containers/create":
		var c map[string]any
		_ = json.Unmarshal(body, &c)
		if !e.images[c["Image"].(string)] {
			http.Error(w, `{"message":"No such image: `+c["Image"].(string)+`"}`, http.StatusNotFound)
			return
		}
		name := r.URL.Query().Get("name")
		if _, ok := e.containers[name]; ok {
			http.Error(w, `{"message":"Conflict. The container name is already in use"}`, http.StatusConflict)
			return
		}
		c["State"] = "created"
		e.containers[name] = c
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"` + strings.Repeat("0", 64) + `","Warnings":[]}`))
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/start"):
		name := strings.TrimSuffix(strings.TrimPrefix(p, "/containers/"), "/start")
		c, ok := e.containers[name]
		if !ok {
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
			return
		}
		if e.startFails {
			http.Error(w, `{"message":"OCI runtime create failed"}`, http.StatusInternalServerError)
			return
		}
		c["State"] = "running"
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete:
		name := strings.TrimPrefix(p, "/containers/")
		if r.URL.Query().Get("force") != "true" {
			http.Error(w, `{"message":"force required"}`, http.StatusConflict)
			return
		}
		if _, ok := e.containers[name]; !ok {
			http.Error(w, `{"message":"No such container: `+name+`"}`, http.StatusNotFound)
			return
		}
		delete(e.containers, name)
		e.removed = append(e.removed, name)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && p == "/containers/json":
		var f map[string][]string
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f)
		items := []any{}
		for name, c := range e.containers {
			labels, _ := c["Labels"].(map[string]any)
			if _, ok := labels[f["label"][0]]; !ok || c["State"] != f["status"][0] {
				continue
			}
			items = append(items, map[string]any{"Names": []string{"/" + name}, "Image": c["Image"], "Labels": labels, "State": c["State"]})
		}
		_ = json.NewEncoder(w).Encode(items)
	default:
		http.Error(w, `{"message":"page not found"}`, http.StatusNotFound)
	}
}

func newDriver(t *testing.T, srv *httptest.Server) *Driver {
	t.Helper()
	d, err := New(Config{Host: "tcp://" + strings.TrimPrefix(srv.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestStart: an absent image is pulled by digest then created; the
// container carries labels, env, AutoRemove, and is started. A second
// Start of the same lease is the daemon's conflict; an image by tag
// never reaches the daemon.
func TestStart(t *testing.T) {
	e, srv := newEngine(t)
	d := newDriver(t, srv)
	h, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image, Params: []byte(`{"nonce":"n"}`), Until: 1})
	if err != nil {
		t.Fatal(err)
	}
	if h != "sap-0011" {
		t.Fatalf("handle %q", h)
	}
	e.mu.Lock()
	c := e.containers["sap-0011"]
	pulls := append([]string(nil), e.pulls...)
	e.mu.Unlock()
	if len(pulls) != 1 || pulls[0] != image {
		t.Fatalf("pulls %v", pulls)
	}
	if c["State"] != "running" || c["Image"] != image {
		t.Fatalf("container %v", c)
	}
	labels := c["Labels"].(map[string]any)
	if labels[provisioner.LabelLease] != "0011" || labels[provisioner.LabelOwner] != "ed:ab" {
		t.Fatalf("labels %v", labels)
	}
	if env := c["Env"].([]any); len(env) != 1 || env[0] != driver.ParamsEnv+`={"nonce":"n"}` {
		t.Fatalf("env %v", env)
	}
	if c["HostConfig"].(map[string]any)["AutoRemove"] != true {
		t.Fatalf("hostconfig %v", c["HostConfig"])
	}

	// Image present now: no second pull.
	if _, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0022", Owner: "ed:ab", Image: image}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	n := len(e.pulls)
	e.mu.Unlock()
	if n != 1 {
		t.Fatalf("pulled again: %d", n)
	}

	_, err = d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image})
	var ae *apiError
	if !errors.As(err, &ae) || ae.Code != http.StatusConflict {
		t.Fatalf("duplicate start: %v", err)
	}
	if _, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0033", Image: "child:latest"}); err == nil {
		t.Fatal("image by tag accepted")
	}
	e.mu.Lock()
	reqs := strings.Join(e.requests, "\n")
	e.mu.Unlock()
	if strings.Contains(reqs, "0033") {
		t.Fatal("a refused image reached the daemon")
	}
}

// TestStartFailures: a pull that fails inline under a 200 is the
// error; a create that succeeds but a start that fails removes the
// container so List never adopts it.
func TestStartFailures(t *testing.T) {
	e, srv := newEngine(t)
	d := newDriver(t, srv)
	e.mu.Lock()
	e.pullFails = true
	e.mu.Unlock()
	_, err := d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image})
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("failed pull: %v", err)
	}

	e.mu.Lock()
	e.pullFails, e.startFails = false, true
	e.mu.Unlock()
	_, err = d.Start(context.Background(), provisioner.StartSpec{Lease: "0011", Owner: "ed:ab", Image: image})
	if err == nil || !strings.Contains(err.Error(), "OCI runtime") {
		t.Fatalf("failed start: %v", err)
	}
	e.mu.Lock()
	_, left := e.containers["sap-0011"]
	removed := append([]string(nil), e.removed...)
	e.mu.Unlock()
	if left || len(removed) != 1 || removed[0] != "sap-0011" {
		t.Fatalf("created-but-unstarted container left behind: left=%v removed=%v", left, removed)
	}
	got, err := d.List(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("list after a failed start: %+v %v", got, err)
	}
}

// TestExtendKillList: Extend touches nothing; Kill is rm -f and a gone
// container is a done kill; List reports running labelled containers
// with lease, owner, image, handle.
func TestExtendKillList(t *testing.T) {
	e, srv := newEngine(t)
	d := newDriver(t, srv)
	for _, l := range []string{"0011", "0022"} {
		if _, err := d.Start(context.Background(), provisioner.StartSpec{Lease: l, Owner: cert.ActorID("ed:" + l), Image: image}); err != nil {
			t.Fatal(err)
		}
	}
	e.mu.Lock()
	before := len(e.requests)
	e.mu.Unlock()
	if err := d.Extend(context.Background(), "sap-0011", time.Now().Unix()+600); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	after := len(e.requests)
	e.mu.Unlock()
	if after != before {
		t.Fatal("Extend reached the daemon")
	}

	if err := d.Kill(context.Background(), "sap-0022"); err != nil {
		t.Fatal(err)
	}
	if err := d.Kill(context.Background(), "sap-0022"); err != nil {
		t.Fatalf("kill of a gone container: %v", err)
	}
	got, err := d.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (provisioner.Running{Lease: "0011", Owner: "ed:0011", Image: image, Handle: "sap-0011"}) {
		t.Fatalf("list %+v", got)
	}
}

// TestConfig: the host forms accepted and refused.
func TestConfig(t *testing.T) {
	for _, h := range []string{"unix:///var/run/docker.sock", "tcp://127.0.0.1:2375"} {
		if _, err := New(Config{Host: h}); err != nil {
			t.Fatalf("%s: %v", h, err)
		}
	}
	if _, err := New(Config{Host: "ssh://host"}); err == nil {
		t.Fatal("ssh host accepted")
	}
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	if d, err := New(Config{}); err != nil || d.base != "http://127.0.0.1:1/"+APIVersion {
		t.Fatalf("DOCKER_HOST: %v %v", d, err)
	}
}

// TestLive runs the whole path against a real daemon when
// SAP_DOCKER_LIVE=1 (DOCKER_HOST or the default socket): pull a tiny
// image by digest, start, list, kill, list.
func TestLive(t *testing.T) {
	if os.Getenv("SAP_DOCKER_LIVE") != "1" {
		t.Skip("SAP_DOCKER_LIVE=1 to run against a daemon")
	}
	img := os.Getenv("SAP_DOCKER_IMAGE")
	if img == "" {
		img = "busybox@sha256:b7f3d86d6e84fc17718c48bcde1450807faa2d56704205c697b4bd5df7b9e29f" // busybox:1.36
	}
	d, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	lease := "live" + time.Now().Format("150405")
	_ = d.Kill(ctx, provisioner.Handle("sap-"+lease))
	h, err := d.Start(ctx, provisioner.StartSpec{Lease: lease, Owner: "ed:live", Image: img, Params: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Kill(context.Background(), h) })
	got, err := d.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range got {
		// The daemon may normalise the name (docker.io/library/…); the digest is what matters.
		if r.Handle == h && r.Lease == lease && r.Owner == "ed:live" && strings.HasSuffix(r.Image, img[strings.Index(img, "@"):]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("started container not listed: %+v", got)
	}
	if err := d.Kill(ctx, h); err != nil {
		t.Fatal(err)
	}
	got, _ = d.List(ctx)
	for _, r := range got {
		if r.Handle == h {
			t.Fatal("killed container still listed")
		}
	}
}
