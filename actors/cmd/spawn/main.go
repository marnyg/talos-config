//go:build iroh

// Command spawn is the v0 parent (protocol ADR-0008/0009, 0bc.4.6): a
// laptop actor that rents one child from a provisioner and watches it
// live and lapse. It is the acceptance run's instrument, not a
// product — one spawn per process, everything logged.
//
//	spawn -state DIR -print-id
//	spawn -state DIR -lighthouse ed:… -provisioner-id ed:… -image ghcr.io/…@sha256:…
//	spawn -state DIR -provisioner location.json -image ghcr.io/…@sha256:…
//
// Identity: -state DIR holds the key (a 32-byte seed, minted on first
// run). It persists because the provisioner's -customer consent names
// this id at ITS start: print the id first, start the provisioner with
// it, then spawn. Nothing else in DIR.
//
// Finding the provisioner (0bc.6): -lighthouse + -provisioner-id
// #lookup the provisioner's record at a lighthouse both are -member of
// (protocol ADR-0007) — the lighthouse is dialled on -relay alone the
// first time (actor.Bootstrap), and the looked-up record is validated
// as the provisioner's own before it is used. The alternative,
// -provisioner, is the location.json the provisioner's process writes
// (a signed, expiring reach-me-at), copied out of band. Either way the
// first reply's piggyback keeps the record fresh after that.
//
// Life of the run: #spawn → the child knocks on #birth (the promise
// resolves: id, location, lease) → one #ping on the child's own
// consent (P→C works) → the child's beat re-#renews its kit chain and
// the spawner's decorator turns each later exp into #extend at the
// provisioner. After -renewals of those this parent STOPS answering
// #renew (a grantor is free not to re-issue; revocation is expiry),
// keeps sweeping on -beat, and exits 0 when its born table no longer
// holds the child — the lease's last asked deadline passed, which is
// when the child exits itself (child.ErrLapsed) and the provisioner
// sweeps it. -renewals 0 renews until SIGINT.
//
// Clocks: -kit-ttl must exceed -window for #extend to fire on the
// child's first beat (the lease's first deadline is the window's end,
// and #extend only fires when a re-issued exp passes it).
//
// The agent (talos-config-e6pu.2, the seed of the CLI, hp79): with
// -prompt the parent is also the child's event sink. It binds the
// agent-events stream facet (child.FacetAgentEvents), outfits the kit
// with one invoke grant on it (held as its own consent, so the child's
// one-link chain roots), admits each connection with cert.VerifyChain
// over the preamble's chain, serves it with facethttp: every line of
// `POST /events/<task-id>` goes to stdout as `event <child-id> <line>`,
// `PUT /session/<task-id>` lands in -session-dir. After #ping it sends
// one #task {prompt} and logs the ack; the outcome is the stream's last
// line (child_settled).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/actors/cmd/internal/boot"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/facethttp"
	"github.com/marnyg/talos-config/protocol/lighthouse"
	"github.com/marnyg/talos-config/protocol/spawn"
)

func main() {
	pb := boot.Flags("parent", filepath.Join(os.Getenv("HOME"), ".sap-parent"))
	var lhFlag, provFlag boot.ID
	flag.Var(&lhFlag, "lighthouse", "lighthouse actor id to #lookup the provisioner at (this id must be one of its -member); dialled on -relay")
	flag.Var(&provFlag, "provisioner-id", "the provisioner's actor id, with -lighthouse")
	var (
		provFile = flag.String("provisioner", "", "the provisioner's location.json (its signed reach-me-at); the no-lighthouse path")
		image    = flag.String("image", "", "child image by content digest (name@sha256:…)")
		window   = flag.Duration("window", 3*time.Minute, "birth window: how long the child has to knock (pull time included)")
		kitTTL   = flag.Duration("kit-ttl", 5*time.Minute, "lifetime of the child's #renew chain per issue; keep above -window")
		renewals = flag.Int("renewals", 1, "answered #renew→#extend rounds before this parent stops re-issuing; 0 = forever")
		beat     = flag.Duration("beat", 30*time.Second, "sweep + reach-me-at interval")
		locTTL   = flag.Duration("location-ttl", 10*time.Minute, "lifetime of each published reach-me-at; keep well above -beat")
		prompt   = flag.String("prompt", "", "send the born child one #task with this prompt and print its event stream (empty: no task)")
		sessDir  = flag.String("session-dir", "", "where a task's session JSONL (PUT /session/<id>) is written (default -state)")
	)
	flag.Parse()
	pb.BindStreams = []string{child.FacetAgentEvents}
	a, ep := pb.Up() // -print-id (the provisioner's -customer, the lighthouse's -member) exits here
	defer ep.Close()
	if *sessDir == "" {
		*sessDir = pb.State()
	}
	lh, prov := lhFlag.ActorID(), provFlag.ActorID()
	byLighthouse := lh != "" || prov != ""
	switch {
	case *image == "", byLighthouse == (*provFile != ""):
		log.Fatal("need -image name@sha256:… and either -lighthouse + -provisioner-id or -provisioner location.json (or -print-id)")
	case byLighthouse && (lh == "" || prov == ""):
		log.Fatal("-lighthouse and -provisioner-id go together")
	}
	var provLoc *cert.Cert
	if !byLighthouse {
		raw, err := os.ReadFile(*provFile)
		if err != nil {
			log.Fatal(err)
		}
		loc, err := cert.DecodeCert(raw)
		if err != nil {
			log.Fatalf("%s: %v", *provFile, err)
		}
		provLoc, prov = &loc, loc.Iss
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if byLighthouse {
		pb.ViaRelay(a, lh)
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		recs, err := lighthouse.Lookup(lctx, a, lh, prov)
		cancel()
		if err != nil {
			log.Fatalf("parent: #lookup at %s: %v", lh, err)
		}
		rec, ok := recs[prov]
		if !ok {
			log.Fatalf("parent: #lookup at %s: no live record for %s (is the provisioner up and a -member there?)", lh, prov)
		}
		slog.Info("parent: #lookup ok", "lighthouse", lh, "provisioner", prov, "exp", rec.Loc.Exp, "endpoints", rec.Loc.Cav.Endpoints)
	} else {
		if provLoc.Exp <= a.Now() {
			slog.Warn("parent: provisioner location expired; the #spawn will likely not reach it", "exp", provLoc.Exp)
		}
		if err := a.UpdateLocation(prov, provLoc); err != nil {
			log.Fatalf("%s: %v", *provFile, err)
		}
	}

	sp := spawn.New(a)
	sp.Provisioner = prov
	sp.Window = int64(window.Seconds())
	sp.KitTTL = int64(kitTTL.Seconds())
	var extends atomic.Int64
	deaf := gate(a, sp, *renewals, &extends)

	listen := make(chan error, 1)
	go func() { listen <- a.Listen(ctx) }()
	publish := func() {
		if _, err := a.PublishLocation(int64(locTTL.Seconds())); err != nil {
			slog.Warn("parent: location", "err", err)
		}
	}
	publish()
	slog.Info("parent: up", "id", a.ID(), "provisioner", prov, "relay", pb.Relay())

	// The agent-events facet: served for the life of the run, admitting
	// only the born child (set once the birth resolves).
	sink := &sink{a: a, dir: *sessDir}
	srv, ln := facethttp.Server(child.FacetAgentEvents, sink.handler())
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	go sink.accept(ctx, ep, ln)

	// #spawn, then the birth.
	sctx, cancel := context.WithTimeout(ctx, *window)
	pr, err := sp.Spawn(sctx, spawn.Spec{Image: *image, Outfit: outfit(a, sp)})
	if err != nil {
		cancel()
		log.Fatalf("parent: #spawn: %v", err)
	}
	slog.Info("parent: #spawn accepted; waiting for #birth", "window", *window)
	b, err := pr.Wait(sctx)
	cancel()
	if err != nil {
		log.Fatalf("parent: birth: %v", err)
	}
	_, until, _ := sp.Child(b.ID)
	sink.child.Store(string(b.ID))
	slog.Info("parent: born", "child", b.ID, "lease", b.Lease.ID, "until", until, "location", b.Location != nil)

	// P→C on the child's own consent. The promise resolved at #birth,
	// before Born installed that consent on the child: retry briefly.
	if ping(ctx, a, b.ID) && *prompt != "" {
		task(ctx, a, b.ID, *prompt)
	}

	t := time.NewTicker(*beat)
	defer t.Stop()
	last := until
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-listen:
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Fatal(err)
			}
			break loop
		case <-t.C:
		}
		sp.Sweep()
		publish()
		_, until, ok := sp.Child(b.ID)
		switch {
		case !ok:
			slog.Info("parent: lapsed — the child's last deadline passed", "child", b.ID, "extends", extends.Load())
			break loop
		case until != last:
			last = until
			slog.Info("parent: lease", "child", b.ID, "until", until, "in", time.Until(time.Unix(until, 0)).Round(time.Second), "extends", extends.Load(), "renewing", !deaf.Load())
		}
	}
	stop()
	slog.Info("parent: exit", "extends", extends.Load())
}

// gate wraps the parent's #renew handler (the spawner's decorator
// included, so an answered round has already sent its #extend): each
// call that moves the caller's lease deadline counts as one extend, and
// once n of them happened the handler refuses instead. n <= 0 never
// refuses. Returned is the flag that says it has gone deaf.
func gate(a *actor.Actor, sp *spawn.Spawner, n int, extends *atomic.Int64) *atomic.Bool {
	deaf := new(atomic.Bool)
	inner := a.AcceptTable[actor.FacetRenew]
	a.AcceptTable[actor.FacetRenew] = func(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
		if deaf.Load() {
			slog.Info("parent: #renew refused (done renewing)", "from", inv.From)
			return nil, errors.New("parent: not renewing")
		}
		_, before, _ := sp.Child(inv.From)
		body, err := inner(ctx, inv)
		if err != nil {
			return body, err
		}
		_, after, ok := sp.Child(inv.From)
		if ok && after > before {
			k := extends.Add(1)
			slog.Info("parent: #renew answered → #extend", "child", inv.From, "until", after, "round", k)
			if n > 0 && k >= int64(n) {
				deaf.Store(true)
				slog.Info("parent: done renewing; the child lapses when its chain expires", "child", inv.From, "chain_exp", after)
			}
		} else {
			slog.Info("parent: #renew answered, no extend (exp not past the lease deadline)", "child", inv.From)
		}
		return body, nil
	}
	return deaf
}

// ping proves P→C once: the child's #ping under its own consent, empty
// chain. Failure is logged, not fatal — the run is about the lease.
func ping(ctx context.Context, a *actor.Actor, id cert.ActorID) bool {
	deadline := time.Now().Add(30 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		var rep *actor.Reply
		if rep, err = a.Send(ctx, id, child.FacetPing, []byte(`"hi"`)); err == nil {
			slog.Info("parent: #ping ok", "child", id, "reply", string(rep.Payload))
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	slog.Warn("parent: #ping failed", "child", id, "err", err)
	return false
}

// task sends the one #task; the ack is logged, the run itself shows
// up on stdout through the sink. Failure is logged, not fatal.
func task(ctx context.Context, a *actor.Actor, id cert.ActorID, prompt string) {
	payload, err := json.Marshal(child.TaskRequest{Prompt: prompt})
	if err != nil {
		log.Fatal(err)
	}
	rep, err := a.Send(ctx, id, child.FacetTask, payload)
	if err != nil {
		slog.Warn("parent: #task failed", "child", id, "err", err)
		return
	}
	slog.Info("parent: #task ok", "child", id, "reply", string(rep.Payload))
}

// outfit is the kit's extra: one invoke grant on (parent, agent-events)
// for the kit's lifetime, held as this actor's own consent so the
// one-link chain the child presents on connect roots (the shape of the
// spawner's mandated #renew chain, spawn.go mintKit). The #renew beat
// re-issues it and reinstalls the fresh one beside (actor.renewHandler
// step 6); the spawner's own consents cover #birth and #renew only.
func outfit(a *actor.Actor, sp *spawn.Spawner) func(cert.ActorID, int64) (spawn.Kit, error) {
	return func(c cert.ActorID, now int64) (spawn.Kit, error) {
		g, err := cert.Sign(cert.Cert{
			Aud: string(c),
			Can: cert.VerbInvoke,
			Cav: cert.Caveats{Target: []cert.ActorID{a.ID()}, Facet: []string{child.FacetAgentEvents}},
			Iat: now,
			Exp: now + sp.KitTTL,
		}, a.Signer)
		if err != nil {
			return spawn.Kit{}, err
		}
		a.EditConsents(func(cs []cert.Cert) []cert.Cert { return append(cs, g) })
		return spawn.Kit{Grants: [][]cert.Cert{{g}}}, nil
	}
}

// sink terminates the agent-events facet: the accept loop decides each
// connection (VerifyChain over the preamble, the protocol's own rule),
// the handler prints events and keeps sessions.
type sink struct {
	a     *actor.Actor
	dir   string
	child atomic.Value // string: the born child's id, "" before birth
}

var taskID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (s *sink) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		peer, ok := facethttp.PeerFrom(req.Context())
		if !ok || !s.isChild(peer) || !taskID.MatchString(id) {
			http.Error(w, "not the born child", http.StatusForbidden)
			return
		}
		slog.Info("parent: events stream open", "child", peer, "task", id)
		r := bufio.NewReader(req.Body)
		n := 0
		for {
			line, err := r.ReadBytes('\n')
			if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
				n++
				fmt.Printf("event %s %s\n", peer, line)
			}
			if err != nil {
				if err != io.EOF {
					slog.Warn("parent: events stream", "task", id, "err", err)
				}
				break
			}
		}
		slog.Info("parent: events stream closed", "child", peer, "task", id, "lines", n)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /session/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		peer, ok := facethttp.PeerFrom(req.Context())
		if !ok || !s.isChild(peer) || !taskID.MatchString(id) {
			http.Error(w, "not the born child", http.StatusForbidden)
			return
		}
		path := filepath.Join(s.dir, "session-"+id+".jsonl")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n, err := io.Copy(f, req.Body)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			slog.Warn("parent: session", "task", id, "path", path, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("parent: session written", "child", peer, "task", id, "path", path, "bytes", n)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (s *sink) isChild(id cert.ActorID) bool {
	c, _ := s.child.Load().(string)
	return c != "" && c == string(id)
}

// accept runs the facet's accept loop for the life of ctx: every
// connection on agent-events is decided by handleConn.
func (s *sink) accept(ctx context.Context, ep *irohtransport.Endpoint, ln *facethttp.Listener) {
	for {
		c, err := ep.AcceptConn(ctx)
		if err != nil {
			return
		}
		go s.handleConn(ctx, c, ln)
	}
}

// handleConn is config-server's handleFacetConn shape with the
// protocol's own admission: the preamble's chain verified under this
// actor's consents for invoke on agent-events, signed by the QUIC
// peer; verified certs feed the clock mark either way.
func (s *sink) handleConn(ctx context.Context, c *irohtransport.Conn, ln *facethttp.Listener) {
	defer c.Close()
	pre, err := c.Preamble(ctx)
	if err != nil {
		return
	}
	chain, speakAs, err := child.DecodePreamble(pre)
	if err != nil {
		slog.Warn("parent: agent-events: malformed preamble", "peer", c.Peer(), "err", err)
		_ = c.Refuse(ctx, "malformed preamble")
		return
	}
	consents, held := s.a.Authority()
	rcv := cert.Receiver{ID: s.a.ID(), Consents: consents, SpeakAs: held}
	_, verified, err := cert.VerifyChain(rcv, cert.VerbInvoke, chain, speakAs, c.Peer(), child.FacetAgentEvents, s.a.Now())
	s.a.Observe(verified)
	if err != nil {
		slog.Warn("parent: agent-events: refused", "peer", c.Peer(), "err", err)
		_ = c.Refuse(ctx, "not authorized")
		return
	}
	if err := c.Admit(ctx); err != nil {
		return
	}
	slog.Info("parent: agent-events: admitted", "peer", c.Peer())
	for {
		raw, err := c.Accept(ctx)
		if err != nil {
			return
		}
		if !ln.Push(ctx, raw, cert.Identity{Key: c.Peer()}, c.Peer()) {
			_ = raw.Close()
			return
		}
	}
}
