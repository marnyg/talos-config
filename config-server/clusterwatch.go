package main

// Cluster watch: the hub's read-only eye on the cluster over the
// control plane's kube-api facet, feeding two /status rows. Nothing
// here acts (ADR-0027: why this and not an in-cluster alerter or a
// scoped token). One dial, one client cert, two GETs per poll:
//
//   - gitops: ArgoCD's root Application (`apps`, the one that recurses
//     k8s/apps) and its two ages — this file.
//   - storage: Longhorn's Volume list and its robustness — storage.go
//     (cnb5).
//
// A third read belongs here too, but as a conscious addition (ADR-0027
// consequences), not a habit.
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

// clusterWatcher runs the poll loop; gitopsSnapshot and
// storageSnapshot are its two outputs.
type clusterWatcher struct {
	root string
	boot *bootstrapper
	dial func(ctx context.Context, name, facet string, timeout time.Duration) (facetClient, error)

	caMu    sync.Mutex
	caCache map[string]*x509.PEMEncodedCertificateAndKey // cluster CA by machine dir

	snapMu  sync.Mutex
	snap    gitopsSnapshot
	storage storageSnapshot
}

func newClusterWatcher(root string, boot *bootstrapper) *clusterWatcher {
	return &clusterWatcher{
		root:    root,
		boot:    boot,
		dial:    boot.dial,
		caCache: map[string]*x509.PEMEncodedCertificateAndKey{},
		snap:    gitopsSnapshot{Idle: "waiting for auto-bootstrap to see etcd running"},
	}
}

func (g *clusterWatcher) status() gitopsSnapshot {
	g.snapMu.Lock()
	defer g.snapMu.Unlock()
	return g.snap
}

func (g *clusterWatcher) setSnap(f func(*gitopsSnapshot)) {
	g.snapMu.Lock()
	defer g.snapMu.Unlock()
	f(&g.snap)
}

func (g *clusterWatcher) storageStatus() storageSnapshot {
	g.snapMu.Lock()
	defer g.snapMu.Unlock()
	return g.storage
}

// setStorage records a Longhorn read or its failure. A failure keeps
// the last good counts (as fail does for gitops) so the row shows
// what was last known beside why it is stale.
func (g *clusterWatcher) setStorage(snap storageSnapshot, err error) {
	g.snapMu.Lock()
	defer g.snapMu.Unlock()
	if err != nil {
		if g.storage.Err != err.Error() {
			log.Printf("storage: %v", err)
		}
		g.storage.Err = err.Error()
		return
	}
	if g.storage.Err != "" {
		log.Printf("storage: longhorn volumes readable again")
	}
	// Log on change only, and only what the row would flag.
	prev, _ := g.storage.line()
	if line, warn := snap.line(); warn && line != prev {
		log.Printf("storage: %s", line)
	}
	g.storage = snap
}

func (g *clusterWatcher) run(ctx context.Context) {
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
func (g *clusterWatcher) step(ctx context.Context) {
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
	kc, err := g.kubeClient(m, fc)
	if err != nil {
		g.fail(err)
		g.setStorage(storageSnapshot{}, err)
		return
	}
	defer kc.CloseIdleConnections()
	app, err := g.fetchApp(ctx, kc)
	if err != nil {
		g.fail(err)
	} else {
		g.recordApp(app)
	}
	vols, err := g.fetchVolumes(ctx, kc)
	if err != nil {
		g.setStorage(storageSnapshot{}, err)
	} else {
		g.setStorage(vols.summarize(), nil)
	}
}

// recordApp replaces the gitops snapshot with one good read.
func (g *clusterWatcher) recordApp(app *argoApp) {
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
func (g *clusterWatcher) fail(err error) {
	g.setSnap(func(s *gitopsSnapshot) {
		if s.Err != err.Error() {
			log.Printf("gitops: %v", err)
		}
		s.LastPoll, s.Idle, s.Err = time.Now(), "", err.Error()
	})
}

// kubeClient is an API-server client over one facet connection: every
// HTTP connection is one stream on fc, TLS-verified against the
// cluster CA with gitopsServerName, authenticated by a one-hour
// system:masters client cert signed by that CA. Caller closes idle
// connections when done with the poll.
type kubeClient struct {
	*http.Client
	tr *http.Transport
}

func (k *kubeClient) CloseIdleConnections() { k.tr.CloseIdleConnections() }

// get GETs one API path into out; a non-200 is an error, not a value.
func (k *kubeClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+gitopsServerName+path, nil)
	if err != nil {
		return err
	}
	resp, err := k.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("GET %s: reading: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: parsing: %w", path, err)
	}
	return nil
}

// fetchOver is kubeClient + fetchApp in one call, kept so a test can
// drive the whole path over an in-memory facet.
func (g *clusterWatcher) fetchOver(ctx context.Context, m machines.Machine, fc facetClient) (*argoApp, error) {
	kc, err := g.kubeClient(m, fc)
	if err != nil {
		return nil, err
	}
	defer kc.CloseIdleConnections()
	return g.fetchApp(ctx, kc)
}

// fetchApp GETs the root Application; a body without reconciledAt is
// an error so the row never shows a fake "reconciled now".
func (g *clusterWatcher) fetchApp(ctx context.Context, kc *kubeClient) (*argoApp, error) {
	var app argoApp
	if err := kc.get(ctx, gitopsAppPath, &app); err != nil {
		return nil, err
	}
	if app.Status.ReconciledAt.IsZero() {
		return nil, fmt.Errorf("GET %s: no status.reconciledAt (never reconciled?)", gitopsAppPath)
	}
	return &app, nil
}

// fetchVolumes GETs Longhorn's Volume list (storage.go).
func (g *clusterWatcher) fetchVolumes(ctx context.Context, kc *kubeClient) (*longhornVolumeList, error) {
	var vols longhornVolumeList
	if err := kc.get(ctx, longhornVolumesPath, &vols); err != nil {
		return nil, err
	}
	return &vols, nil
}

func (g *clusterWatcher) kubeClient(m machines.Machine, fc facetClient) (*kubeClient, error) {
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
	return &kubeClient{Client: &http.Client{Transport: tr}, tr: tr}, nil
}

// clusterCA extracts the Kubernetes CA (cert + key) from the machine's
// composed config; only control-plane configs carry the key. Sibling
// of bootstrapper.issuingCA (the OS CA).
func (g *clusterWatcher) clusterCA(m machines.Machine) (*x509.PEMEncodedCertificateAndKey, error) {
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
