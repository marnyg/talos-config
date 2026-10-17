package main

// Cluster watch: the hub's read-only eye on the cluster over the
// control plane's kube-api facet, feeding two /status rows. Nothing
// here acts (ADR-0027: why this and not an in-cluster alerter or a
// scoped token). One dial, one client cert, three GETs per poll:
//
//   - gitops: ArgoCD's root Application (`apps`, the app of apps that
//     syncs k8s/clusters/homelab) and its two ages, plus the
//     Application list of the same namespace so the per-directory
//     children the root emits are held to the same two ages
//     (talos-config-2c2x: since app-of-apps the children do the real
//     syncing, and a child stuck the 9l67 way left the root reading
//     Synced) — this file.
//   - storage: Longhorn's Volume list and its robustness — storage.go
//     (cnb5).
//
// The children list is the gitops read widened, not a new concern. A
// third concern belongs here too, but as a conscious addition
// (ADR-0027 consequences), not a habit.
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
	"github.com/marnyg/talos-config/protocol/facethttp"
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
	// gitopsApp is the root Application, namespace/name; the children
	// are every other Application in that namespace (gitopsAppsPath).
	gitopsNamespace = "argocd"
	gitopsAppName   = "apps"
	gitopsApp       = gitopsNamespace + "/" + gitopsAppName
	gitopsAppsPath  = "/apis/argoproj.io/v1alpha1/namespaces/" + gitopsNamespace + "/applications"
	gitopsAppPath   = gitopsAppsPath + "/" + gitopsAppName
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
	Children     gitopsChildren
}

// gitopsChildren is what the last Application list saw across the
// root's children, reduced to the one child per age that is worst off
// — the row names a culprit, not a table. Like the root fields it is
// the last good read; Err beside it says the latest list failed.
type gitopsChildren struct {
	Listed bool   // false ⇒ no list has been read yet; the rest is zero
	Err    string // last list failure, "" when the last poll read the list
	Count  int
	// Stuck*: the child with the oldest sync op still Running.
	StuckName    string
	StuckSince   time.Time
	StuckMessage string
	// Stale*: the child with the oldest reconciledAt; a zero StaleAt
	// with a name is a child ArgoCD has never reconciled.
	StaleName string
	StaleAt   time.Time
	// Off*: the first child not Synced, or Degraded/Missing — context,
	// never a warn on its own (auto-sync passes through OutOfSync).
	OffName  string
	OffState string // "<sync>/<health>"
}

// summarizeChildren reduces an Application list to gitopsChildren,
// skipping the root itself.
func summarizeChildren(items []argoApp) gitopsChildren {
	c := gitopsChildren{Listed: true}
	for _, a := range items {
		name := a.Metadata.Name
		if name == gitopsAppName {
			continue
		}
		c.Count++
		if op := a.Status.OperationState; op != nil && op.Phase == "Running" &&
			(c.StuckName == "" || op.StartedAt.Before(c.StuckSince)) {
			c.StuckName, c.StuckSince, c.StuckMessage = name, op.StartedAt, op.Message
		}
		if c.StaleName == "" || staler(a.Status.ReconciledAt, c.StaleAt) {
			c.StaleName, c.StaleAt = name, a.Status.ReconciledAt
		}
		// A child with no sync status yet is the never-reconciled case
		// above, not an off one.
		if sync, health := a.Status.Sync.Status, a.Status.Health.Status; c.OffName == "" && sync != "" &&
			(sync != "Synced" || health == "Degraded" || health == "Missing") {
			c.OffName, c.OffState = name, sync+"/"+health
		}
	}
	return c
}

// staler reports whether reconciledAt t is older than than; a zero t
// (never reconciled) is older than any time.
func staler(t, than time.Time) bool {
	if than.IsZero() {
		return false
	}
	return t.IsZero() || t.Before(than)
}

// line renders the /status row and whether it warrants the warn class:
// a fetch error, a reconcile older than gitopsStaleAfter, or an op
// Running longer than gitopsOpLongAfter — for the root, and for the
// worst-off child on each age.
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
	if c := g.Children; c.Listed {
		parts = append(parts, fmt.Sprintf("%d children", c.Count))
		if c.StuckName != "" && now.Sub(c.StuckSince) > gitopsOpLongAfter {
			warn = true
			msg := fmt.Sprintf("child %s STUCK Running since %s", c.StuckName, ago(now, c.StuckSince))
			if c.StuckMessage != "" {
				msg += " (" + c.StuckMessage + ")"
			}
			parts = append(parts, msg)
		}
		switch {
		case c.StaleName == "":
		case c.StaleAt.IsZero():
			warn = true
			parts = append(parts, "child "+c.StaleName+" NEVER RECONCILED")
		case now.Sub(c.StaleAt) > gitopsStaleAfter:
			warn = true
			parts = append(parts, fmt.Sprintf("child %s NOT RECONCILED for %s", c.StaleName, ago(now, c.StaleAt)))
		}
		if c.OffName != "" {
			parts = append(parts, fmt.Sprintf("child %s %s", c.OffName, c.OffState))
		}
	}
	if g.Err != "" {
		warn = true
		parts = append(parts, "last poll failed: "+g.Err)
	}
	if g.Children.Err != "" {
		warn = true
		parts = append(parts, "last poll failed: "+g.Children.Err)
	}
	return strings.Join(parts, " — "), warn
}

// argoApp is the slice of an Application we read; the same shape is
// each item of an ApplicationList.
type argoApp struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
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
		g.failAll(fmt.Errorf("loading machines: %w", err))
		return
	}
	m, ok := byMAC[bs.Target]
	if !ok {
		g.failAll(fmt.Errorf("control plane %s not declared", bs.Target))
		return
	}
	ctx, cancel := context.WithTimeout(ctx, gitopsDialTimeout)
	defer cancel()
	fc, err := g.dial(ctx, bs.Name, "kube-api", gitopsDialTimeout)
	if err != nil {
		g.failAll(fmt.Errorf("dial %s/kube-api: %w", bs.Name, err))
		return
	}
	defer fc.Close() //nolint:errcheck
	kc, err := g.kubeClient(m, fc)
	if err != nil {
		g.failAll(err)
		return
	}
	defer kc.CloseIdleConnections()
	app, err := g.fetchApp(ctx, kc)
	if err != nil {
		g.fail(err)
	} else {
		g.recordApp(app)
		g.recordChildren(g.fetchChildren(ctx, kc))
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
			Children:     s.Children, // recordChildren follows with its own read
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

// recordChildren replaces the children summary with one good list
// read, or — like fail — keeps the last good one and notes the failure
// beside it, so a list that stops answering never erases the root read
// recorded just before.
func (g *clusterWatcher) recordChildren(kids gitopsChildren, err error) {
	g.setSnap(func(s *gitopsSnapshot) {
		prev := s.Children
		if err != nil {
			kids = prev
			kids.Err = err.Error()
			if prev.Err != kids.Err {
				log.Printf("gitops: %v", err)
			}
		} else if prev.Err != "" {
			log.Printf("gitops: %s readable again", gitopsAppsPath)
		}
		s.Children = kids
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

// failAll records a failure that happened before either read could
// start — machines, control-plane lookup, dial, client build — on both
// rows. Each keeps its last good read beside the error; without this
// the storage row kept showing a stale healthy count with nothing to
// say the poll had stopped reaching the cluster (talos-config-63u5).
func (g *clusterWatcher) failAll(err error) {
	g.fail(err)
	g.setStorage(storageSnapshot{}, err)
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

// fetchOver is kubeClient + the two gitops reads in one call, kept so
// a test can drive the whole path over an in-memory facet. The error
// is the root read's; a failed list read lands in the children's Err,
// as in step.
func (g *clusterWatcher) fetchOver(ctx context.Context, m machines.Machine, fc facetClient) (*argoApp, gitopsChildren, error) {
	kc, err := g.kubeClient(m, fc)
	if err != nil {
		return nil, gitopsChildren{}, err
	}
	defer kc.CloseIdleConnections()
	app, err := g.fetchApp(ctx, kc)
	if err != nil {
		return nil, gitopsChildren{}, err
	}
	kids, err := g.fetchChildren(ctx, kc)
	if err != nil {
		kids.Err = err.Error()
	}
	return app, kids, nil
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

// fetchChildren GETs the namespace's Application list and reduces it
// to the worst-off children. A child without reconciledAt is a stale
// signal in the summary, not a fetch error — unlike the root, where it
// would leave the row with nothing to show.
func (g *clusterWatcher) fetchChildren(ctx context.Context, kc *kubeClient) (gitopsChildren, error) {
	var list struct {
		Items []argoApp `json:"items"`
	}
	if err := kc.get(ctx, gitopsAppsPath, &list); err != nil {
		return gitopsChildren{}, err
	}
	return summarizeChildren(list.Items), nil
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
			return facethttp.Dialed(raw, HubMemberName, fc.Peer()), nil
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
