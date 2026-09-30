package main

// GitOps watch: the hub reads ArgoCD's root Application (`apps`, the
// one that recurses k8s/apps) over the control plane's kube-api facet
// and shows its age on /status. Nothing here acts; it is an eye.
//
// Why (talos-config-9l67, 2026-09-29): argocd-application-controller-0
// sat Terminating on a dead node from 09-21 to 09-29 and nothing
// reconciled for 8 days; then a sync op hung "waiting for healthy
// state" of a ghost pod for 9 h while `apps` still read Synced. Both
// times the only symptom was a push that never landed. Two ages catch
// both: how long since ArgoCD last reconciled the app (the controller
// is dead), and how long the current sync operation has been Running
// (the op is stuck). /status is the owner's page, so the row lives
// there; the hub is the one component that can watch the cluster from
// outside it.
//
// Path: the same identity-plane dial auto-bootstrap uses (hubcaller
// dialMember), facet kube-api instead of apid (policy row
// `{facet: kube-api, host: hub}`), TLS to the API server with a
// short-lived system:masters client cert minted from the cluster CA
// the hub already holds in the composed control-plane config — no new
// authority: the hub composes that config, so it holds the CA key
// either way (same reasoning as bootstrap.go's os:admin cert). Read-
// only by construction: one GET, no token, no kubeconfig on disk.
//
// State is a safe-to-lose snapshot; a restart re-reads reality on the
// first poll after auto-bootstrap reports etcd-running.

import (
	"context"
	"crypto/tls"
	stdx509 "crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"

	"github.com/marnyg/talos-config/config-server/machines"
)

const (
	gitopsPollInterval = 5 * time.Minute
	gitopsDialTimeout  = 15 * time.Second
	// gitopsStaleAfter: ArgoCD refreshes every ~3 min; an app not
	// reconciled for this long has no live controller.
	gitopsStaleAfter = 20 * time.Minute
	// gitopsOpLongAfter: a sync op still Running for this long is stuck
	// (later waves wait on an earlier wave's health forever).
	gitopsOpLongAfter = 30 * time.Minute
	// gitopsApp is the root Application, namespace/name.
	gitopsApp     = "argocd/apps"
	gitopsAppPath = "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/apps"
	// gitopsServerName is the SAN the API server is verified against;
	// kube-apiserver's cert always carries it.
	gitopsServerName = "kubernetes"
)

// gitopsSnapshot is what the last poll saw.
type gitopsSnapshot struct {
	LastPoll     time.Time
	Idle         string // non-empty ⇒ not polling, and why
	Err          string // last fetch failure, "" when the last poll read the app
	Sync         string
	Health       string
	Revision     string
	ReconciledAt time.Time
	OpPhase      string // "" when the app has never had an operation
	OpStartedAt  time.Time
	OpFinishedAt time.Time
	OpMessage    string
}

// line renders the /status row and whether it warrants the warn class:
// a fetch error, a reconcile older than gitopsStaleAfter, or an op
// Running longer than gitopsOpLongAfter.
func (g gitopsSnapshot) line(now time.Time) (string, bool) {
	if g.Idle != "" {
		return "idle — " + g.Idle, false
	}
	if g.Err != "" && g.ReconciledAt.IsZero() {
		return "error: " + g.Err, true
	}
	var parts []string
	warn := false
	parts = append(parts, fmt.Sprintf("%s %s/%s", gitopsApp, g.Sync, g.Health))
	if len(g.Revision) >= 7 {
		parts[0] += " @" + g.Revision[:7]
	}
	age := now.Sub(g.ReconciledAt)
	if age > gitopsStaleAfter {
		warn = true
		parts = append(parts, fmt.Sprintf("NOT RECONCILED for %s", ago(now, g.ReconciledAt)))
	} else {
		parts = append(parts, "reconciled "+ago(now, g.ReconciledAt))
	}
	switch {
	case g.OpPhase == "":
	case g.OpPhase == "Running" && now.Sub(g.OpStartedAt) > gitopsOpLongAfter:
		warn = true
		msg := fmt.Sprintf("sync op STUCK Running since %s", ago(now, g.OpStartedAt))
		if g.OpMessage != "" {
			msg += " (" + g.OpMessage + ")"
		}
		parts = append(parts, msg)
	case g.OpPhase == "Running":
		parts = append(parts, "sync op running "+ago(now, g.OpStartedAt))
	case !g.OpFinishedAt.IsZero():
		parts = append(parts, fmt.Sprintf("last op %s %s", g.OpPhase, ago(now, g.OpFinishedAt)))
	default:
		parts = append(parts, "last op "+g.OpPhase)
	}
	if g.Err != "" {
		warn = true
		parts = append(parts, "last poll failed: "+g.Err)
	}
	return strings.Join(parts, " — "), warn
}

// argoApp is the slice of an Application we read.
type argoApp struct {
	Status struct {
		ReconciledAt time.Time `json:"reconciledAt"`
		Sync         struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
		OperationState *struct {
			Phase      string    `json:"phase"`
			Message    string    `json:"message"`
			StartedAt  time.Time `json:"startedAt"`
			FinishedAt time.Time `json:"finishedAt"`
		} `json:"operationState"`
	} `json:"status"`
}

// gitopsWatcher runs the poll loop.
type gitopsWatcher struct {
	root string
	boot *bootstrapper
	dial func(ctx context.Context, name, facet string, timeout time.Duration) (facetClient, error)

	caMu    sync.Mutex
	caCache map[string]*x509.PEMEncodedCertificateAndKey // cluster CA by machine dir

	snapMu sync.Mutex
	snap   gitopsSnapshot
}

func newGitopsWatcher(root string, boot *bootstrapper) *gitopsWatcher {
	return &gitopsWatcher{
		root:    root,
		boot:    boot,
		dial:    boot.dial,
		caCache: map[string]*x509.PEMEncodedCertificateAndKey{},
		snap:    gitopsSnapshot{Idle: "waiting for auto-bootstrap to see etcd running"},
	}
}

func (g *gitopsWatcher) status() gitopsSnapshot {
	g.snapMu.Lock()
	defer g.snapMu.Unlock()
	return g.snap
}

func (g *gitopsWatcher) setSnap(f func(*gitopsSnapshot)) {
	g.snapMu.Lock()
	defer g.snapMu.Unlock()
	f(&g.snap)
}

func (g *gitopsWatcher) run(ctx context.Context) {
	log.Printf("gitops: watching %s over the control plane's kube-api facet (poll %s)", gitopsApp, gitopsPollInterval)
	ticker := time.NewTicker(gitopsPollInterval)
	defer ticker.Stop()
	// The first poll waits for auto-bootstrap's first observation
	// rather than racing it.
	first := time.NewTimer(2 * bootstrapPollInterval)
	defer first.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-ticker.C:
		}
		g.step(ctx)
	}
}

// step polls once. It only dials while auto-bootstrap sees etcd
// running on a known control plane — before that there is no API
// server to ask, and the auto-bootstrap row already says why.
func (g *gitopsWatcher) step(ctx context.Context) {
	bs := g.boot.status()
	if bs.State != etcdRunning.String() || bs.Target == "" {
		g.setSnap(func(s *gitopsSnapshot) { s.LastPoll = time.Now(); s.Idle = "auto-bootstrap: " + bs.State })
		return
	}
	byMAC, err := machines.Load(filepath.Join(g.root, "machines"))
	if err != nil {
		g.fail(fmt.Errorf("loading machines: %w", err))
		return
	}
	m, ok := byMAC[bs.Target]
	if !ok {
		g.fail(fmt.Errorf("control plane %s not declared", bs.Target))
		return
	}
	ctx, cancel := context.WithTimeout(ctx, gitopsDialTimeout)
	defer cancel()
	fc, err := g.dial(ctx, bs.Name, "kube-api", gitopsDialTimeout)
	if err != nil {
		g.fail(fmt.Errorf("dial %s/kube-api: %w", bs.Name, err))
		return
	}
	defer fc.Close() //nolint:errcheck
	app, err := g.fetchOver(ctx, m, fc)
	if err != nil {
		g.fail(err)
		return
	}
	g.setSnap(func(s *gitopsSnapshot) {
		prevErr := s.Err
		*s = gitopsSnapshot{
			LastPoll:     time.Now(),
			Sync:         app.Status.Sync.Status,
			Health:       app.Status.Health.Status,
			Revision:     app.Status.Sync.Revision,
			ReconciledAt: app.Status.ReconciledAt,
		}
		if op := app.Status.OperationState; op != nil {
			s.OpPhase, s.OpMessage = op.Phase, op.Message
			s.OpStartedAt, s.OpFinishedAt = op.StartedAt, op.FinishedAt
		}
		if prevErr != "" {
			log.Printf("gitops: %s readable again", gitopsApp)
		}
	})
}

// fail records a poll failure without discarding the last good read
// (the row keeps showing the last known state, plus the failure).
func (g *gitopsWatcher) fail(err error) {
	g.setSnap(func(s *gitopsSnapshot) {
		if s.Err != err.Error() {
			log.Printf("gitops: %v", err)
		}
		s.LastPoll, s.Idle, s.Err = time.Now(), "", err.Error()
	})
}

// fetchOver GETs the Application over one facet connection: every HTTP
// connection is one stream on fc, TLS-verified against the cluster CA
// with gitopsServerName, authenticated by a one-hour system:masters
// client cert signed by that CA. Split from step so a test can drive
// it over an in-memory facet.
func (g *gitopsWatcher) fetchOver(ctx context.Context, m machines.Machine, fc facetClient) (*argoApp, error) {
	ca, err := g.clusterCA(m)
	if err != nil {
		return nil, err
	}
	caObj, err := x509.NewCertificateAuthorityFromCertificateAndKey(ca)
	if err != nil {
		return nil, fmt.Errorf("cluster CA: %w", err)
	}
	now := time.Now()
	admin, err := x509.NewKeyPair(caObj,
		x509.CommonName("hub"), x509.Organization("system:masters"),
		x509.NotBefore(now.Add(-time.Minute)), x509.NotAfter(now.Add(time.Hour)),
		x509.KeyUsage(stdx509.KeyUsageDigitalSignature),
		x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}),
	)
	if err != nil {
		return nil, fmt.Errorf("minting kube client cert: %w", err)
	}
	pool := stdx509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.Crt) {
		return nil, fmt.Errorf("cluster CA: no certificate in PEM")
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			raw, err := fc.Open(ctx)
			if err != nil {
				return nil, err
			}
			return &facetStreamConn{ReadWriteCloser: raw, peer: fc.Peer()}, nil
		},
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{*admin.Certificate},
			RootCAs:      pool,
			ServerName:   gitopsServerName,
			MinVersion:   tls.VersionTLS12,
		},
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+gitopsServerName+gitopsAppPath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", gitopsAppPath, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("GET %s: reading: %w", gitopsAppPath, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", gitopsAppPath, resp.Status)
	}
	var app argoApp
	if err := json.Unmarshal(body, &app); err != nil {
		return nil, fmt.Errorf("GET %s: parsing: %w", gitopsAppPath, err)
	}
	if app.Status.ReconciledAt.IsZero() {
		return nil, fmt.Errorf("GET %s: no status.reconciledAt (never reconciled?)", gitopsAppPath)
	}
	return &app, nil
}

// clusterCA extracts the Kubernetes CA (cert + key) from the machine's
// composed config; only control-plane configs carry the key. Sibling
// of bootstrapper.issuingCA (the OS CA).
func (g *gitopsWatcher) clusterCA(m machines.Machine) (*x509.PEMEncodedCertificateAndKey, error) {
	g.caMu.Lock()
	defer g.caMu.Unlock()
	if ca, ok := g.caCache[m.Dir]; ok {
		return ca, nil
	}
	body, err := machines.BuildConfig(g.root, m)
	if err != nil {
		return nil, fmt.Errorf("composing config for cluster CA: %w", err)
	}
	provider, err := configloader.NewFromBytes(body)
	if err != nil {
		return nil, fmt.Errorf("parsing composed config: %w", err)
	}
	ca := provider.Cluster().IssuingCA()
	if ca == nil || len(ca.Key) == 0 {
		return nil, fmt.Errorf("composed config has no cluster CA key (not a control-plane config?)")
	}
	g.caCache[m.Dir] = ca
	return ca, nil
}
