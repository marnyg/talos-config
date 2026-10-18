package parent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/facethttp"
	"github.com/marnyg/talos-config/protocol/spawn"
)

// Options configures a Parent. Actor and Spawner are the caller's
// (spawn.New on the actor, Provisioner/Window/KitTTL set); New wraps
// the actor's #renew once more, so call it after spawn.New and before
// Listen — the same ordering rule as the spawner's own decorator.
type Options struct {
	Actor   *actor.Actor
	Spawner *spawn.Spawner
	Store   *Store
	// Image is the agent child image by digest, what every #spawn names.
	Image string
	// Beat is the housekeeping interval (sweep, snapshot, retire); 0 ⇒ 30 s.
	Beat time.Duration
	// Grace is how long a restarted parent gives an adopted child to
	// knock before its stale deadline sweeps it, and how long a settled
	// child has to ship its session before its lease is killed; 0 ⇒ 2 min.
	Grace time.Duration
	Log   *slog.Logger
}

// Parent is the agent backend's actor side (ADR-0037): one actor that
// spawns a child per session, is the sink of their streams, keeps the
// store, and answers the surface the web layer puts behind the person
// gate. Transport-free: the caller runs the actor's Listen and binds
// the agent-events accept loop to Admit + Handler.
type Parent struct {
	a     *actor.Actor
	sp    *spawn.Spawner
	store *Store
	image string
	beat  time.Duration
	grace time.Duration
	log   *slog.Logger

	mu      sync.Mutex
	retired map[cert.ActorID]time.Time // settled children awaiting Kill
}

// New builds the parent and installs its #renew gate: a child whose
// session has ended is refused renewal, so a lease outlives its run
// by at most the kit TTL even if Kill fails.
func New(o Options) *Parent {
	p := &Parent{a: o.Actor, sp: o.Spawner, store: o.Store, image: o.Image, beat: o.Beat, grace: o.Grace, log: o.Log, retired: make(map[cert.ActorID]time.Time)}
	if p.beat <= 0 {
		p.beat = 30 * time.Second
	}
	if p.grace <= 0 {
		p.grace = 2 * time.Minute
	}
	if p.log == nil {
		p.log = slog.Default()
	}
	inner := p.a.AcceptTable[actor.FacetRenew]
	p.a.AcceptTable[actor.FacetRenew] = func(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
		if _, err := p.store.ByChild(inv.From, true); err != nil {
			p.log.Info("parent: #renew refused: no live session", "child", inv.From)
			return nil, errors.New("parent: not renewing: no live session")
		}
		return inner(ctx, inv)
	}
	return p
}

// Restore refills the actor from the store before Listen: the consents
// this key issued, the born table (spawn.Adopt, deadlines floored at
// now+Grace), and every live session whose child is not among them is
// lost. Returns the ids it lost.
func (p *Parent) Restore() ([]string, error) {
	now := p.a.Now()
	cs, err := p.store.Consents(now)
	if err != nil {
		return nil, err
	}
	if len(cs) > 0 {
		p.a.EditConsents(func(have []cert.Cert) []cert.Cert { return append(have, cs...) })
	}
	kids, err := p.store.Children(now + int64(p.grace.Seconds()))
	if err != nil {
		return nil, err
	}
	p.sp.Adopt(kids)
	alive := make(map[cert.ActorID]bool, len(kids))
	for _, k := range kids {
		alive[k.ID] = true
	}
	lost, err := p.store.LoseLive("parent restarted before the run settled", func(c cert.ActorID) bool { return alive[c] })
	if err != nil {
		return lost, err
	}
	p.log.Info("parent: restored", "consents", len(cs), "children", len(kids), "lost", len(lost))
	return lost, nil
}

// Run is the housekeeping loop until ctx ends: Beat every Options.Beat.
func (p *Parent) Run(ctx context.Context) {
	t := time.NewTicker(p.beat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Beat(ctx)
		}
	}
}

// Beat does one round: sweep the spawner, snapshot the born table and
// consents to the store, lose live sessions whose child lapsed, and
// kill the leases of settled children past their grace.
func (p *Parent) Beat(ctx context.Context) {
	p.sp.Sweep()
	kids := p.sp.Children()
	if err := p.store.PutChildren(kids); err != nil {
		p.log.Warn("parent: snapshot children", "err", err)
	}
	cs, _ := p.a.Authority()
	if err := p.store.PutConsents(cs, p.a.Now()); err != nil {
		p.log.Warn("parent: snapshot consents", "err", err)
	}
	alive := make(map[cert.ActorID]bool, len(kids))
	for _, k := range kids {
		alive[k.ID] = true
	}
	// A running session whose child left the born table lapsed there
	// (it stopped renewing, or the provisioner swept it).
	lost, err := p.store.LoseLive("child lapsed", func(c cert.ActorID) bool { return c == "" || alive[c] })
	if err != nil {
		p.log.Warn("parent: lose lapsed", "err", err)
	}
	for _, id := range lost {
		p.log.Info("parent: session lost: child lapsed", "session", id)
	}
	// Retire: a born child with no live session is done; give it Grace
	// from the moment we noticed (its session upload may be in flight),
	// then #kill its lease.
	p.mu.Lock()
	now := time.Now()
	var kill []cert.ActorID
	for _, k := range kids {
		if _, err := p.store.ByChild(k.ID, true); err == nil {
			delete(p.retired, k.ID)
			continue
		}
		since, ok := p.retired[k.ID]
		if !ok {
			p.retired[k.ID] = now
			continue
		}
		if now.Sub(since) >= p.grace {
			kill = append(kill, k.ID)
		}
	}
	for id := range p.retired {
		if !alive[id] {
			delete(p.retired, id)
		}
	}
	p.mu.Unlock()
	for _, id := range kill {
		p.kill(ctx, id, "settled, grace passed")
	}
}

func (p *Parent) kill(ctx context.Context, id cert.ActorID, why string) {
	kctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := p.sp.Kill(kctx, id); err != nil {
		p.log.Warn("parent: #kill failed", "child", id, "why", why, "err", err)
		return
	}
	p.log.Info("parent: #kill", "child", id, "why", why)
	p.mu.Lock()
	delete(p.retired, id)
	p.mu.Unlock()
}

// ---- submit / steer / abort -------------------------------------------------

// Submit records a new session for owner and starts its run in the
// background: #spawn, the birth, #ping, #task. The session is returned
// pending; its progress is its stream. Failures before the ack end it
// failed with the reason.
func (p *Parent) Submit(ctx context.Context, owner string, req child.TaskRequest) (Session, error) {
	if req.Prompt == "" {
		return Session{}, errors.New("parent: empty prompt")
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Session{}, err
	}
	sess := Session{ID: hex.EncodeToString(b[:]), Owner: owner, Prompt: req.Prompt, Repo: req.Repo, Ref: req.Ref, Title: req.Title, Image: p.image}
	if err := p.store.Create(sess); err != nil {
		return Session{}, err
	}
	sess, err := p.store.Get(sess.ID)
	if err != nil {
		return Session{}, err
	}
	go p.run(ctx, sess, req)
	return sess, nil
}

// run is one session's spawn-to-task. ctx is the parent's lifetime,
// not the request's.
func (p *Parent) run(ctx context.Context, sess Session, req child.TaskRequest) {
	fail := func(why string, err error) {
		reason := why
		if err != nil {
			reason += ": " + err.Error()
		}
		p.log.Warn("parent: session failed", "session", sess.ID, "reason", reason)
		if err := p.store.Settle(sess.ID, StatusFailed, reason, "", "", ""); err != nil {
			p.log.Warn("parent: settle", "session", sess.ID, "err", err)
		}
		line, _ := json.Marshal(child.Settled{Type: child.TypeSettled, Status: StatusFailed, Reason: reason})
		_, _ = p.store.Append(sess.ID, child.TypeSettled, "", line)
	}
	window := time.Duration(p.sp.Window) * time.Second
	if window <= 0 {
		window = time.Duration(spawn.DefaultWindow) * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	pr, err := p.sp.Spawn(sctx, spawn.Spec{Image: p.image, Outfit: p.outfit})
	if err != nil {
		fail("#spawn", err)
		return
	}
	p.log.Info("parent: #spawn accepted", "session", sess.ID)
	b, err := pr.Wait(sctx)
	if err != nil {
		fail("birth", err)
		return
	}
	if err := p.store.Started(sess.ID, b.ID, b.Lease.ID); err != nil {
		fail("record birth", err)
		return
	}
	p.log.Info("parent: born", "session", sess.ID, "child", b.ID, "lease", b.Lease.ID)
	// Snapshot now: a restart between here and the next beat must not
	// forget a child whose lease is live.
	_ = p.store.PutChildren(p.sp.Children())
	if cs, _ := p.a.Authority(); cs != nil {
		_ = p.store.PutConsents(cs, p.a.Now())
	}
	// P→C on the child's own consent: the promise resolved at #birth,
	// before Born installed that consent on the child — retry briefly.
	if err := p.ping(ctx, b.ID); err != nil {
		fail("#ping", err)
		return
	}
	payload, _ := json.Marshal(req)
	tctx, tcancel := context.WithTimeout(ctx, 30*time.Second)
	rep, err := p.a.Send(tctx, b.ID, child.FacetTask, payload)
	tcancel()
	if err != nil {
		fail("#task", err)
		return
	}
	var ack struct {
		Status  string `json:"status"`
		Session string `json:"session"`
	}
	if err := json.Unmarshal(rep.Payload, &ack); err != nil || ack.Status != "started" || ack.Session == "" {
		fail("#task ack", fmt.Errorf("%s", rep.Payload))
		return
	}
	if err := p.store.SetTask(sess.ID, ack.Session); err != nil {
		p.log.Warn("parent: task id", "session", sess.ID, "err", err)
	}
	p.log.Info("parent: #task started", "session", sess.ID, "child", b.ID, "task", ack.Session)
}

func (p *Parent) ping(ctx context.Context, id cert.ActorID) error {
	deadline := time.Now().Add(30 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = p.a.Send(pctx, id, child.FacetPing, []byte(`"hi"`))
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return err
}

// Steer forwards a message into a live run and stamps it, with the
// sender's wallet, on the stream. The caller has checked RightSteer.
func (p *Parent) Steer(ctx context.Context, id, from, message string) error {
	sess, err := p.store.Get(id)
	if err != nil {
		return err
	}
	if sess.Status != StatusRunning || sess.Child == "" {
		return fmt.Errorf("parent: session %s is %s", id, sess.Status)
	}
	payload, _ := json.Marshal(child.SteerRequest{Message: message})
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rep, err := p.a.Send(sctx, sess.Child, child.FacetSteer, payload)
	if err != nil {
		return err
	}
	line, _ := json.Marshal(map[string]any{"type": "steer", "message": message, "from": from, "response": json.RawMessage(rep.Payload)})
	_, err = p.store.Append(id, "steer", from, line)
	return err
}

// Abort ends a live run: #abort to the child, stamped on the stream.
// The child settles "aborted" and ships what it has.
func (p *Parent) Abort(ctx context.Context, id, from string) error {
	sess, err := p.store.Get(id)
	if err != nil {
		return err
	}
	if sess.Status != StatusRunning || sess.Child == "" {
		return fmt.Errorf("parent: session %s is %s", id, sess.Status)
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := p.a.Send(sctx, sess.Child, child.FacetAbort, []byte(`{}`)); err != nil {
		return err
	}
	line, _ := json.Marshal(map[string]any{"type": "abort", "from": from})
	_, err = p.store.Append(id, "abort", from, line)
	return err
}

// ---- the kit's outfit -------------------------------------------------------

// outfit is the kit's extra (cmd/spawn's, verbatim): one invoke grant
// on (parent, agent-events) for the kit's lifetime, held as this
// actor's own consent so the one-link chain the child presents on
// connect roots. The #renew beat re-issues and reinstalls it.
func (p *Parent) outfit(c cert.ActorID, now int64) (spawn.Kit, error) {
	ttl := p.sp.KitTTL
	if ttl <= 0 {
		ttl = p.sp.Window
	}
	if ttl <= 0 {
		ttl = spawn.DefaultWindow
	}
	g, err := cert.Sign(cert.Cert{
		Aud: string(c),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{p.a.ID()}, Facet: []string{child.FacetAgentEvents}},
		Iat: now,
		Exp: now + ttl,
	}, p.a.Signer)
	if err != nil {
		return spawn.Kit{}, err
	}
	p.a.EditConsents(func(cs []cert.Cert) []cert.Cert { return append(cs, g) })
	return spawn.Kit{Grants: [][]cert.Cert{{g}}}, nil
}

// ---- the agent-events facet -------------------------------------------------

// Admit decides one agent-events connection from peer by its preamble:
// the chain verified under this actor's consents for invoke on
// agent-events, signed by the QUIC peer. Verified certs feed the clock
// mark either way. nil means admit.
func (p *Parent) Admit(peer cert.ActorID, preamble []byte) error {
	chain, speakAs, err := child.DecodePreamble(preamble)
	if err != nil {
		return fmt.Errorf("malformed preamble: %w", err)
	}
	consents, held := p.a.Authority()
	rcv := cert.Receiver{ID: p.a.ID(), Consents: consents, SpeakAs: held}
	_, verified, err := cert.VerifyChain(rcv, cert.VerbInvoke, chain, speakAs, peer, child.FacetAgentEvents, p.a.Now())
	p.a.Observe(verified)
	if err != nil {
		return fmt.Errorf("not authorized: %w", err)
	}
	return nil
}

// Handler serves the facet over an admitted connection (facethttp):
// `POST /events/<task>?from=N` appends every line to the session the
// child is running and settles it on the child_settled line — N is the
// index of the body's first line in the child's stream, so a resumed
// upload (the child re-dialled after this process restarted) is
// deduplicated against what the store holds; `GET /events/<task>`
// answers {count} for that resume; `PUT /session/<task>` stores the
// shipped JSONL. The peer must be the session's child.
func (p *Parent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events/{task}", func(w http.ResponseWriter, req *http.Request) {
		sess, _, ok := p.sessionFor(w, req)
		if !ok {
			return
		}
		n, err := p.store.ChildLines(sess.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"count": n})
	})
	mux.HandleFunc("POST /events/{task}", func(w http.ResponseWriter, req *http.Request) {
		sess, peer, ok := p.sessionFor(w, req)
		if !ok {
			return
		}
		from, _ := strconv.Atoi(req.URL.Query().Get("from"))
		have, err := p.store.ChildLines(sess.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		skip := have - from // lines the body repeats
		if skip < 0 {
			p.log.Warn("parent: events stream resumes past what is held", "session", sess.ID, "from", from, "have", have)
			skip = 0
		}
		p.log.Info("parent: events stream open", "session", sess.ID, "child", peer, "task", sess.Task, "from", from, "skip", skip)
		r := bufio.NewReader(req.Body)
		n := 0
		for {
			line, err := r.ReadBytes('\n')
			if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
				if skip > 0 {
					skip--
				} else {
					n++
					p.ingest(sess.ID, line)
				}
			}
			if err != nil {
				if err != io.EOF {
					p.log.Warn("parent: events stream", "session", sess.ID, "err", err)
				}
				break
			}
		}
		p.log.Info("parent: events stream closed", "session", sess.ID, "lines", n)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /session/{task}", func(w http.ResponseWriter, req *http.Request) {
		sess, peer, ok := p.sessionFor(w, req)
		if !ok {
			return
		}
		n, err := p.store.PutFile(sess.ID, req.Body)
		if err != nil {
			p.log.Warn("parent: session file", "session", sess.ID, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		p.log.Info("parent: session file stored", "session", sess.ID, "child", peer, "bytes", n)
		w.WriteHeader(http.StatusNoContent)
		// The run is over and shipped: the lease can go now.
		go p.kill(context.Background(), peer, "session shipped")
	})
	return mux
}

// sessionFor resolves the request's session: by the task id if known,
// else the peer's live session (the stream can open before the #task
// ack is read), recording the task id then. 403 on any mismatch.
func (p *Parent) sessionFor(w http.ResponseWriter, req *http.Request) (Session, cert.ActorID, bool) {
	task := req.PathValue("task")
	peer, ok := facethttp.PeerFrom(req.Context())
	if !ok || !ValidID(task) {
		http.Error(w, "not a child", http.StatusForbidden)
		return Session{}, "", false
	}
	sess, err := p.store.ByTask(task)
	if errors.Is(err, ErrNotFound) {
		sess, err = p.store.ByChild(peer, true)
		if err == nil {
			err = p.store.SetTask(sess.ID, task)
			sess.Task = task
		}
	}
	if err != nil || sess.Child != peer {
		http.Error(w, "not this session's child", http.StatusForbidden)
		return Session{}, "", false
	}
	return sess, peer, true
}

// ingest appends one stream line; the child_settled line also settles
// the session with its status and pointer.
func (p *Parent) ingest(id string, line []byte) {
	raw := json.RawMessage(bytes.Clone(line))
	var t struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &t)
	if _, err := p.store.Append(id, t.Type, "", raw); err != nil {
		p.log.Warn("parent: append", "session", id, "err", err)
		return
	}
	if t.Type != child.TypeSettled {
		return
	}
	var st child.Settled
	if err := json.Unmarshal(raw, &st); err != nil {
		p.log.Warn("parent: settled line", "session", id, "err", err)
		return
	}
	status := st.Status
	switch status {
	case StatusSettled, StatusAborted, StatusFailed:
	default:
		status = StatusFailed
	}
	if err := p.store.Settle(id, status, st.Reason, st.Branch, st.Head, st.PR); err != nil {
		p.log.Warn("parent: settle", "session", id, "err", err)
		return
	}
	p.log.Info("parent: session settled", "session", id, "status", status, "events", st.Events, "pr", st.PR)
}
