// Package provisioner is the platform side of spawning (protocol
// ADR-0009; invariant 13): an ordinary actor with three facets —
// #spawn {image@digest, params, until} → {lease}, #extend {lease,
// until}, #kill {lease} — that renders LEASES into running containers
// through a per-platform Driver. It knows leases: an image, an opaque
// params blob, a deadline, an owner, and a container handle. It knows
// nothing about actors: the params are the parent's intro and are
// never read here, and a provisioner never learns whether a birth
// succeeded. A third party runs one knowing only "be an actor with
// three facets".
//
// # Leases are passive
//
// Every lease has a deadline. #spawn sets the first one (the parent's
// birth window); each #extend moves it (the parent's #renew decorator
// sends until = the re-issued cert's exp, spawn.Spawner); nothing
// else does. A parent that stops renewing, or dies, lets its children
// lapse: Sweep kills every running lease whose deadline has passed
// under this actor's clock. Sweep runs inside each handler and is
// exported for the owner's beat — on a platform with a native
// deadline (k8s activeDeadlineSeconds) it is bookkeeping, on one
// without (docker) it IS the deadline while this process lives; Adopt
// at the next start covers the gap. Nothing here starts a goroutine.
//
// # The table is a cache, not a record
//
// The lease table is what this process rendered, never persisted
// (ADR-0009 amendment; invariant 12). A restarted provisioner calls
// Adopt before Listen: Driver.List returns every container the driver
// labelled at Start (lease id, owner — immutable facts; never the
// deadline), and each becomes a running lease with Until = now +
// AdoptGrace. The owner's next #extend sets the real deadline; an
// owner that never shows lets Sweep kill it — the birth window's
// shape again.
//
// # State machine
//
//	pending ──Start ok──▶ running ──deadline passed, Kill──▶ lapsed
//	   │                     │
//	   └─Start failed        └──────#kill, Kill ok──────────▶ killed
//
// pending is the lease during Driver.Start (the only slow call —
// image pull — so it runs without the table locked); a terminal lease
// leaves the table. #extend and #kill are the owner's alone: the
// spawner that sent #spawn, as the chain bound it (Invocation.From).
//
// # The driver
//
// Driver{Start, Extend, Kill, List} is the one seam a platform fills.
// Drivers live outside the protocol module (k8s, docker; a market
// later) so protocol/ never imports a platform SDK. Driver.Extend on
// a platform without a native deadline is a no-op; Sweep does the
// killing. Every driver labels what it starts with LabelLease and
// LabelOwner so List can return it.
package provisioner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/spawn"
)

// Handle is the driver's own reference to a running container (a Job
// name, a container id). Opaque to the provisioner.
type Handle string

// Labels every driver sets on what it starts, and reads back at List.
// Immutable facts only: the deadline moves every beat and has no home
// on docker, so it is never stored on the platform.
const (
	LabelLease = "sap/lease"
	LabelOwner = "sap/owner"
)

// StartSpec is what a driver starts: the lease id and owner (to label
// the container for List), the image by digest, the params to inject
// unread, and the first deadline.
type StartSpec struct {
	Lease  string
	Owner  cert.ActorID
	Image  string
	Params []byte
	Until  int64
}

// Running is one container a driver finds alive at List: the labels
// it set at Start, the image, and its handle.
type Running struct {
	Lease  string
	Owner  cert.ActorID
	Image  string
	Handle Handle
}

// Driver renders leases on one platform. Every call is synchronous;
// an error from Start means nothing is running, an error from Extend
// or Kill leaves the lease as it was (the caller retries on its beat).
// List returns every container this driver labelled and that is
// still alive; containers without both labels are not the driver's
// to report (skip them, log if you like — never kill what you do not
// understand).
type Driver interface {
	Start(ctx context.Context, spec StartSpec) (Handle, error)
	Extend(ctx context.Context, h Handle, until int64) error
	Kill(ctx context.Context, h Handle) error
	List(ctx context.Context) ([]Running, error)
}

// State is where a lease is in its machine.
type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateLapsed  State = "lapsed"
	StateKilled  State = "killed"
)

// Lease is one rental as the provisioner holds it.
type Lease struct {
	ID string
	// Owner is the actor that sent #spawn — the authority the chain
	// bound; #extend and #kill from anyone else are refused.
	Owner  cert.ActorID
	Image  string
	Until  int64
	State  State
	Handle Handle
}

// Refusals the handlers answer with (actor.StatusError text).
const (
	RefuseImage        = "image is not name@sha256:<digest>"
	RefuseUntilPast    = "until is not in the future"
	RefuseUnknownLease = "unknown lease"
	RefuseNotOwner     = "lease belongs to another actor"
	RefuseNotRunning   = "lease is not running"
)

// DefaultDriverTimeout bounds each driver call when
// Provisioner.DriverTimeout is 0. A number, not a rule.
const DefaultDriverTimeout = 60 * time.Second

// DefaultAdoptGrace is how long an adopted lease lives before an
// owner must have extended it, when Provisioner.AdoptGrace is 0. Size
// it to a spawner's beat plus slack. A number, not a rule.
const DefaultAdoptGrace = 5 * time.Minute

// imageByDigest is the one image shape accepted (ADR-0009: by digest,
// never by tag — the parent names code, not a moving pointer).
var imageByDigest = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)

// CheckImage is the image rule: name@sha256:<64 hex>.
func CheckImage(image string) error {
	if !imageByDigest.MatchString(image) {
		return errors.New(RefuseImage)
	}
	return nil
}

// Provisioner is the lease table and the three handlers on one actor.
// Configure the exported fields before Listen.
type Provisioner struct {
	Driver Driver
	// DriverTimeout bounds each Driver call made from a handler or
	// Sweep; 0 ⇒ DefaultDriverTimeout. Size it to the platform's
	// slowest synchronous Start: a driver that pulls the image inside
	// Start (docker run) needs more than one that only submits (a k8s
	// Job). A cancelled Start fails the #spawn and drops the lease.
	DriverTimeout time.Duration
	// AdoptGrace is the deadline Adopt gives each lease it takes over,
	// counted from the actor's clock; 0 ⇒ DefaultAdoptGrace.
	AdoptGrace time.Duration
	// Log receives what is not surfaced on the wire: Sweep's kills and
	// their failures, Adopt's takeovers. nil ⇒ slog.Default().
	Log *slog.Logger

	a      *actor.Actor
	mu     sync.Mutex
	leases map[string]*Lease
}

// New registers #spawn, #extend and #kill on a and returns the
// provisioner. Call before a.Listen.
func New(a *actor.Actor, d Driver) *Provisioner {
	p := &Provisioner{Driver: d, a: a, leases: make(map[string]*Lease)}
	a.AcceptTable[spawn.FacetSpawn] = p.spawn
	a.AcceptTable[spawn.FacetExtend] = p.extend
	a.AcceptTable[spawn.FacetKill] = p.kill
	return p
}

func (p *Provisioner) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// Leases is a snapshot of the table: every pending or running lease.
func (p *Provisioner) Leases() []Lease {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Lease, 0, len(p.leases))
	for _, l := range p.leases {
		out = append(out, *l)
	}
	return out
}

// Lease looks one lease up by id.
func (p *Provisioner) Lease(id string) (Lease, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.leases[id]
	if !ok {
		return Lease{}, false
	}
	return *l, true
}

func newLeaseID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (p *Provisioner) driverCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	d := p.DriverTimeout
	if d <= 0 {
		d = DefaultDriverTimeout
	}
	return context.WithTimeout(ctx, d)
}

// Adopt takes over what the driver finds alive: each Running the
// table does not already hold becomes a running lease with Until =
// now + AdoptGrace. Call once before Listen on a restart; a lease
// already in the table (spawned since, or adopted before) is left as
// it is. A List failure adopts nothing and is returned.
func (p *Provisioner) Adopt(ctx context.Context) error {
	dctx, cancel := p.driverCtx(ctx)
	found, err := p.Driver.List(dctx)
	cancel()
	if err != nil {
		return fmt.Errorf("adopt: list: %w", err)
	}
	grace := p.AdoptGrace
	if grace <= 0 {
		grace = DefaultAdoptGrace
	}
	until := p.a.Now() + int64(grace/time.Second)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range found {
		if r.Lease == "" || r.Owner == "" {
			p.log().Warn("provisioner: adopt skipped unlabelled container", "handle", r.Handle)
			continue
		}
		if _, ok := p.leases[r.Lease]; ok {
			continue
		}
		p.leases[r.Lease] = &Lease{ID: r.Lease, Owner: r.Owner, Image: r.Image, Until: until, State: StateRunning, Handle: r.Handle}
		p.log().Info("provisioner: lease adopted", "lease", r.Lease, "owner", r.Owner, "until", until)
	}
	return nil
}

// Sweep lapses every running lease whose deadline has passed under the
// actor's effective clock: Driver.Kill, then the lease leaves the
// table. A Kill that fails is logged and the lease stays for the next
// sweep. Handlers call it; the owner calls it on its beat.
func (p *Provisioner) Sweep(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(ctx, p.a.Now())
}

func (p *Provisioner) sweepLocked(ctx context.Context, now int64) {
	for id, l := range p.leases {
		if l.State != StateRunning || l.Until > now {
			continue
		}
		dctx, cancel := p.driverCtx(ctx)
		err := p.Driver.Kill(dctx, l.Handle)
		cancel()
		if err != nil {
			p.log().Warn("provisioner: lapse kill failed", "lease", id, "owner", l.Owner, "err", err)
			continue
		}
		l.State = StateLapsed
		p.log().Info("provisioner: lease lapsed", "lease", id, "owner", l.Owner, "until", l.Until)
		delete(p.leases, id)
	}
}

// spawn serves #spawn: check the request, record the lease pending,
// Driver.Start without the table locked, then running. A Start failure
// drops the lease and is the refusal's text.
func (p *Provisioner) spawn(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
	var req spawn.SpawnRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("spawn: payload: %w", err)
	}
	if err := CheckImage(req.Image); err != nil {
		return nil, err
	}
	now := p.a.Now()
	if req.Until <= now {
		return nil, errors.New(RefuseUntilPast)
	}
	id, err := newLeaseID()
	if err != nil {
		return nil, err
	}
	l := &Lease{ID: id, Owner: inv.From, Image: req.Image, Until: req.Until, State: StatePending}
	p.mu.Lock()
	p.sweepLocked(ctx, now)
	p.leases[id] = l
	p.mu.Unlock()

	dctx, cancel := p.driverCtx(ctx)
	h, err := p.Driver.Start(dctx, StartSpec{Lease: id, Owner: inv.From, Image: req.Image, Params: req.Params, Until: req.Until})
	cancel()

	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		delete(p.leases, id)
		return nil, fmt.Errorf("start: %w", err)
	}
	l.Handle = h
	l.State = StateRunning
	return json.Marshal(spawn.SpawnReply{Lease: id})
}

// owned finds a running lease the caller owns. Caller holds p.mu.
func (p *Provisioner) owned(id string, caller cert.ActorID) (*Lease, error) {
	l, ok := p.leases[id]
	if !ok {
		return nil, errors.New(RefuseUnknownLease)
	}
	if l.Owner != caller {
		return nil, errors.New(RefuseNotOwner)
	}
	if l.State != StateRunning {
		return nil, errors.New(RefuseNotRunning)
	}
	return l, nil
}

// extend serves #extend: the owner moves a running lease's deadline
// to until (> now; a parent may shorten its own lease). Driver.Extend
// first — a driver refusal leaves the deadline as it was.
func (p *Provisioner) extend(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
	var req spawn.ExtendRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("extend: payload: %w", err)
	}
	now := p.a.Now()
	if req.Until <= now {
		return nil, errors.New(RefuseUntilPast)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(ctx, now)
	l, err := p.owned(req.Lease, inv.From)
	if err != nil {
		return nil, err
	}
	dctx, cancel := p.driverCtx(ctx)
	err = p.Driver.Extend(dctx, l.Handle, req.Until)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("extend: %w", err)
	}
	l.Until = req.Until
	return json.Marshal(spawn.ExtendReply{Until: l.Until})
}

// kill serves #kill: the owner ends a running lease. Driver.Kill
// first — a driver refusal keeps the lease running (the owner
// retries).
func (p *Provisioner) kill(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
	var req spawn.KillRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("kill: payload: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(ctx, p.a.Now())
	l, err := p.owned(req.Lease, inv.From)
	if err != nil {
		return nil, err
	}
	dctx, cancel := p.driverCtx(ctx)
	err = p.Driver.Kill(dctx, l.Handle)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("kill: %w", err)
	}
	l.State = StateKilled
	delete(p.leases, req.Lease)
	return []byte(`{}`), nil
}
