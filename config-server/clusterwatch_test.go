package main

import (
	"context"
	"crypto/tls"
	stdx509 "crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/marnyg/talos-config/config-server/machines"
)

// clusterFixture: an HTTPS "API server" behind a pipeFacet, TLS'd with
// a CA the watcher trusts as the cluster CA, requiring a client cert
// from that CA (as kube-apiserver does).
func newClusterFixture(t *testing.T, handler http.Handler) (*clusterWatcher, machines.Machine, *pipeFacet) {
	t.Helper()
	now := time.Now()
	ca, err := secrets.NewTalosCA(now)
	if err != nil {
		t.Fatal(err)
	}
	serverKP, err := x509.NewKeyPair(ca,
		x509.DNSNames([]string{gitopsServerName}),
		x509.NotBefore(now), x509.NotAfter(now.Add(time.Hour)),
		x509.KeyUsage(stdx509.KeyUsageDigitalSignature),
		x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageServerAuth}),
	)
	if err != nil {
		t.Fatal(err)
	}
	pool := stdx509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CrtPEM)
	srv := &http.Server{Handler: handler, TLSConfig: &tls.Config{
		Certificates: []tls.Certificate{*serverKP.Certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}}
	accept := make(chan net.Conn)
	ln := &pipeListener{accept: accept, done: make(chan struct{})}
	go srv.ServeTLS(ln, "", "") //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	b := newBootstrapper(t.TempDir(), testHubManager(t, nil))
	g := newClusterWatcher(b.root, b)
	m := machines.Machine{Dir: "/fake/cp1"}
	g.caCache[m.Dir] = &x509.PEMEncodedCertificateAndKey{Crt: ca.CrtPEM, Key: ca.KeyPEM}
	return g, m, &pipeFacet{accept: accept}
}

// TestGitopsFetchOverFacet: the GETs ride the facet, verify the
// server as `kubernetes`, authenticate with a system:masters client
// cert from the cluster CA, the root Application's status fields land
// in the snapshot, and the Application list is reduced to the
// worst-off children.
func TestGitopsFetchOverFacet(t *testing.T) {
	var seen struct {
		paths []string
		cn    string
		org   []string
	}
	const body = `{"status":{"reconciledAt":"2026-09-29T19:10:00Z",
	  "sync":{"status":"Synced","revision":"f93ebd3bec026c2376471df41e844733aa97558e"},
	  "health":{"status":"Healthy"},
	  "operationState":{"phase":"Running","message":"waiting for healthy state of apps/Deployment/gateway",
	    "startedAt":"2026-09-29T18:11:26Z"}}}`
	// The list carries the root itself (skipped), a healthy child, one
	// with a sync op Running since 11:00, one Running since 09:00 (the
	// oldest is named), one never reconciled (stalest, beats any time)
	// and one OutOfSync.
	const list = `{"items":[
	  {"metadata":{"name":"apps"},"status":{"reconciledAt":"2026-09-29T19:10:00Z","sync":{"status":"Synced"},"health":{"status":"Healthy"}}},
	  {"metadata":{"name":"platform-dns"},"status":{"reconciledAt":"2026-09-29T19:09:00Z","sync":{"status":"Synced"},"health":{"status":"Healthy"}}},
	  {"metadata":{"name":"platform-media"},"status":{"reconciledAt":"2026-09-29T19:08:00Z","sync":{"status":"Synced"},"health":{"status":"Progressing"},
	    "operationState":{"phase":"Running","startedAt":"2026-09-29T11:00:00Z"}}},
	  {"metadata":{"name":"platform-gateway"},"status":{"reconciledAt":"2026-09-29T19:07:00Z","sync":{"status":"Synced"},"health":{"status":"Progressing"},
	    "operationState":{"phase":"Running","message":"waiting for healthy state of apps/Deployment/gateway","startedAt":"2026-09-29T09:00:00Z"}}},
	  {"metadata":{"name":"platform-new"},"status":{}},
	  {"metadata":{"name":"platform-cache"},"status":{"reconciledAt":"2026-09-29T19:06:00Z","sync":{"status":"OutOfSync"},"health":{"status":"Healthy"}}}
	]}`
	g, m, facet := newClusterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.paths = append(seen.paths, r.URL.Path)
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			seen.cn = r.TLS.PeerCertificates[0].Subject.CommonName
			seen.org = r.TLS.PeerCertificates[0].Subject.Organization
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case gitopsAppPath:
			_, _ = w.Write([]byte(body))
		case gitopsAppsPath:
			_, _ = w.Write([]byte(list))
		default:
			http.NotFound(w, r)
		}
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	app, kids, err := g.fetchOver(ctx, m, facet)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen.paths) != 2 || seen.paths[0] != gitopsAppPath || seen.paths[1] != gitopsAppsPath ||
		seen.cn != "hub" || len(seen.org) != 1 || seen.org[0] != "system:masters" {
		t.Fatalf("server saw paths %q from CN %q O %v", seen.paths, seen.cn, seen.org)
	}
	if app.Status.Sync.Status != "Synced" || app.Status.Health.Status != "Healthy" ||
		app.Status.OperationState == nil || app.Status.OperationState.Phase != "Running" ||
		app.Status.ReconciledAt.UTC().Format(time.RFC3339) != "2026-09-29T19:10:00Z" {
		t.Fatalf("parsed app = %+v", app.Status)
	}
	if !kids.Listed || kids.Err != "" || kids.Count != 5 ||
		kids.StuckName != "platform-gateway" || kids.StuckSince.UTC().Format(time.RFC3339) != "2026-09-29T09:00:00Z" ||
		!strings.Contains(kids.StuckMessage, "gateway") ||
		kids.StaleName != "platform-new" || !kids.StaleAt.IsZero() ||
		kids.OffName != "platform-cache" || kids.OffState != "OutOfSync/Healthy" {
		t.Fatalf("children = %+v", kids)
	}
	// With every child reconciled, the stalest is the oldest timestamp.
	if c := summarizeChildren([]argoApp{mkApp("a", "2026-09-29T19:00:00Z"), mkApp("b", "2026-09-29T18:00:00Z"), mkApp("c", "2026-09-29T18:30:00Z")}); c.StaleName != "b" || c.Count != 3 {
		t.Fatalf("stalest = %+v", c)
	}

	// A non-200 and a body without reconciledAt are both errors, not
	// snapshots: the row must never show a fake "reconciled now".
	g2, m2, facet2 := newClusterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	if _, _, err := g2.fetchOver(ctx, m2, facet2); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("403 not surfaced: %v", err)
	}
	g3, m3, facet3 := newClusterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{}}`))
	}))
	if _, _, err := g3.fetchOver(ctx, m3, facet3); err == nil || !strings.Contains(err.Error(), "reconciledAt") {
		t.Fatalf("missing reconciledAt not surfaced: %v", err)
	}

	// A list that fails keeps the root read; the failure rides beside it.
	g4, m4, facet4 := newClusterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == gitopsAppPath {
			_, _ = w.Write([]byte(body))
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	app4, kids4, err := g4.fetchOver(ctx, m4, facet4)
	if err != nil || app4 == nil || kids4.Listed || !strings.Contains(kids4.Err, "403") {
		t.Fatalf("list failure: app=%v kids=%+v err=%v", app4 != nil, kids4, err)
	}
	g4.recordApp(app4)
	g4.recordChildren(gitopsChildren{}, errors.New(kids4.Err))
	if line, warn := g4.status().line(time.Now()); !warn || !strings.Contains(line, "Synced/Healthy") || !strings.Contains(line, "last poll failed") {
		t.Fatalf("list failure row = %q warn=%v", line, warn)
	}
}

func mkApp(name, reconciledAt string) argoApp {
	var a argoApp
	a.Metadata.Name = name
	a.Status.ReconciledAt, _ = time.Parse(time.RFC3339, reconciledAt)
	a.Status.Sync.Status, a.Status.Health.Status = "Synced", "Healthy"
	return a
}

// TestGitopsStepGatesOnBootstrap: no dial before auto-bootstrap has
// seen etcd running — the row says why instead.
func TestGitopsStepGatesOnBootstrap(t *testing.T) {
	b := newBootstrapper(t.TempDir(), testHubManager(t, nil))
	if err := os.MkdirAll(filepath.Join(b.root, "machines", "aa-bb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.root, "machines", "aa-bb", "meta.yaml"), []byte("config: cp.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := newClusterWatcher(b.root, b)
	dialed := false
	g.dial = func(context.Context, string, string, time.Duration) (facetClient, error) {
		dialed = true
		return nil, errMemberUnknown
	}
	b.setSnap(func(s *bootSnapshot) { s.State = etcdUnknown.String(); s.Target = "aa:bb"; s.Name = "cp1" })
	g.step(t.Context())
	if dialed {
		t.Fatal("dialed kube-api before etcd was observed running")
	}
	if line, warn := g.status().line(time.Now()); warn || !strings.Contains(line, "node-unknown") {
		t.Fatalf("idle line = %q warn=%v", line, warn)
	}
	// etcd running, dial fails ⇒ an error row, warn.
	b.setSnap(func(s *bootSnapshot) { s.State = etcdRunning.String() })
	g.step(t.Context())
	if !dialed {
		t.Fatal("did not dial once etcd was running")
	}
	if line, warn := g.status().line(time.Now()); !warn || !strings.Contains(line, "error") {
		t.Fatalf("failed poll line = %q warn=%v", line, warn)
	}
}

// TestGitopsLine pins the two ages the row exists for.
func TestGitopsLine(t *testing.T) {
	now := time.Date(2026, 9, 29, 20, 0, 0, 0, time.UTC)
	fresh := gitopsSnapshot{
		Sync: "Synced", Health: "Healthy", Revision: "f93ebd3bec02",
		ReconciledAt: now.Add(-2 * time.Minute),
		OpPhase:      "Succeeded", OpStartedAt: now.Add(-time.Hour), OpFinishedAt: now.Add(-50 * time.Minute),
	}
	if line, warn := fresh.line(now); warn || !strings.Contains(line, "@f93ebd3") || !strings.Contains(line, "reconciled 2m ago") {
		t.Fatalf("fresh: %q warn=%v", line, warn)
	}
	// The controller is dead: reconciledAt ages past gitopsStaleAfter.
	dead := fresh
	dead.ReconciledAt = now.Add(-8 * 24 * time.Hour)
	if line, warn := dead.line(now); !warn || !strings.Contains(line, "NOT RECONCILED") {
		t.Fatalf("dead controller: %q warn=%v", line, warn)
	}
	// A sync op stuck Running past gitopsOpLongAfter, app still Synced.
	stuck := fresh
	stuck.OpPhase, stuck.OpStartedAt, stuck.OpFinishedAt = "Running", now.Add(-9*time.Hour), time.Time{}
	stuck.OpMessage = "waiting for healthy state of apps/Deployment/gateway"
	if line, warn := stuck.line(now); !warn || !strings.Contains(line, "STUCK") || !strings.Contains(line, "gateway") {
		t.Fatalf("stuck op: %q warn=%v", line, warn)
	}
	// A young Running op is normal.
	young := stuck
	young.OpStartedAt = now.Add(-time.Minute)
	if line, warn := young.line(now); warn || !strings.Contains(line, "running") {
		t.Fatalf("young op: %q warn=%v", line, warn)
	}
	// A failed poll keeps the last good read and warns beside it.
	failed := fresh
	failed.Err = "dial cp1/kube-api: unreachable"
	if line, warn := failed.line(now); !warn || !strings.Contains(line, "reconciled 2m ago") || !strings.Contains(line, "last poll failed") {
		t.Fatalf("failed poll: %q warn=%v", line, warn)
	}

	// Children (2c2x). Healthy ones add a count and nothing else.
	healthyKids := fresh
	healthyKids.Children = gitopsChildren{Listed: true, Count: 30, StaleName: "platform-dns", StaleAt: now.Add(-3 * time.Minute)}
	if line, warn := healthyKids.line(now); warn || !strings.Contains(line, "30 children") || strings.Contains(line, "child ") {
		t.Fatalf("healthy children: %q warn=%v", line, warn)
	}
	// A child's sync op stuck past gitopsOpLongAfter while the root
	// reads Synced/Healthy — the 9l67 symptom.
	stuckKid := healthyKids
	stuckKid.Children.StuckName, stuckKid.Children.StuckSince = "platform-gateway", now.Add(-9*time.Hour)
	stuckKid.Children.StuckMessage = "waiting for healthy state of apps/Deployment/gateway"
	if line, warn := stuckKid.line(now); !warn || !strings.Contains(line, "child platform-gateway STUCK Running since 9h") || !strings.Contains(line, "gateway)") {
		t.Fatalf("stuck child: %q warn=%v", line, warn)
	}
	// A young child op is normal.
	youngKid := stuckKid
	youngKid.Children.StuckSince = now.Add(-time.Minute)
	if line, warn := youngKid.line(now); warn || strings.Contains(line, "STUCK") {
		t.Fatalf("young child op: %q warn=%v", line, warn)
	}
	// A child nobody reconciles past gitopsStaleAfter.
	staleKid := healthyKids
	staleKid.Children.StaleName, staleKid.Children.StaleAt = "platform-media", now.Add(-2*time.Hour)
	if line, warn := staleKid.line(now); !warn || !strings.Contains(line, "child platform-media NOT RECONCILED for 2h") {
		t.Fatalf("stale child: %q warn=%v", line, warn)
	}
	// A child never reconciled at all is the stale case without an age.
	newKid := healthyKids
	newKid.Children.StaleName, newKid.Children.StaleAt = "platform-new", time.Time{}
	if line, warn := newKid.line(now); !warn || !strings.Contains(line, "child platform-new NEVER RECONCILED") {
		t.Fatalf("never-reconciled child: %q warn=%v", line, warn)
	}
	// An OutOfSync child is named for context, not warned about.
	offKid := healthyKids
	offKid.Children.OffName, offKid.Children.OffState = "platform-cache", "OutOfSync/Healthy"
	if line, warn := offKid.line(now); warn || !strings.Contains(line, "child platform-cache OutOfSync/Healthy") {
		t.Fatalf("off child: %q warn=%v", line, warn)
	}
	// A failed list keeps the last children summary and warns beside it.
	listFailed := healthyKids
	listFailed.Children.Err = "GET " + gitopsAppsPath + ": 403 Forbidden"
	if line, warn := listFailed.line(now); !warn || !strings.Contains(line, "30 children") || !strings.Contains(line, "last poll failed: GET") {
		t.Fatalf("failed list: %q warn=%v", line, warn)
	}
}
