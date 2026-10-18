package child_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/facethttp"
	"github.com/marnyg/talos-config/protocol/spawn"
)

// The fake pi is this test binary re-executed with CHILD_FAKE_PI=1 —
// the same shape as pirpc's test fake (copied, not imported: test code
// does not cross packages). It honours `--session-dir DIR` by writing
// DIR/session.jsonl on the prompt and naming it in get_state, runs a
// canned turn, and with CHILD_FAKE_HANG=1 stops after message_start
// until an abort (then agent_end + agent_settled) or stdin EOF. On EOF
// it emits one last `fake_eof` record and exits 0, so a stream that
// shows fake_eof before child_settled proves stdin was closed and the
// process left on its own.
func TestMain(m *testing.M) {
	if os.Getenv("CHILD_FAKE_PI") == "1" {
		fakeMain()
		return
	}
	os.Exit(m.Run())
}

const fakeSession = "{\"type\":\"session\",\"id\":\"abc123\"}\n{\"type\":\"message\",\"role\":\"user\",\"content\":\"hi\"}\n"

func fakeMain() {
	out := bufio.NewWriter(os.Stdout)
	emit := func(s string) {
		out.WriteString(s)
		out.WriteByte('\n')
		out.Flush()
	}
	var sdir string
	for i, a := range os.Args {
		if a == "--session-dir" && i+1 < len(os.Args) {
			sdir = os.Args[i+1]
		}
	}
	hang := os.Getenv("CHILD_FAKE_HANG") == "1"
	sfile := ""
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(line) > 0 {
			var cmd struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if jerr := json.Unmarshal(line, &cmd); jerr != nil {
				emit(`{"type":"response","command":"parse","success":false,"error":"bad json"}`)
				continue
			}
			switch cmd.Type {
			case "prompt":
				if sdir != "" {
					sfile = filepath.Join(sdir, "session.jsonl")
					_ = os.WriteFile(sfile, []byte(fakeSession), 0o600)
				}
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"prompt","success":true,"data":{"disposition":"started"}}`)
				emit(`{"type":"agent_start"}`)
				emit(`{"type":"turn_start"}`)
				emit(`{"type":"message_start"}`)
				if hang {
					continue
				}
				emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`)
				// CHILD_FAKE_WRITE: leave a file in cwd (the workspace), as
				// an agent that did work would.
				if os.Getenv("CHILD_FAKE_WRITE") == "1" {
					_ = os.WriteFile("done.txt", []byte(cmd.Message+"\n"), 0o644)
				}
				emit(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hi, done"}]}}`)
				emit(`{"type":"turn_end"}`)
				emit(`{"type":"agent_end","messages":[],"willRetry":false}`)
				emit(`{"type":"agent_settled"}`)
			case "steer":
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"steer","success":true,"data":{"disposition":"queued"}}`)
			case "abort":
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"abort","success":true}`)
				if hang {
					emit(`{"type":"agent_end","messages":[],"willRetry":false}`)
					emit(`{"type":"agent_settled"}`)
				}
			case "get_state":
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"get_state","success":true,"data":{"model":{"id":"fake"},"isStreaming":false,"isCompacting":false,"sessionFile":"` + sfile + `","sessionId":"abc123","sessionName":"","messageCount":0,"pendingMessageCount":0}}`)
			default:
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"` + cmd.Type + `","success":false,"error":"unknown command"}`)
			}
		}
		if err != nil {
			break
		}
	}
	emit(`{"type":"fake_eof"}`)
	os.Exit(0)
}

// fakePi points Options.Task at this binary as pi. Env goes through
// the process environment (pirpc inherits it); GORACE keeps the
// re-exec'd binary from sleeping 1 s at exit under -race.
func fakePi(t *testing.T, hang bool) {
	t.Helper()
	t.Setenv("CHILD_FAKE_PI", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	if hang {
		t.Setenv("CHILD_FAKE_HANG", "1")
	} else {
		t.Setenv("CHILD_FAKE_HANG", "0")
	}
}

// parentFacet is the parent's end of agent-events in-process: a
// facethttp server fed by net.Pipe ends the Dialer hands over, with
// the chain-on-connect preamble verified the way cmd/spawn does it
// (cert.VerifyChain against the parent's authority).
type parentFacet struct {
	r      *rig
	ln     *facethttp.Listener
	srv    *http.Server
	mu     sync.Mutex
	events map[string][]json.RawMessage // task id → lines
	closed map[string]bool              // task id → POST body ended
	sess   map[string][]byte
	dials  int
	verify []error
	peer   cert.ActorID
}

func newParentFacet(r *rig) *parentFacet {
	p := &parentFacet{r: r, events: map[string][]json.RawMessage{}, closed: map[string]bool{}, sess: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := req.PathValue("id")
		if peer, ok := facethttp.PeerFrom(req.Context()); !ok || peer != p.peer {
			http.Error(w, "not the child", http.StatusForbidden)
			return
		}
		sc := bufio.NewReader(req.Body)
		for {
			line, err := sc.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				p.mu.Lock()
				p.events[id] = append(p.events[id], json.RawMessage(bytes.TrimSuffix(line, []byte{'\n'})))
				p.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		p.mu.Lock()
		p.closed[id] = true
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /session/{id}", func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		p.mu.Lock()
		p.sess[req.PathValue("id")] = b
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	p.srv, p.ln = facethttp.Server(child.FacetAgentEvents, mux)
	go func() { _ = p.srv.Serve(p.ln) }()
	r.t.Cleanup(func() { p.srv.Close() })
	return p
}

// dial is the child's Dialer: verify the preamble as the parent
// would, then one pipe end to the server, the other to the child.
func (p *parentFacet) dial(ctx context.Context, parent cert.ActorID, hints []string, preamble []byte) (io.ReadWriteCloser, error) {
	chain, speakAs, err := child.DecodePreamble(preamble)
	if err == nil {
		consents, held := p.r.parent.Authority()
		rcv := cert.Receiver{ID: p.r.parent.ID(), Consents: consents, SpeakAs: held}
		_, _, err = cert.VerifyChain(rcv, cert.VerbInvoke, chain, speakAs, p.peer, child.FacetAgentEvents, p.r.parent.Now())
	}
	p.mu.Lock()
	p.dials++
	p.verify = append(p.verify, err)
	if parent != p.r.parent.ID() || len(hints) == 0 {
		p.verify = append(p.verify, fmt.Errorf("dial(%s, %v)", parent, hints))
	}
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	c1, c2 := net.Pipe()
	go p.ln.Push(ctx, c2, cert.Identity{Key: p.peer}, p.peer)
	return c1, nil
}

func (p *parentFacet) lines(id string) []json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]json.RawMessage(nil), p.events[id]...)
}

func (p *parentFacet) types(id string) []string {
	var out []string
	for _, l := range p.lines(id) {
		var t struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(l, &t)
		out = append(out, t.Type)
	}
	return out
}

func (p *parentFacet) settled(id string) (child.Settled, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ls := p.events[id]
	if !p.closed[id] || len(ls) == 0 {
		return child.Settled{}, false
	}
	var s child.Settled
	if json.Unmarshal(ls[len(ls)-1], &s) != nil || s.Type != child.TypeSettled {
		return child.Settled{}, false
	}
	return s, true
}

func (p *parentFacet) session(id string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.sess[id]
	return b, ok
}

// agentOutfit is what cmd/spawn's Spec.Outfit does: grant the child
// invoke on (parent, agent-events) for the kit's lifetime and hold
// the same cert as a consent so the one-link chain roots.
func agentOutfit(r *rig) func(cert.ActorID, int64) (spawn.Kit, error) {
	return func(c cert.ActorID, now int64) (spawn.Kit, error) {
		g, err := cert.Sign(cert.Cert{
			Aud: string(c),
			Can: cert.VerbInvoke,
			Cav: cert.Caveats{Target: []cert.ActorID{r.parent.ID()}, Facet: []string{child.FacetAgentEvents}},
			Iat: now,
			Exp: now + r.sp.KitTTL,
		}, r.parent.Signer)
		if err != nil {
			return spawn.Kit{}, err
		}
		r.parent.EditConsents(func(cs []cert.Cert) []cert.Cert { return append(cs, g) })
		return spawn.Kit{Grants: [][]cert.Cert{{g}}}, nil
	}
}

// agentRig: setup with the task options pointed at the fake pi and the
// in-process parent facet; the child's id is learned at birth.
func agentRig(t *testing.T, hang bool) (*rig, *parentFacet) {
	t.Helper()
	fakePi(t, hang)
	r := setup(t, child.Options{Beat: 50 * time.Millisecond})
	p := newParentFacet(r)
	r.drv.opts.Task = child.TaskOptions{PiBin: os.Args[0], Dial: p.dial, KillAfter: 2 * time.Second}
	return r, p
}

func (r *rig) pingUntilUp(t *testing.T, id cert.ActorID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	waitFor(t, "ping", func() bool {
		_, err := r.parent.Send(ctx, id, child.FacetPing, []byte(`"hi"`))
		return err == nil
	})
}

func (r *rig) task(t *testing.T, id cert.ActorID, prompt string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	rep, err := r.parent.Send(ctx, id, child.FacetTask, []byte(`{"prompt":`+fmt.Sprintf("%q", prompt)+`}`))
	if err != nil {
		return "", err
	}
	var ack struct{ Status, Session string }
	if err := json.Unmarshal(rep.Payload, &ack); err != nil {
		t.Fatalf("#task reply %q: %v", rep.Payload, err)
	}
	if ack.Status != "started" || ack.Session == "" {
		t.Fatalf("#task reply %q", rep.Payload)
	}
	return ack.Session, nil
}

// TestTaskStreamsEventsAndSession: #task → the fake's records land
// at the parent in order, child_settled last with the count, then the
// session JSONL arrives as the PUT body; the preamble verified under
// the outfit's consent; and the slot is free for a second task.
func TestTaskStreamsEventsAndSession(t *testing.T) {
	r, p := agentRig(t, false)
	b := r.spawn(t, spawn.Spec{Image: image, Outfit: agentOutfit(r)})
	p.peer = b.ID
	r.pingUntilUp(t, b.ID)

	id, err := r.task(t, b.ID, "say hi")
	if err != nil {
		t.Fatalf("#task: %v", err)
	}
	var s child.Settled
	waitFor(t, "child_settled", func() bool {
		var ok bool
		s, ok = p.settled(id)
		return ok
	})
	got := strings.Join(p.types(id), " ")
	want := "response agent_start turn_start message_start message_update message_end turn_end agent_end agent_settled response fake_eof child_settled"
	if got != want {
		t.Fatalf("stream:\n got %s\nwant %s", got, want)
	}
	if s.Status != "settled" || s.Session != id || s.Events != len(p.lines(id))-1 {
		t.Fatalf("settled %+v", s)
	}
	var sess []byte
	waitFor(t, "session PUT", func() bool {
		var ok bool
		sess, ok = p.session(id)
		return ok
	})
	if string(sess) != fakeSession {
		t.Fatalf("session body %q", sess)
	}
	p.mu.Lock()
	dials, verify := p.dials, p.verify
	p.mu.Unlock()
	if dials != 1 {
		t.Fatalf("%d dials, want 1 (POST and PUT share the connection)", dials)
	}
	for _, err := range verify {
		if err != nil {
			t.Fatalf("preamble: %v", err)
		}
	}

	// The slot is free again (once the uploader has returned, which is
	// after the PUT the parent just saw: retry briefly).
	var id2 string
	waitFor(t, "second #task after settle", func() bool {
		var err error
		id2, err = r.task(t, b.ID, "again")
		return err == nil
	})
	waitFor(t, "second child_settled", func() bool { _, ok := p.settled(id2); return ok })
}

// TestTaskWithoutOutfitRefused: no chain at (parent, agent-events) ⇒
// #task is refused with a clear reason, nothing dialled.
func TestTaskWithoutOutfitRefused(t *testing.T) {
	r, p := agentRig(t, false)
	b := r.spawn(t, spawn.Spec{Image: image})
	p.peer = b.ID
	r.pingUntilUp(t, b.ID)
	_, err := r.task(t, b.ID, "say hi")
	if err == nil || !strings.Contains(err.Error(), "outfit") {
		t.Fatalf("#task without outfit: %v", err)
	}
	if p.dials != 0 {
		t.Fatal("dialled without a chain")
	}
}

// TestTaskRepoResultIsBranchAndPR (e6pu.3): #task with a repo clones
// it from the forge, pi runs in the clone, and after settle the
// changes are on agent/<id> at the origin with a PR whose body is
// pi's final message; child_settled carries the pointer. The forge is
// a bare repo (clone base) plus an httptest API.
func TestTaskRepoResultIsBranchAndPR(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r, p := agentRig(t, false)
	t.Setenv("CHILD_FAKE_WRITE", "1")

	// origin: bare agents/scratch.git with one commit on main.
	forge := t.TempDir()
	bare := filepath.Join(forge, "agents", "scratch.git")
	gitRun(t, "", "init", "-q", "--bare", "-b", "main", bare)
	seed := t.TempDir()
	gitRun(t, "", "clone", "-q", bare, seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", "README")
	gitRun(t, seed, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	gitRun(t, seed, "push", "-q", "origin", "HEAD:main")

	// the API: one POST /pulls → 201, the request kept for the checks.
	var prMu sync.Mutex
	var prReq map[string]string
	var prAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/api/v1/repos/agents/scratch/pulls" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		prMu.Lock()
		defer prMu.Unlock()
		prAuth = req.Header.Get("Authorization")
		_ = json.NewDecoder(req.Body).Decode(&prReq)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"html_url":"http://forge/agents/scratch/pulls/1"}`)
	}))
	defer api.Close()

	ws := t.TempDir()
	r.drv.opts.Task.Dir = ws
	r.drv.opts.Task.Forge = child.Forge{URL: api.URL, Token: "tok", CloneBase: forge}
	b := r.spawn(t, spawn.Spec{Image: image, Outfit: agentOutfit(r)})
	p.peer = b.ID
	r.pingUntilUp(t, b.ID)

	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	rep, err := r.parent.Send(ctx, b.ID, child.FacetTask, []byte(`{"prompt":"write done.txt\nsecond line","repo":"agents/scratch","ref":"main","title":"hello file"}`))
	if err != nil {
		t.Fatalf("#task: %v", err)
	}
	var ack struct{ Session string }
	_ = json.Unmarshal(rep.Payload, &ack)
	var s child.Settled
	waitFor(t, "child_settled", func() bool {
		var ok bool
		s, ok = p.settled(ack.Session)
		return ok
	})
	if s.Status != "settled" || s.Reason != "" {
		t.Fatalf("settled %+v", s)
	}
	if s.Repo != "agents/scratch" || s.Branch != "agent/"+ack.Session || s.Head == "" || s.PR != "http://forge/agents/scratch/pulls/1" {
		t.Fatalf("pointer %+v", s.Pointer)
	}
	// The branch is at the origin with the file, committed as the bot.
	if got := gitRun(t, "", "--git-dir", bare, "show", s.Branch+":done.txt"); !strings.Contains(got, "write done.txt") {
		t.Fatalf("done.txt on %s: %q", s.Branch, got)
	}
	if head := strings.TrimSpace(gitRun(t, "", "--git-dir", bare, "rev-parse", s.Branch)); head != s.Head {
		t.Fatalf("origin %s = %s, pointer head %s", s.Branch, head, s.Head)
	}
	if log := gitRun(t, "", "--git-dir", bare, "log", "-1", "--format=%s%n%b%n%an", s.Branch); !strings.Contains(log, "agent: "+ack.Session) || !strings.Contains(log, "agent: hello file") || !strings.HasSuffix(strings.TrimSpace(log), "\nagent") {
		t.Fatalf("commit on %s:\n%s", s.Branch, log)
	}
	// The PR: title from the prompt, base from the clone, body = final message + trailer.
	prMu.Lock()
	defer prMu.Unlock()
	if prAuth != "token tok" {
		t.Fatalf("PR auth %q", prAuth)
	}
	if prReq["title"] != "agent: hello file" || prReq["head"] != s.Branch || prReq["base"] != "main" {
		t.Fatalf("PR request %v", prReq)
	}
	if !strings.HasPrefix(prReq["body"], "hi, done\n\n---\nTask "+ack.Session+", settled, ") {
		t.Fatalf("PR body %q", prReq["body"])
	}
	// pi ran in the clone.
	if _, err := os.Stat(filepath.Join(ws, "scratch", "done.txt")); err != nil {
		t.Fatalf("workspace: %v", err)
	}
}

// TestTaskRepoWithoutForgeRefused: a #task naming a repo on a child
// with no forge is refused at the ack, nothing dialled.
func TestTaskRepoWithoutForgeRefused(t *testing.T) {
	r, p := agentRig(t, false)
	b := r.spawn(t, spawn.Spec{Image: image, Outfit: agentOutfit(r)})
	p.peer = b.ID
	r.pingUntilUp(t, b.ID)
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	_, err := r.parent.Send(ctx, b.ID, child.FacetTask, []byte(`{"prompt":"x","repo":"agents/scratch"}`))
	if err == nil || !strings.Contains(err.Error(), "forge") {
		t.Fatalf("#task repo without forge: %v", err)
	}
	if p.dials != 0 {
		t.Fatal("dialled")
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestTaskSecondRefusedThenAbort: one task per child — a second #task
// while one runs is refused; #abort ends the run, the stream closes
// with status aborted after pi left on its own (fake_eof).
func TestTaskSecondRefusedThenAbort(t *testing.T) {
	r, p := agentRig(t, true)
	b := r.spawn(t, spawn.Spec{Image: image, Outfit: agentOutfit(r)})
	p.peer = b.ID
	r.pingUntilUp(t, b.ID)

	id, err := r.task(t, b.ID, "hang")
	if err != nil {
		t.Fatalf("#task: %v", err)
	}
	waitFor(t, "message_start", func() bool { return len(p.lines(id)) >= 4 })
	if _, err := r.task(t, b.ID, "again"); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("second #task while running: %v", err)
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	rep, err := r.parent.Send(ctx, b.ID, child.FacetSteer, []byte(`{"message":"faster"}`))
	if err != nil || !strings.Contains(string(rep.Payload), "queued") {
		t.Fatalf("#steer: %v %+v", err, rep)
	}
	rep, err = r.parent.Send(ctx, b.ID, child.FacetAbort, []byte(`{}`))
	if err != nil || !strings.Contains(string(rep.Payload), "aborting") {
		t.Fatalf("#abort: %v %+v", err, rep)
	}
	var s child.Settled
	waitFor(t, "child_settled", func() bool {
		var ok bool
		s, ok = p.settled(id)
		return ok
	})
	if s.Status != "aborted" {
		t.Fatalf("settled %+v", s)
	}
	types := p.types(id)
	if types[len(types)-2] != "fake_eof" {
		t.Fatalf("pi did not leave on stdin close: %v", types)
	}
	waitFor(t, "session PUT after abort", func() bool { _, ok := p.session(id); return ok })
	if _, err := r.parent.Send(ctx, b.ID, child.FacetAbort, []byte(`{}`)); err == nil {
		t.Fatal("#abort with no task running succeeded")
	}
}

// TestTaskDeadlineEndsUpload: the child's context ending (SIGTERM,
// the platform) closes pi's stdin and the POST body cleanly — the
// parent still gets fake_eof and child_settled — and Run returns only
// once that is done.
func TestTaskDeadlineEndsUpload(t *testing.T) {
	r, p := agentRig(t, true)
	rctx, rcancel := context.WithCancel(r.ctx)
	defer rcancel()
	r.drv.runCtx = rctx
	b := r.spawn(t, spawn.Spec{Image: image, Outfit: agentOutfit(r)})
	p.peer = b.ID
	r.pingUntilUp(t, b.ID)

	id, err := r.task(t, b.ID, "hang")
	if err != nil {
		t.Fatalf("#task: %v", err)
	}
	waitFor(t, "message_start", func() bool { return len(p.lines(id)) >= 4 })
	rcancel()
	select {
	case err := <-r.drv.ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	s, ok := p.settled(id)
	if !ok {
		t.Fatalf("Run returned before the stream closed: %v", p.types(id))
	}
	if s.Status != "aborted" || s.Reason != "deadline" {
		t.Fatalf("settled %+v", s)
	}
	types := p.types(id)
	if types[len(types)-2] != "fake_eof" {
		t.Fatalf("pi did not leave on stdin close: %v", types)
	}
}
