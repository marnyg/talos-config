package main

// Auto-bootstrap: the server watches the mesh for the approved
// control-plane node and, when its etcd is waiting for bootstrap, calls
// the machinery Bootstrap API over the overlay (phase 2 step 3 moved
// the dial from wg0's netstack to the mesh's). No trust escalation: the
// server already composes configs from the cluster secrets, so it holds
// the OS CA and can mint its own short-lived os:admin client cert.
//
// Bootstrap must run exactly once per cluster — calling it on a CP that
// should *join* an existing etcd would split-brain the cluster. Guards,
// in order:
//   - refuses to act unless exactly ONE control plane is declared in
//     talos/machines/ (multi-CP auto-bootstrap is deliberately out of
//     scope; bootstrap manually and add a guard before relaxing this)
//   - requires etcd observed "waiting" on two consecutive polls
//   - never bootstraps when etcd is Running anywhere it can see
//   - at most one successful Bootstrap call per server lifetime
//
// All state is in-memory: a restart re-observes reality (an already
// bootstrapped cluster reports etcd Running and the loop goes idle).

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/siderolabs/crypto/x509"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"

	"github.com/marnyg/talos-config/config-server/nebstack"
)

const (
	bootstrapPollInterval = 30 * time.Second
	bootstrapDialTimeout  = 15 * time.Second
	// waitingStreakNeeded is how many consecutive "etcd waiting" polls
	// must be seen before Bootstrap is called.
	waitingStreakNeeded = 2
)

// etcdObservation is what one poll of the target node concluded.
type etcdObservation int

const (
	etcdUnreachable etcdObservation = iota // tunnel/apid not reachable
	etcdAbsent                             // apid up, no etcd service yet
	etcdWaiting                            // etcd present, not Running (pre-bootstrap)
	etcdRunning                            // etcd Running: cluster is bootstrapped
)

func (o etcdObservation) String() string {
	return [...]string{"unreachable", "etcd-absent", "etcd-waiting", "etcd-running"}[o]
}

// bootAction is what the state machine decided after an observation.
type bootAction int

const (
	actNone bootAction = iota
	actBootstrap
	actDone
)

// bootState is the pure decision core, separated for testability.
type bootState struct {
	waitingStreak int
	attempted     bool // a Bootstrap call succeeded this lifetime
	done          bool // cluster confirmed bootstrapped
}

func (st *bootState) next(obs etcdObservation) bootAction {
	if st.done {
		return actNone
	}
	switch obs {
	case etcdRunning:
		st.done = true
		return actDone
	case etcdWaiting:
		st.waitingStreak++
		if !st.attempted && st.waitingStreak >= waitingStreakNeeded {
			return actBootstrap
		}
	default:
		st.waitingStreak = 0
	}
	return actNone
}

// observeEtcd maps a service list to an observation.
func observeEtcd(services []*machineapi.ServiceInfo) etcdObservation {
	for _, svc := range services {
		if svc.GetId() != "etcd" {
			continue
		}
		if svc.GetState() == "Running" {
			return etcdRunning
		}
		return etcdWaiting
	}
	return etcdAbsent
}

// bootSnapshot is a point-in-time view of the loop, for /status. It is
// the only bootstrapper state shared across goroutines.
type bootSnapshot struct {
	LastPoll  time.Time
	State     string // sealed | mesh-down | no-control-plane | multi-cp-refused | <observation>
	Target    string // control-plane MAC
	MeshIP    string // target's overlay address
	Done      bool   // cluster confirmed bootstrapped
	Attempted bool   // a Bootstrap call succeeded this lifetime
	LastErr   string // last RPC failure, "" when healthy
}

// bootstrapper runs the auto-bootstrap loop.
type bootstrapper struct {
	root string
	hub  *hubManager

	snapMu sync.Mutex
	snap   bootSnapshot

	st            bootState
	multiCPWarned bool
	lastObs       etcdObservation
	obsLogged     bool
	lastFail      string                                       // last RPC failure, logged on change only
	caCache       map[string]*x509.PEMEncodedCertificateAndKey // machine dir -> OS CA
}

func newBootstrapper(root string, hub *hubManager) *bootstrapper {
	return &bootstrapper{
		root:    root,
		hub:     hub,
		caCache: map[string]*x509.PEMEncodedCertificateAndKey{},
	}
}

// status returns a copy of the current snapshot.
func (b *bootstrapper) status() bootSnapshot {
	b.snapMu.Lock()
	defer b.snapMu.Unlock()
	return b.snap
}

func (b *bootstrapper) setSnap(f func(*bootSnapshot)) {
	b.snapMu.Lock()
	defer b.snapMu.Unlock()
	f(&b.snap)
}

func (b *bootstrapper) run(ctx context.Context) {
	log.Printf("auto-bootstrap: watching for a control plane with etcd waiting (poll %s)", bootstrapPollInterval)
	ticker := time.NewTicker(bootstrapPollInterval)
	defer ticker.Stop()
	for {
		b.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// step performs one poll + decision.
func (b *bootstrapper) step(ctx context.Context) {
	b.setSnap(func(s *bootSnapshot) { s.LastPoll = time.Now() })

	master := b.hub.current()
	if master == nil {
		b.setSnap(func(s *bootSnapshot) { s.State = "sealed" })
		return // sealed: no master, nothing to derive or dial
	}

	// The control channel rides the mesh: an unsealed hub whose mesh
	// failed to start has nothing to dial through. Kept distinct from
	// "sealed" so /status says which of the two it is.
	var svc *nebstack.Service
	var meshSubnet netip.Prefix
	if mesh := b.hub.mesh; mesh != nil {
		svc, _, _ = mesh.state()
		meshSubnet = mesh.subnet
	}
	if svc == nil {
		b.setSnap(func(s *bootSnapshot) { s.State = "mesh-down" })
		return
	}

	machines, err := loadMachines(filepath.Join(b.root, "machines"))
	if err != nil {
		log.Printf("auto-bootstrap: loading machines: %v", err)
		return
	}
	cps := controlPlanes(b.root, machines)
	if len(cps) == 0 {
		b.setSnap(func(s *bootSnapshot) { s.State = "no-control-plane" })
		return
	}
	if len(cps) > 1 {
		b.setSnap(func(s *bootSnapshot) { s.State = "multi-cp-refused" })
		if !b.multiCPWarned {
			log.Printf("auto-bootstrap: %d control planes declared — refusing to auto-bootstrap (split-brain risk); bootstrap manually", len(cps))
			b.multiCPWarned = true
		}
		return
	}

	var mac string
	for m := range cps {
		mac = m
	}
	m := cps[mac]
	ip, err := machineMeshIP(master, mac, m, meshSubnet)
	if err != nil {
		log.Printf("auto-bootstrap: mesh address for %s: %v", mac, err)
		return
	}

	obs := b.observe(ctx, svc, m, ip)
	if obs != b.lastObs || !b.obsLogged {
		log.Printf("auto-bootstrap: %s (%s over mesh): %s", mac, ip, obs)
		b.lastObs, b.obsLogged = obs, true
	}

	action := b.st.next(obs)
	b.setSnap(func(s *bootSnapshot) {
		s.State, s.Target, s.MeshIP = obs.String(), mac, ip.String()
		s.Done, s.Attempted, s.LastErr = b.st.done, b.st.attempted, b.lastFail
	})

	switch action {
	case actBootstrap:
		b.bootstrap(ctx, svc, mac, m, ip)
	case actDone:
		log.Printf("auto-bootstrap: cluster is bootstrapped (etcd running on %s); going idle", mac)
	}
}

// observe dials the node over the mesh and inspects its services.
func (b *bootstrapper) observe(ctx context.Context, svc *nebstack.Service, m machine, ip netip.Addr) etcdObservation {
	ctx, cancel := context.WithTimeout(ctx, bootstrapDialTimeout)
	defer cancel()

	c, err := b.talosClient(ctx, svc, m, ip)
	if err != nil {
		log.Printf("auto-bootstrap: building client: %v", err)
		return etcdUnreachable
	}
	defer c.Close() //nolint:errcheck

	resp, err := c.ServiceList(ctx)
	if err != nil {
		// "unreachable" alone hides whether the overlay or TLS failed;
		// log the underlying error whenever it changes.
		if msg := err.Error(); msg != b.lastFail {
			log.Printf("auto-bootstrap: service list via mesh failed: %v", err)
			b.lastFail = msg
		}
		return etcdUnreachable
	}
	b.lastFail = ""
	var services []*machineapi.ServiceInfo
	for _, msg := range resp.GetMessages() {
		services = append(services, msg.GetServices()...)
	}
	return observeEtcd(services)
}

// bootstrap performs the one-shot Bootstrap call.
func (b *bootstrapper) bootstrap(ctx context.Context, svc *nebstack.Service, mac string, m machine, ip netip.Addr) {
	log.Printf("AUTO-BOOTSTRAP: calling Bootstrap on %s (%s) — etcd waited %d consecutive polls", mac, ip, b.st.waitingStreak)

	ctx, cancel := context.WithTimeout(ctx, bootstrapDialTimeout)
	defer cancel()
	c, err := b.talosClient(ctx, svc, m, ip)
	if err != nil {
		log.Printf("auto-bootstrap: building client: %v", err)
		return
	}
	defer c.Close() //nolint:errcheck

	if err := c.Bootstrap(ctx, &machineapi.BootstrapRequest{}); err != nil {
		// FailedPrecondition covers both "not ready yet" and "already
		// bootstrapped"; either way the next observation settles it.
		log.Printf("auto-bootstrap: Bootstrap rejected (will re-observe): %v", err)
		return
	}
	b.st.attempted = true
	log.Printf("AUTO-BOOTSTRAP: Bootstrap accepted by %s — watching for etcd to come up", mac)
}

// talosClient builds a machinery client that dials through the mesh
// netstack, authenticating with a short-lived os:admin cert minted from
// the cluster's OS CA (extracted from the machine's composed config).
// The TLS dial verifies against the machine's overlay-address certSAN,
// which nebMachinePatch injects for exactly this reason.
func (b *bootstrapper) talosClient(ctx context.Context, svc *nebstack.Service, m machine, ip netip.Addr) (*client.Client, error) {
	ca, err := b.issuingCA(m)
	if err != nil {
		return nil, err
	}
	admin, err := secrets.NewAdminCertificateAndKey(time.Now(), ca, role.MakeSet(role.Admin), time.Hour)
	if err != nil {
		return nil, fmt.Errorf("minting admin cert: %w", err)
	}
	cfg := clientconfig.NewConfig("auto-bootstrap", []string{ip.String()}, ca.Crt, admin)

	dialer := grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		return svc.DialContext(ctx, "tcp", addr)
	})
	return client.New(ctx, client.WithConfig(cfg), client.WithGRPCDialOptions(dialer))
}

// issuingCA extracts the Talos OS CA (cert + key) from the machine's
// composed config. Only control-plane configs carry the CA key.
func (b *bootstrapper) issuingCA(m machine) (*x509.PEMEncodedCertificateAndKey, error) {
	if ca, ok := b.caCache[m.dir]; ok {
		return ca, nil
	}
	body, err := buildConfig(b.root, m)
	if err != nil {
		return nil, fmt.Errorf("composing config for CA extraction: %w", err)
	}
	provider, err := configloader.NewFromBytes(body)
	if err != nil {
		return nil, fmt.Errorf("parsing composed config: %w", err)
	}
	ca := provider.Machine().Security().IssuingCA()
	if ca == nil || len(ca.Key) == 0 {
		return nil, fmt.Errorf("composed config has no OS CA key (not a control-plane config?)")
	}
	b.caCache[m.dir] = ca
	return ca, nil
}

// controlPlanes filters machines to those whose base config declares a
// control-plane machine.type. The base (role) layer is where the type
// lives; patches never change it in this repo. Parsing the declared
// type — not the filename — keeps the single-CP guard honest if a
// role file is ever renamed or added.
func controlPlanes(root string, machines map[string]machine) map[string]machine {
	cps := map[string]machine{}
	for mac, m := range machines {
		raw, err := os.ReadFile(filepath.Join(root, m.Config))
		if err != nil {
			log.Printf("auto-bootstrap: reading base config for %s: %v", mac, err)
			continue
		}
		provider, err := configloader.NewFromBytes(raw)
		if err != nil {
			log.Printf("auto-bootstrap: parsing base config for %s: %v", mac, err)
			continue
		}
		if provider.Machine() != nil && provider.Machine().Type().IsControlPlane() {
			cps[mac] = m
		}
	}
	return cps
}
