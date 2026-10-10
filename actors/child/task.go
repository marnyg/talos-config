package child

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marnyg/talos-config/actors/pirpc"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/facethttp"
)

// The agent facets (ADR-0037, talos-config-e6pu.2): a child whose
// Options.Task names a pi binary serves these three beside FacetPing
// and consents the parent to them at Born. One task per child (v0).
const (
	// FacetTask starts a run: `{"prompt": "…"}` (`repo`/`ref` are
	// accepted and ignored for now). The reply is an ack,
	// `{"status":"started","session":"<task-id>"}`; the outcome is the
	// LAST line on the event stream (see FacetAgentEvents), not the
	// reply — handlers run inline in the mailbox loop, so a #task that
	// waited for the run would stall #renew, #steer and #abort for its
	// whole length. Refused while a task is running.
	FacetTask = "#task"
	// FacetSteer queues `{"message": "…"}` into the live run (pi's
	// `steer`); the reply is pi's response data.
	FacetSteer = "#steer"
	// FacetAbort ends the live run (`{}`): pi's `abort` goes down, the
	// run's context ends, the stream closes with status "aborted".
	FacetAbort = "#abort"
	// FacetAgentEvents is the stream facet (iroh ALPN) the PARENT
	// serves and the child dials once per task, chain-on-connect: the
	// preamble carries the chain the Kit's Outfit granted at
	// (parent, FacetAgentEvents). Over the admitted connection the
	// child speaks HTTP: `POST /events/<task-id>` (application/x-ndjson,
	// chunked, one pirpc record per line, `child_settled` last) and
	// `PUT /session/<task-id>` (the session JSONL) once pi settled.
	FacetAgentEvents = "agent-events"
)

// Dialer opens one stream to parent's FacetAgentEvents facet with
// preamble as the chain-on-connect bundle (Preamble on the wire) and
// returns it admitted. hints are the parent's current endpoints.
// Transport-free: cmd/child binds it to irohtransport's DialConn +
// Open; tests hand over one end of a net.Pipe.
type Dialer func(ctx context.Context, parent cert.ActorID, hints []string, preamble []byte) (io.ReadWriteCloser, error)

// TaskOptions is the agent half of Options: set PiBin to serve the
// task facets.
type TaskOptions struct {
	// PiBin is the pi binary; "" ⇒ the child serves FacetPing only.
	PiBin string
	// PiArgs follow `--mode rpc --session-dir DIR` on pi's command line.
	PiArgs []string
	// Dir is pi's working directory (the workspace); "" ⇒ the process's.
	Dir string
	// Dial reaches the parent's FacetAgentEvents facet. Required when
	// PiBin is set.
	Dial Dialer
	// KillAfter bounds pi's shutdown after its stdin is closed; 0 ⇒
	// pirpc's default (10 s).
	KillAfter time.Duration
}

// Preamble is the chain-on-connect bundle the child sends on the
// agent-events facet: the chain to (parent, FacetAgentEvents) and the
// child's held speak-as certs (empty today — a child's key is its
// own, there is no principal it is hot for; sent regardless so the
// receiver's VerifyChain call is the general one).
type Preamble struct {
	Chain   []json.RawMessage `json:"chain"`
	SpeakAs []json.RawMessage `json:"speak_as"`
}

// EncodePreamble builds the wire bundle from decoded certs.
func EncodePreamble(chain, speakAs []cert.Cert) ([]byte, error) {
	enc := func(cs []cert.Cert) ([]json.RawMessage, error) {
		out := make([]json.RawMessage, 0, len(cs))
		for _, c := range cs {
			raw, err := cert.Encode(c)
			if err != nil {
				return nil, err
			}
			out = append(out, raw)
		}
		return out, nil
	}
	c, err := enc(chain)
	if err != nil {
		return nil, err
	}
	s, err := enc(speakAs)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Preamble{Chain: c, SpeakAs: s})
}

// DecodePreamble parses the wire bundle strictly (each cert through
// cert.DecodeCert). The receiver feeds the result to cert.VerifyChain.
func DecodePreamble(raw []byte) (chain, speakAs []cert.Cert, err error) {
	var p Preamble
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, nil, fmt.Errorf("preamble: %w", err)
	}
	dec1 := func(rs []json.RawMessage) ([]cert.Cert, error) {
		out := make([]cert.Cert, 0, len(rs))
		for _, r := range rs {
			c, err := cert.DecodeCert(r)
			if err != nil {
				return nil, fmt.Errorf("preamble: %w", err)
			}
			out = append(out, c)
		}
		return out, nil
	}
	if chain, err = dec1(p.Chain); err != nil {
		return nil, nil, err
	}
	if speakAs, err = dec1(p.SpeakAs); err != nil {
		return nil, nil, err
	}
	return chain, speakAs, nil
}

// Settled is the last line on a task's event stream: the run's outcome.
// Status is "settled" (pi reached agent_settled), "aborted" (#abort or
// the deadline — ctx end, lapse — ended it first) or "failed" (pi could
// not be started or prompted, or exited mid-run); Events counts the
// pirpc records forwarded before this line; Reason is free text on
// the non-settled statuses.
type Settled struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Events  int    `json:"events"`
	Session string `json:"session"`
	Reason  string `json:"reason,omitempty"`
}

// TypeSettled is Settled.Type.
const TypeSettled = "child_settled"

// settleGrace bounds how long Run waits, after its own end, for a live
// task to close pi (KillAfter at most) and finish its upload.
const settleGrace = 25 * time.Second

// stateTimeout bounds the get_state after settle.
const stateTimeout = 3 * time.Second

// TaskRequest is #task's payload.
type TaskRequest struct {
	Prompt string `json:"prompt"`
	Repo   string `json:"repo,omitempty"`
	Ref    string `json:"ref,omitempty"`
}

// SteerRequest is #steer's payload.
type SteerRequest struct {
	Message string `json:"message"`
}

// agent is the task side of a child: the handlers' shared state. The
// handlers run in the mailbox loop, Run owns a.Grants — so Run copies
// the chain in (setChain) and the handlers only read the copy.
type agent struct {
	a      *actor.Actor
	parent cert.ActorID
	o      TaskOptions
	log    *slog.Logger
	base   context.Context // every run's parent; shutdown cancels it
	stop   context.CancelFunc

	mu    sync.Mutex
	chain []cert.Cert
	hints []string
	cur   *run
}

// run is one task.
type run struct {
	id      string
	cancel  context.CancelFunc
	done    chan struct{}
	aborted atomic.Bool
	pmu     sync.Mutex
	pi      *pirpc.Client
}

func newAgent(ctx context.Context, a *actor.Actor, parent cert.ActorID, o TaskOptions, log *slog.Logger) *agent {
	base, stop := context.WithCancel(ctx)
	return &agent{a: a, parent: parent, o: o, log: log, base: base, stop: stop}
}

func (ag *agent) install() {
	ag.a.AcceptTable[FacetTask] = ag.task
	ag.a.AcceptTable[FacetSteer] = ag.steer
	ag.a.AcceptTable[FacetAbort] = ag.abort
}

// setChain publishes the chain held at (parent, FacetAgentEvents) and
// the parent's current endpoints for the handlers. Called by Run after
// Born and after each renewal.
func (ag *agent) setChain(chain []cert.Cert, hints []string) {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	ag.chain = append([]cert.Cert(nil), chain...)
	ag.hints = append([]string(nil), hints...)
}

// shutdown ends a live run and waits (settleGrace at most) for its
// upload to finish, so a SIGTERM or lapse still delivers the stream's
// last line and the session.
func (ag *agent) shutdown() {
	ag.stop()
	ag.mu.Lock()
	r := ag.cur
	ag.mu.Unlock()
	if r == nil {
		return
	}
	select {
	case <-r.done:
	case <-time.After(settleGrace):
		ag.log.Warn("child: task did not settle in time", "task", r.id)
	}
}

func (r *run) client() *pirpc.Client {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	return r.pi
}

// task serves FacetTask: validate, take the one slot, reply started.
func (ag *agent) task(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	var req TaskRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("child: #task: payload: %w", err)
	}
	if req.Prompt == "" {
		return nil, errors.New("child: #task: empty prompt")
	}
	ag.mu.Lock()
	defer ag.mu.Unlock()
	if ag.cur != nil {
		return nil, fmt.Errorf("child: #task: task %s is running (one task per child)", ag.cur.id)
	}
	if len(ag.chain) == 0 {
		return nil, fmt.Errorf("child: #task: no chain to (%s, %s): the kit's outfit did not grant it", ag.parent, FacetAgentEvents)
	}
	_, speakAs := ag.a.Authority()
	pre, err := EncodePreamble(ag.chain, speakAs)
	if err != nil {
		return nil, fmt.Errorf("child: #task: preamble: %w", err)
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	rctx, cancel := context.WithCancel(ag.base)
	r := &run{id: hex.EncodeToString(b[:]), cancel: cancel, done: make(chan struct{})}
	ag.cur = r
	hints := ag.hints
	go ag.execute(rctx, r, req.Prompt, pre, hints)
	return json.Marshal(map[string]string{"status": "started", "session": r.id})
}

// steer serves FacetSteer: pi's steer, inline (its response is
// immediate: queued or handled), bounded.
func (ag *agent) steer(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
	var req SteerRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("child: #steer: payload: %w", err)
	}
	pi, err := ag.live("#steer")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := pi.Steer(ctx, req.Message)
	if err != nil {
		return nil, fmt.Errorf("child: #steer: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("child: #steer: pi: %s", resp.Error)
	}
	return json.Marshal(map[string]string{"status": resp.Disposition()})
}

// abort serves FacetAbort: mark the run aborted, send pi's abort off
// the loop (it answers once the session is idle, which may take a
// tool call's time), then end the run's context — closing stdin,
// which is the deadline either way. Replies at once.
func (ag *agent) abort(_ context.Context, _ *actor.Invocation) ([]byte, error) {
	ag.mu.Lock()
	r := ag.cur
	ag.mu.Unlock()
	if r == nil {
		return nil, errors.New("child: #abort: no task is running")
	}
	r.aborted.Store(true)
	go func() {
		if pi := r.client(); pi != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := pi.Abort(ctx); err != nil {
				ag.log.Warn("child: #abort: pi", "task", r.id, "err", err)
			}
		}
		r.cancel()
	}()
	return json.Marshal(map[string]string{"status": "aborting", "session": r.id})
}

func (ag *agent) live(facet string) (*pirpc.Client, error) {
	ag.mu.Lock()
	r := ag.cur
	ag.mu.Unlock()
	if r == nil {
		return nil, fmt.Errorf("child: %s: no task is running", facet)
	}
	pi := r.client()
	if pi == nil {
		return nil, fmt.Errorf("child: %s: pi is not up yet", facet)
	}
	return pi, nil
}

// execute is one task's life, on its own goroutine: dial the parent's
// facet, start pi, prompt, forward every record up as the POST body,
// wait for settle/abort/deadline, read the session, close pi, write
// the last line, PUT the session, free the slot.
func (ag *agent) execute(rctx context.Context, r *run, prompt string, preamble []byte, hints []string) {
	log := ag.log.With("task", r.id)
	defer func() {
		ag.mu.Lock()
		ag.cur = nil
		ag.mu.Unlock()
		r.cancel()
		close(r.done)
	}()

	up, err := ag.uploader(rctx, r, preamble, hints)
	if err != nil {
		log.Error("child: task: dial parent", "err", err)
		return
	}
	defer up.close()

	settled := Settled{Type: TypeSettled, Status: "failed", Session: r.id}
	var session []byte

	sdir, err := os.MkdirTemp("", "sap-session-")
	if err != nil {
		settled.Reason = err.Error()
		up.finish(settled, nil, log)
		return
	}
	defer os.RemoveAll(sdir)

	// pi's lifetime is ours to end (pctx), not the run's: the deadline
	// closes stdin only after the session file is read.
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()
	pi, err := pirpc.Start(pctx, ag.o.PiBin, pirpc.Options{
		Dir:       ag.o.Dir,
		Args:      append([]string{"--session-dir", sdir}, ag.o.PiArgs...),
		Log:       log,
		KillAfter: ag.o.KillAfter,
	})
	if err != nil {
		settled.Reason = err.Error()
		up.finish(settled, nil, log)
		return
	}
	r.pmu.Lock()
	r.pi = pi
	r.pmu.Unlock()

	// Forward every record, in order, until stdout ends. Drains Events
	// even once the upload is gone (pi stalls on a full buffer).
	agentSettled := make(chan struct{})
	var once sync.Once
	var n atomic.Int64
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for rec := range pi.Events() {
			n.Add(1)
			up.line(rec.Raw, log)
			if rec.Type == "agent_settled" {
				once.Do(func() { close(agentSettled) })
			}
		}
	}()

	resp, err := pi.Prompt(pctx, prompt)
	switch {
	case err != nil:
		settled.Reason = "prompt: " + err.Error()
	case !resp.Success:
		settled.Reason = "prompt: " + resp.Error
	case resp.Disposition() == "handled":
		settled.Status = "settled"
	default:
		select {
		case <-agentSettled:
			settled.Status = "settled"
		case <-rctx.Done():
			settled.Status, settled.Reason = "aborted", "deadline"
		case <-pi.Done():
			settled.Reason = "pi exited mid-run"
		}
	}
	if r.aborted.Load() {
		settled.Status, settled.Reason = "aborted", "#abort"
	}

	// The session file, while stdin is still open.
	sctx, scancel := context.WithTimeout(context.Background(), stateTimeout)
	st, err := pi.State(sctx)
	scancel()
	switch {
	case err != nil:
		log.Warn("child: task: get_state", "err", err)
	case st.SessionFile == "":
		log.Warn("child: task: pi reports no session file")
	default:
		if session, err = os.ReadFile(st.SessionFile); err != nil {
			log.Warn("child: task: session file", "path", st.SessionFile, "err", err)
			session = nil
		}
	}

	if err := pi.Close(); err != nil {
		log.Warn("child: task: pi exit", "err", err)
	}
	<-forwarded
	settled.Events = int(n.Load())
	up.finish(settled, session, log)
	log.Info("child: task done", "status", settled.Status, "events", settled.Events, "session", len(session))
}

// uploader is the child's end of the agent-events facet for one task:
// one http.Transport over the Dialer (one connection: the first dial
// is made up front so a parent that cannot be reached fails the task
// before pi starts), one long POST whose body is a pipe fed a line per
// record, then one PUT with the session on the same connection.
type uploader struct {
	id       string
	tr       *http.Transport
	pw       *io.PipeWriter
	postDone chan error
	upOK     atomic.Bool
}

func (ag *agent) uploader(ctx context.Context, r *run, preamble []byte, hints []string) (*uploader, error) {
	first, err := ag.o.Dial(ctx, ag.parent, hints, preamble)
	if err != nil {
		return nil, err
	}
	var fmu sync.Mutex
	me, parent := string(ag.a.ID()), ag.parent
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			fmu.Lock()
			c := first
			first = nil
			fmu.Unlock()
			if c == nil {
				var err error
				if c, err = ag.o.Dial(ctx, parent, hints, preamble); err != nil {
					return nil, err
				}
			}
			return facethttp.Dialed(c, me, parent), nil
		},
		DisableKeepAlives: false,
		MaxConnsPerHost:   1,
	}
	pr, pw := io.Pipe()
	up := &uploader{id: r.id, tr: tr, pw: pw, postDone: make(chan error, 1)}
	up.upOK.Store(true)
	// The request's context is NOT the run's: the deadline ends the
	// body cleanly through pw.Close, never by tearing the request down.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, up.url("events"), pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	go func() {
		resp, err := tr.RoundTrip(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				err = fmt.Errorf("POST /events/%s: %s", r.id, resp.Status)
			}
		}
		if err != nil {
			up.upOK.Store(false)
			pr.CloseWithError(err) // unblocks a writer
		}
		up.postDone <- err
	}()
	return up, nil
}

func (up *uploader) url(kind string) string {
	return "http://" + FacetAgentEvents + "/" + kind + "/" + up.id
}

// line writes one record (plus LF); a dead upload is logged once and
// then ignored so pi keeps draining.
func (up *uploader) line(raw []byte, log *slog.Logger) {
	if !up.upOK.Load() {
		return
	}
	buf := make([]byte, 0, len(raw)+1)
	buf = append(buf, raw...)
	buf = append(buf, '\n')
	if _, err := up.pw.Write(buf); err != nil {
		if up.upOK.CompareAndSwap(true, false) {
			log.Warn("child: task: event upload ended early", "err", err)
		}
	}
}

// finish writes the last line, ends the POST, then PUTs the session.
func (up *uploader) finish(s Settled, session []byte, log *slog.Logger) {
	if raw, err := json.Marshal(s); err == nil {
		up.line(raw, log)
	}
	up.pw.Close()
	if err := <-up.postDone; err != nil {
		log.Warn("child: task: events POST", "err", err)
		return
	}
	if session == nil {
		return
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, up.url("session"), bytes.NewReader(session))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := up.tr.RoundTrip(req)
	if err != nil {
		log.Warn("child: task: session PUT", "err", err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		log.Warn("child: task: session PUT", "status", resp.Status)
	}
}

func (up *uploader) close() { up.tr.CloseIdleConnections() }
