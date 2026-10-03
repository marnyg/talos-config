package main

import (
	"context"
	"crypto/tls"
	stdx509 "crypto/x509"
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

// TestGitopsFetchOverFacet: the GET rides the facet, verifies the
// server as `kubernetes`, authenticates with a system:masters client
// cert from the cluster CA, and the Application's status fields land
// in the snapshot.
func TestGitopsFetchOverFacet(t *testing.T) {
	var seen struct {
		path string
		cn   string
		org  []string
	}
	const body = `{"status":{"reconciledAt":"2026-09-29T19:10:00Z",
	  "sync":{"status":"Synced","revision":"f93ebd3bec026c2376471df41e844733aa97558e"},
	  "health":{"status":"Healthy"},
	  "operationState":{"phase":"Running","message":"waiting for healthy state of apps/Deployment/gateway",
	    "startedAt":"2026-09-29T18:11:26Z"}}}`
	g, m, facet := newClusterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			seen.cn = r.TLS.PeerCertificates[0].Subject.CommonName
			seen.org = r.TLS.PeerCertificates[0].Subject.Organization
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	app, err := g.fetchOver(ctx, m, facet)
	if err != nil {
		t.Fatal(err)
	}
	if seen.path != gitopsAppPath || seen.cn != "hub" || len(seen.org) != 1 || seen.org[0] != "system:masters" {
		t.Fatalf("server saw path %q from CN %q O %v", seen.path, seen.cn, seen.org)
	}
	if app.Status.Sync.Status != "Synced" || app.Status.Health.Status != "Healthy" ||
		app.Status.OperationState == nil || app.Status.OperationState.Phase != "Running" ||
		app.Status.ReconciledAt.UTC().Format(time.RFC3339) != "2026-09-29T19:10:00Z" {
		t.Fatalf("parsed app = %+v", app.Status)
	}

	// A non-200 and a body without reconciledAt are both errors, not
	// snapshots: the row must never show a fake "reconciled now".
	g2, m2, facet2 := newClusterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	if _, err := g2.fetchOver(ctx, m2, facet2); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("403 not surfaced: %v", err)
	}
	g3, m3, facet3 := newClusterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{}}`))
	}))
	if _, err := g3.fetchOver(ctx, m3, facet3); err == nil || !strings.Contains(err.Error(), "reconciledAt") {
		t.Fatalf("missing reconciledAt not surfaced: %v", err)
	}
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
}
