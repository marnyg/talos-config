//go:build iroh

package nodeagent

// The node agent runtime (talos-config-359.8.3.3): the first real
// member of the identity plane. One process on a Talos node that
//
//   - holds the NodeId (State.Key) and binds it on iroh, homed at the
//     hub's relay, outbound only;
//   - enrolls once: redeems the boot token for a Kit (ADR-0015) or loads
//     the persisted one;
//   - beats: learns the current hubkey from /.well-known (or its cache),
//     renews its member cert and beat grant when due or when the hub
//     rotated, fetches #bundle (grants, blocklist, name map), publishes
//     its own reach-me-at, persists everything. The beat is periodic,
//     and it is also the answer to evidence that the last one is stale:
//     a Dial that cannot reach a name, or a name the map lacks, re-beats
//     first (rate-limited) — a hub redeploy under a new key (ipt7), a
//     member enrolled or re-keyed since, are all one beat away. A node
//     that dials nothing between beats has two more kinds of evidence
//     (decision z2go): the pooled connection its last beat left to the
//     hub closing (iroh keep-alives it; ~30 s after the hub process
//     dies), and an admitted caller whose verified speak-as names a
//     hubkey newer than the one it beat against;
//   - serves stream facets: a caller connects under policy.ALPN(facet),
//     presents its bundle, and Authorize — rooted in this node's own
//     consent grant to the Owner — decides; admitted connections are
//     spliced to the loopback target (apid, kube-api).
//
// The node is a verifier: it reads no git, no registry, no network
// service to authorize (invariant 2, "git is compiler input, never
// verifier input"). The blocklist it applies is the one #bundle handed
// it; the consent it roots chains in is one it signed itself.
//
// The same runtime, with no facets to forward, is a caller-only member
// (irohup, talos-config-359.8.4): it enrolls (the caller hands it a
// Kit — a device's enrollment is wallet-signed, not a boot token),
// beats identically, and Dials other members by name with its bundle
// on connect (caller.go). Members and machines are one runtime; what
// differs is the accept table.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/policy"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Timing. Beat is the bundle refresh cadence — blocklist propagation
// latency and the name map's staleness bound; well inside GrantTTL.
// LocationTTL is the node's reach-me-at lifetime, one grant runway
// (hubseal.go's reasoning). ConsentTTL is the node's own root: refreshed
// on every beat attempt, hub or no hub, so it never lapses while the
// process runs.
// DefaultMinRebeat bounds how often staleness evidence (Dial failures,
// unknown names, Kick) may trigger a beat ahead of schedule, and is the
// flat retry against a sealed hub — up and waiting for the wallet, not
// broken, so the first beat after the unseal lands within one MinRebeat
// rather than at the tail of an exponential backoff; DefaultDialTimeout
// bounds one candidate dial, since a dial to a key nobody holds any
// more (the hub before a redeploy) otherwise hangs for iroh's idle
// timeout instead of failing.
const (
	DefaultBeat        = 6 * time.Hour
	LocationTTL        = issuer.GrantTTL
	ConsentTTL         = int64(24 * 60 * 60)
	DefaultMinRebeat   = time.Minute
	DefaultDialTimeout = 15 * time.Second
	minBackoff         = time.Minute
	maxBackoff         = 30 * time.Minute
)

// Options configures Start.
type Options struct {
	Config Config
	State  State
	// Forward is the accept table's other half: facet → loopback target
	// (glossary "Facet": ports exist only inside a facet definition).
	// Keys must be policy.Facets(policy.KindNode); only these ALPNs are
	// advertised, and the node consents to the Owner for exactly them.
	// Empty ⇒ a caller-only member: no ALPN advertised, no consent
	// signed, nothing accepted.
	Forward map[string]string
	// BindAddr is the UDP socket; "" ⇒ all interfaces, ephemeral port.
	BindAddr string
	// HTTP is the client for the hub's WAN endpoints; nil ⇒ 30 s timeout.
	HTTP *http.Client
	// Log; nil ⇒ log.Default().
	Log *log.Logger
	// BeatEvery; 0 ⇒ DefaultBeat.
	BeatEvery time.Duration
	// DialTimeout bounds each candidate dial in Dial; 0 ⇒ DefaultDialTimeout.
	DialTimeout time.Duration
	// MinRebeat is the floor between beats triggered by staleness
	// evidence (Dial, Kick); 0 ⇒ DefaultMinRebeat.
	MinRebeat time.Duration
	// Clock is the local clock (tests); nil ⇒ time.Now.
	Clock func() int64
}

// Agent is a running node agent.
type Agent struct {
	o      Options
	log    *log.Logger
	http   *http.Client
	ep     *irohtransport.Endpoint
	actor  *actor.Actor
	accept map[string]string // ALPN → facet, restricted to Forward
	facets []string          // sorted Forward keys

	beats    atomic.Int64 // successful beats this process
	attempts atomic.Int64 // beats attempted this process (tests: a refused beat is still evidence acted on)

	// One beat at a time: the loop's and any rebeat serialize on beatMu;
	// lastBeat (unix nanos, wall clock) is when the last one was
	// attempted, the reference for MinRebeat. kick wakes the loop.
	beatMu   sync.Mutex
	lastBeat atomic.Int64
	kick     chan struct{}

	mu     sync.Mutex
	kit    *issuer.Kit
	hub    *HubRecord
	bundle *issuer.Bundle
}

// Start binds the node's identity on iroh and loads its state. It
// neither enrolls nor beats — Run does. Close releases the endpoint.
func Start(o Options) (*Agent, error) {
	if err := o.Config.Validate(); err != nil {
		return nil, err
	}
	known := policy.Facets(policy.KindNode)
	facets := make([]string, 0, len(o.Forward))
	for f, target := range o.Forward {
		if !slices.Contains(known, f) {
			return nil, fmt.Errorf("nodeagent: %q is not a node facet (%v)", f, known)
		}
		if _, _, err := net.SplitHostPort(target); err != nil {
			return nil, fmt.Errorf("nodeagent: forward %s=%q: %w", f, target, err)
		}
		facets = append(facets, f)
	}
	slices.Sort(facets)
	accept := map[string]string{}
	alpns := make([]string, 0, len(facets))
	for _, f := range facets {
		accept[policy.ALPN(f)] = f
		alpns = append(alpns, policy.ALPN(f))
	}

	priv, minted, err := o.State.Key()
	if err != nil {
		return nil, err
	}
	a := &Agent{o: o, log: o.Log, http: o.HTTP, accept: accept, facets: facets, kick: make(chan struct{}, 1)}
	ep, err := irohtransport.Bind(priv, irohtransport.Options{BindAddr: o.BindAddr, Relay: o.Config.Relay, StreamALPNs: alpns, OnConnLost: a.connLost})
	if err != nil {
		return nil, err
	}
	a.ep = ep
	if a.log == nil {
		a.log = log.Default()
	}
	if a.http == nil {
		a.http = &http.Client{Timeout: 30 * time.Second}
	}
	a.actor = actor.New(cert.NewEdSigner(priv), ep)
	a.actor.Clock = o.Clock
	a.actor.RestoreLowWater(o.State.Mark())
	// The hub outlives the agent's restarts (reboot, upgrade) and keeps
	// its seq high-water mark for this node; seed from the clock so the
	// first beat after a restart is not a replay. Microseconds, not
	// nanoseconds: seq must stay exact under the wire's RFC 8785
	// doubles (envelope.MaxSeq).
	a.actor.SeqBase = func() int64 { return time.Now().UnixMicro() }
	a.log.Printf("member %s (key %s) relay %s facets %v", a.ID(), map[bool]string{true: "minted", false: "loaded"}[minted], o.Config.Relay, facets)

	if kit, ok, err := o.State.Kit(); err != nil {
		a.log.Printf("ignoring %s: %v", KitFile, err)
	} else if ok {
		if err := CheckKit(kit, a.ID()); err != nil {
			a.log.Printf("ignoring %s: %v", KitFile, err)
		} else {
			a.kit = &kit
			a.log.Printf("member %q groups %v until %s (issuer %s)", kit.Member.Cav.Name, kit.Member.Cav.Groups, time.Unix(kit.Member.Exp, 0).UTC().Format(time.RFC3339), kit.Member.Iss)
		}
	}
	if b, ok, err := o.State.Bundle(); err != nil {
		a.log.Printf("ignoring %s: %v", BundleFile, err)
	} else if ok {
		a.bundle = &b
	}
	if h, ok, err := o.State.Hub(); err != nil {
		a.log.Printf("ignoring %s: %v", HubFile, err)
	} else if ok {
		a.hub = &h
	}
	a.installAuthority()
	return a, nil
}

// ID is the NodeId.
func (a *Agent) ID() cert.ActorID { return a.actor.ID() }

// Actor exposes the protocol actor (tests, diagnostics).
func (a *Agent) Actor() *actor.Actor { return a.actor }

// Endpoint exposes the iroh endpoint (tests: Endpoints() for hints).
func (a *Agent) Endpoint() *irohtransport.Endpoint { return a.ep }

// Kit returns the current Kit, nil before enrollment.
func (a *Agent) Kit() *issuer.Kit {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.kit
}

// Bundle returns the last bundle, nil before the first beat.
func (a *Agent) Bundle() *issuer.Bundle {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bundle
}

// Beats counts successful beats since Start.
func (a *Agent) Beats() int64 { return a.beats.Load() }

// BeatAttempts counts beats attempted since Start, successful or not.
func (a *Agent) BeatAttempts() int64 { return a.attempts.Load() }

// Close releases the endpoint.
func (a *Agent) Close() error { return a.ep.Close() }

// Run serves until ctx ends: the actor inbox, the stream facets (when
// any are forwarded), and the enroll-then-beat loop. It returns
// ErrTokenDead when the node cannot enroll and no fresh config will
// arrive by itself.
func (a *Agent) Run(ctx context.Context) error {
	go func() {
		if err := a.actor.Listen(ctx); err != nil && ctx.Err() == nil {
			a.log.Printf("actor inbox: %v", err)
		}
	}()
	if len(a.facets) > 0 {
		go a.serveConns(ctx)
	}

	backoff := minBackoff
	for a.Kit() == nil {
		err := a.enroll(ctx)
		if err == nil {
			break
		}
		if errors.Is(err, ErrTokenDead) {
			return err
		}
		wait := backoff
		if errors.Is(err, ErrHubSealed) {
			wait = a.minRebeat() // same as the beat: a sealed hub is polled flat
		} else {
			backoff = min(backoff*2, maxBackoff)
		}
		a.log.Printf("enroll: %v (retry in %s)", err, wait)
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}

	every := a.o.BeatEvery
	if every <= 0 {
		every = DefaultBeat
	}
	backoff = minBackoff
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			wait := every
			if err := a.Beat(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, ErrHubSealed) {
					// Not an outage: the hub is up and waiting for the
					// wallet. Poll it flat so the unseal is one MinRebeat
					// from every member's beat; the backoff is untouched.
					wait = a.minRebeat()
				} else {
					wait, backoff = backoff, min(backoff*2, maxBackoff)
				}
				a.log.Printf("beat: %v (retry in %s)", err, wait)
			} else {
				backoff = minBackoff
			}
			timer.Reset(wait)
		case <-a.kick:
			// Off-schedule: pull the timer in to the earliest moment the
			// rate limit allows (now, when the last attempt is MinRebeat
			// old). Never later than its date; a kick storm collapses
			// into one beat per MinRebeat.
			timer.Reset(a.untilRebeat())
		}
	}
}

// Kick asks the running loop to beat as soon as the rate limit allows —
// the asynchronous form of staleness evidence, for callers that cannot
// wait (a DNS answer, a lost connection, a newer speak-as on an admitted
// stream): the next lookup sees the fresh map. The beat is scheduled,
// not dropped, when one is more recent than MinRebeat.
func (a *Agent) Kick() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *Agent) minRebeat() time.Duration {
	if a.o.MinRebeat > 0 {
		return a.o.MinRebeat
	}
	return DefaultMinRebeat
}

// untilRebeat is how long the rate limit still holds: zero when the
// last attempt is MinRebeat old or older.
func (a *Agent) untilRebeat() time.Duration {
	return max(0, a.minRebeat()-time.Since(time.Unix(0, a.lastBeat.Load())))
}

func (a *Agent) rebeatDue() bool { return a.untilRebeat() == 0 }

// connLost is the transport's word that a pooled connection ended
// (irohtransport.Options.OnConnLost). When the peer is the hub, the
// process this member beat against is gone — a redeploy, most likely,
// under a key that will differ — and this is the only evidence a node
// that dials nothing between beats ever sees (decision z2go).
func (a *Agent) connLost(peer cert.ActorID) {
	a.mu.Lock()
	h := a.hub
	a.mu.Unlock()
	if h == nil || h.ID() != peer {
		return
	}
	a.log.Printf("connection to hub %s lost; beating", peer)
	a.Kick()
}

// noticeRotation is the receiver-side evidence (decision z2go): an
// admitted caller carried a wallet-signed speak-as for a hubkey this
// member has not beaten against, issued after the one it holds — the
// caller has seen a newer hub. Only rooted certs are consulted
// (Result.Verified: signature provenance to this node's own consent),
// and only on admission: a refused stranger schedules nothing (5qt).
func (a *Agent) noticeRotation(res cert.Result) {
	if !res.OK {
		return
	}
	a.mu.Lock()
	h, kit := a.hub, a.kit
	a.mu.Unlock()
	if h == nil || kit == nil {
		return
	}
	for _, c := range res.Verified {
		// >= not >: two unseals within one second are a rotation too, and
		// what the comparison must exclude is a caller with an OLDER hub.
		if c.Can == cert.VerbSpeakAs && c.Iss == kit.SpeakAs.Iss && cert.ActorID(c.Aud) != h.ID() && c.Iat >= h.SpeakAs.Iat {
			a.log.Printf("caller presented hub %s (speak-as iat %d > ours %d); beating", c.Aud, c.Iat, h.SpeakAs.Iat)
			a.Kick()
			return
		}
	}
}

// rebeat beats now unless one was attempted within MinRebeat, and
// reports whether it did. The synchronous form: Dial retries on the
// fresh directory once it returns. A failed rebeat is logged, not
// returned — the caller's own error is the one that matters.
func (a *Agent) rebeat(ctx context.Context) bool {
	a.beatMu.Lock()
	defer a.beatMu.Unlock()
	if !a.rebeatDue() {
		return false
	}
	if err := a.beat(ctx); err != nil && ctx.Err() == nil {
		a.log.Printf("rebeat: %v", err)
	}
	return true
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// enroll redeems the boot token.
func (a *Agent) enroll(ctx context.Context) error {
	if a.o.Config.Token == "" {
		return fmt.Errorf("%w: no kit and no token in config", ErrTokenDead)
	}
	kit, err := Enroll(ctx, a.http, a.o.Config.Hub, a.ID(), a.o.Config.Token)
	if err != nil {
		return err
	}
	if err := a.o.State.SaveKit(kit); err != nil {
		return err
	}
	a.mu.Lock()
	a.kit = &kit
	a.mu.Unlock()
	a.installAuthority()
	a.log.Printf("enrolled: member %q groups %v (issuer %s)", kit.Member.Cav.Name, kit.Member.Cav.Groups, kit.Member.Iss)
	return nil
}

// installAuthority (re)signs the node's consent grant to the Owner — the
// root every caller chain must reach — for exactly the forwarded facets,
// and installs it as the actor's authority. Before enrollment there is
// no Owner to consent to, and a caller-only member serves nothing: in
// both cases the inbox refuses everything.
func (a *Agent) installAuthority() {
	kit := a.Kit()
	if kit == nil || len(a.facets) == 0 {
		a.actor.Hold(nil, nil)
		return
	}
	now := a.actor.Now()
	consent, err := cert.Sign(cert.Cert{
		Aud: string(kit.SpeakAs.Iss),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{a.ID()}, Facet: slices.Clone(a.facets), Delegable: true},
		Iat: now,
		Exp: now + ConsentTTL,
	}, a.actor.Signer)
	if err != nil {
		a.log.Printf("signing consent: %v", err)
		return
	}
	a.actor.Hold([]cert.Cert{consent}, nil)
}

// Beat is one renewal beat against the hub.
func (a *Agent) Beat(ctx context.Context) error {
	a.beatMu.Lock()
	defer a.beatMu.Unlock()
	return a.beat(ctx)
}

func (a *Agent) beat(ctx context.Context) error {
	a.lastBeat.Store(time.Now().UnixNano())
	a.attempts.Add(1)
	a.installAuthority()
	kit := a.Kit()
	if kit == nil {
		return errors.New("nodeagent: not enrolled")
	}
	hub, err := a.locateHub(ctx, kit)
	if err != nil {
		return err
	}
	if err := a.actor.UpdateLocation(hub.ID(), &hub.ReachMeAt); err != nil {
		return fmt.Errorf("nodeagent: hub location: %w", err)
	}
	a.grantBeat(kit, hub)
	if _, err := a.actor.PublishLocation(LocationTTL); err != nil {
		return err
	}

	now := a.actor.Now()
	if a.renewDue(kit, hub.ID(), now) {
		renewed, err := a.renew(ctx, kit, hub)
		if err != nil {
			a.log.Printf("renew: %v (beating with the held certs)", err)
		} else {
			kit = renewed
			a.grantBeat(kit, hub)
		}
	}

	req, err := issuer.EncodeBundleRequest(kit.Member)
	if err != nil {
		return err
	}
	rep, err := a.send(ctx, hub.ID(), issuer.FacetBundle, req)
	if err != nil {
		return fmt.Errorf("nodeagent: #bundle: %w", err)
	}
	b, err := issuer.DecodeBundle(rep.Payload)
	if err != nil {
		return err
	}
	b.NameMap = a.mergeNames(b)
	if err := a.o.State.SaveBundle(b); err != nil {
		return err
	}
	a.mu.Lock()
	a.bundle = &b
	a.mu.Unlock()
	if err := a.o.State.SaveMark(a.actor.LowWater()); err != nil {
		a.log.Printf("persisting mark: %v", err)
	}
	a.beats.Add(1)
	a.log.Printf("beat ok: %d grants, %d blocked, %d names, hub %s", len(b.Grants), len(b.Blocklist), len(b.NameMap), hub.ID())
	return nil
}

// dialTimeout bounds one dial: DialTimeout or its default.
func (a *Agent) dialTimeout() time.Duration {
	if a.o.DialTimeout > 0 {
		return a.o.DialTimeout
	}
	return DefaultDialTimeout
}

// send is one beat invocation, bounded by DialTimeout like every
// candidate dial in Dial: a hub record that names a dead key (the
// well-known answered before the URL moved) otherwise holds the beat —
// and beatMu with it — for iroh's own connect timeout.
func (a *Agent) send(ctx context.Context, to cert.ActorID, facet string, payload []byte) (*actor.Reply, error) {
	sctx, cancel := context.WithTimeout(ctx, a.dialTimeout())
	defer cancel()
	rep, err := a.actor.Send(sctx, to, facet, payload)
	if err != nil && sctx.Err() != nil && ctx.Err() == nil {
		err = fmt.Errorf("%w: no answer in %s", actor.ErrUnreachable, a.dialTimeout())
	}
	return rep, err
}

// locateHub fetches the hub's current speak-as + reach-me-at, pinned to
// the wallet the Kit names; on a WAN failure the cached record serves
// while its certs are live.
func (a *Agent) locateHub(ctx context.Context, kit *issuer.Kit) (HubRecord, error) {
	h, err := FetchHub(ctx, a.http, a.o.Config.Hub, kit.SpeakAs.Iss)
	if errors.Is(err, ErrHubSealed) {
		// The hub answered: it holds no speak-as yet. Whatever the
		// cache names is a dead key or a sealed one; dialing it only
		// turns "sealed" into a 15 s "unreachable".
		return HubRecord{}, err
	}
	if err == nil {
		if err := a.o.State.SaveHub(h); err != nil {
			a.log.Printf("persisting hub record: %v", err)
		}
		a.mu.Lock()
		a.hub = &h
		a.mu.Unlock()
		return h, nil
	}
	a.mu.Lock()
	cached := a.hub
	a.mu.Unlock()
	now := a.actor.Now()
	if cached != nil && cached.SpeakAs.Iss == kit.SpeakAs.Iss && cached.SpeakAs.Exp > now && cached.ReachMeAt.Exp > now {
		a.log.Printf("hub fetch: %v (using cached record for %s)", err, cached.ID())
		return *cached, nil
	}
	return HubRecord{}, err
}

// grantBeat installs the caller-held chains for #renew and #bundle at
// the current hubkey: the beat grant plus every speak-as that may be
// needed to resolve its issuer (the Kit's, and the hub's current one
// after a rotation).
func (a *Agent) grantBeat(kit *issuer.Kit, hub HubRecord) {
	chain := []cert.Cert{kit.BeatGrant, kit.SpeakAs}
	if hub.SpeakAs.Aud != kit.SpeakAs.Aud {
		chain = append(chain, hub.SpeakAs)
	}
	for _, f := range issuer.BeatFacets {
		a.actor.Grant(hub.ID(), f, chain...)
	}
}

// renewDue: a Kit cert is renewed when it is past half its lifetime, or
// when the hub rotated (what the dead key signed renews at the live one
// while the dead key's speak-as still resolves it).
func (a *Agent) renewDue(kit *issuer.Kit, hubID cert.ActorID, now int64) bool {
	for _, c := range []cert.Cert{kit.Member, kit.BeatGrant} {
		if c.Iss != hubID || c.Exp-now < (c.Exp-c.Iat)/2 {
			return true
		}
	}
	return false
}

// renew re-signs the member cert and beat grant at the current hubkey
// and persists the new Kit, whose speak-as is now the hub's current one.
func (a *Agent) renew(ctx context.Context, kit *issuer.Kit, hub HubRecord) (*issuer.Kit, error) {
	req, err := actor.EncodeRenewRequest([]cert.Cert{kit.Member, kit.BeatGrant}, nil)
	if err != nil {
		return nil, err
	}
	rep, err := a.send(ctx, hub.ID(), actor.FacetRenew, req)
	if err != nil {
		return nil, err
	}
	certs, errs, err := actor.DecodeRenewResponse(rep.Payload)
	if err != nil {
		return nil, err
	}
	if len(certs) != 2 || errs[0] != nil || errs[1] != nil {
		return nil, fmt.Errorf("refused: %v", errs)
	}
	renewed := issuer.Kit{Member: certs[0], BeatGrant: certs[1], SpeakAs: hub.SpeakAs}
	if err := CheckKit(renewed, a.ID()); err != nil {
		return nil, err
	}
	if err := a.o.State.SaveKit(renewed); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.kit = &renewed
	a.mu.Unlock()
	a.log.Printf("renewed: member until %s at %s", time.Unix(renewed.Member.Exp, 0).UTC().Format(time.RFC3339), renewed.Member.Iss)
	return &renewed, nil
}

// mergeNames folds the member's own last name map into the one the hub
// just sent (mergeNameMaps).
func (a *Agent) mergeNames(fresh issuer.Bundle) []issuer.NameEntry {
	a.mu.Lock()
	prev := a.bundle
	a.mu.Unlock()
	if prev == nil {
		return fresh.NameMap
	}
	return mergeNameMaps(prev.NameMap, fresh, a.actor.Now())
}

// blocklist is the receiver-side copy from the last bundle.
func (a *Agent) blocklist() map[cert.ActorID]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bundle == nil {
		return nil
	}
	m := make(map[cert.ActorID]bool, len(a.bundle.Blocklist))
	for _, id := range a.bundle.Blocklist {
		m[id] = true
	}
	return m
}

// serveConns accepts stream-facet connections for the life of ctx.
func (a *Agent) serveConns(ctx context.Context) {
	for {
		c, err := a.ep.AcceptConn(ctx)
		if err != nil {
			return
		}
		go a.handleConn(ctx, c)
	}
}

// Authorize runs the receiver's decision for one connection: the
// caller's bundle against this node's consent, accept table and
// blocklist, at the effective clock. Exposed for tests.
func (a *Agent) Authorize(alpn string, peer cert.ActorID, b cert.Bundle) cert.Result {
	consents, speakAs := a.actor.Authority()
	res := cert.Authorize(cert.Input{
		Receiver:    cert.Receiver{ID: a.ID(), Consents: consents, SpeakAs: speakAs},
		AcceptTable: a.accept,
		Blocklist:   a.blocklist(),
		Now:         a.actor.Now(),
		ALPN:        alpn,
		Peer:        peer,
		Bundle:      b,
	})
	a.actor.Observe(res.Verified)
	return res
}

func (a *Agent) handleConn(ctx context.Context, c *irohtransport.Conn) {
	defer c.Close()
	pre, err := c.Preamble(ctx)
	if err != nil {
		return
	}
	b, err := cert.DecodeBundle(pre)
	if err != nil {
		_ = c.Refuse(ctx, "malformed bundle")
		return
	}
	res := a.Authorize(c.ALPN(), c.Peer(), b)
	if !res.OK {
		a.log.Printf("refused %s on %s", c.Peer(), c.ALPN())
		_ = c.Refuse(ctx, "not authorized")
		return
	}
	a.noticeRotation(res)
	facet := a.accept[c.ALPN()]
	target := a.o.Forward[facet]
	if err := c.Admit(ctx); err != nil {
		return
	}
	a.log.Printf("admitted %q %v (%s) → %s %s", res.Identity.Name, res.Identity.Groups, c.Peer(), facet, target)
	for {
		raw, err := c.Accept(ctx)
		if err != nil {
			return
		}
		go splice(raw, target)
	}
}

// splice pipes one forward stream to a fresh TCP connection to target,
// half-closes propagating both ways.
func splice(raw *irohtransport.Raw, target string) {
	defer raw.Close()
	tcp, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return
	}
	defer tcp.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(tcp, raw)
		_ = tcp.(*net.TCPConn).CloseWrite()
	}()
	_, _ = io.Copy(raw, tcp)
	_ = raw.CloseWrite()
	<-done
}
