package parent_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/actors/parent"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/facethttp"
	"github.com/marnyg/talos-config/protocol/postage"
	"github.com/marnyg/talos-config/protocol/provisioner"
	"github.com/marnyg/talos-config/protocol/spawn"
)

const (
	t0    int64 = 1_800_000_000
	image       = "ghcr.io/x/agent@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	owner       = "0xf56863bf40a75dc3c632c17fc65aa2eb06a89406"
)

// The fake pi: this binary re-executed with PARENT_FAKE_PI=1 — the
// shape of actors/child's test fake (copied, not imported). Canned
// turn on prompt; with PARENT_FAKE_HANG=1 it stops after message_start
// until an abort or stdin EOF.
func TestMain(m *testing.M) {
	if os.Getenv("PARENT_FAKE_PI") == "1" {
		fakeMain()
		return
	}
	os.Exit(m.Run())
}

const fakeSession = "{\"type\":\"session\",\"id\":\"abc123\"}\n"

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
	hang := os.Getenv("PARENT_FAKE_HANG") == "1"
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
				emit(`{"type":"message_end","message":{"role":"user","content":` + jsonString(cmd.Message) + `}}`)
				emit(`{"type":"message_start"}`)
				if hang {
					continue
				}
				emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`)
				emit(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":` + jsonString(fakeAnswer(cmd.Message)) + `}]}}`)
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

// fakeAnswer is the fake's last word: "the zebra crossed", or — when
// the prompt carries a `fake:to=<state>` or `fake:children=<state>`
// marker (a test flow's persona) — that plus a transition block to the
// state, the latter with two children.
func fakeAnswer(prompt string) string {
	const zebra = "the zebra crossed"
	if i := strings.Index(prompt, "fake:to="); i >= 0 {
		to := strings.Fields(prompt[i+len("fake:to="):])[0]
		return zebra + "\n```transition\n{\"to\":\"" + to + "\",\"note\":\"fake moved on\",\"artifact\":{\"ok\":true},\"set\":{\"why\":\"because the fake said so\"}}\n```"
	}
	if i := strings.Index(prompt, "fake:children="); i >= 0 {
		to := strings.Fields(prompt[i+len("fake:children="):])[0]
		return zebra + "\n```transition\n{\"to\":\"" + to + "\",\"note\":\"split\",\"children\":[{\"title\":\"part a\"},{\"title\":\"part b\"}],\"followups\":[{\"title\":\"later\"}]}\n```"
	}
	return zebra
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// rig: the parent (actor + spawner + store + parent.Parent), a
// provisioner whose driver runs child.Run in-process against the fake
// pi, all on one memory network under one settable clock.
type rig struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc
	net    *actor.MemoryNetwork
	clock  atomic.Int64
	dir    string

	signer  cert.EdSigner
	pa      *actor.Actor
	sp      *spawn.Spawner
	store   *parent.Store
	p       *parent.Parent
	ln      *facethttp.Listener
	srv     *http.Server
	pctx    context.Context
	pstop   context.CancelFunc
	retries int

	prov *actor.Actor
	drv  *driver
}

type driver struct {
	r     *rig
	mu    sync.Mutex
	exts  []int64
	kills int
	ended chan error
	child chan *actor.Actor
}

func (d *driver) Start(_ context.Context, spec provisioner.StartSpec) (provisioner.Handle, error) {
	in, err := spawn.DecodeIntro(spec.Params)
	if err != nil {
		return "", err
	}
	a := d.r.actor("child-" + spec.Lease[:4])
	go func() { _ = a.Listen(d.r.ctx) }()
	opts := child.Options{Beat: 50 * time.Millisecond, Task: child.TaskOptions{PiBin: os.Args[0], Dial: d.r.dial, KillAfter: 2 * time.Second}}
	select {
	case d.child <- a:
	default:
	}
	go func() { d.ended <- child.Run(d.r.ctx, a, in, opts) }()
	return provisioner.Handle("ctr-" + spec.Lease), nil
}

func (d *driver) Extend(_ context.Context, _ provisioner.Handle, until int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exts = append(d.exts, until)
	return nil
}

func (d *driver) Kill(context.Context, provisioner.Handle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kills++
	return nil
}

func (d *driver) List(context.Context) ([]provisioner.Running, error) { return nil, nil }

func (d *driver) seen() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.exts), d.kills
}

func setup(t *testing.T, hang bool) *rig { return setupWith(t, hang, 0) }

// setupWith is setup with the parent's Retries.
func setupWith(t *testing.T, hang bool, retries int) *rig {
	t.Helper()
	t.Setenv("PARENT_FAKE_PI", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	if hang {
		t.Setenv("PARENT_FAKE_HANG", "1")
	} else {
		t.Setenv("PARENT_FAKE_HANG", "0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, ctx: ctx, cancel: cancel, net: actor.NewMemoryNetwork(), dir: t.TempDir(), retries: retries}
	t.Cleanup(cancel)
	r.clock.Store(t0)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r.signer = cert.NewEdSigner(priv)

	r.prov = r.actor("prov")
	r.drv = &driver{r: r, ended: make(chan error, 8), child: make(chan *actor.Actor, 8)}
	r.store, err = parent.Open(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.store.Close() })
	r.up(t)
	provisioner.New(r.prov, r.drv)
	go func() { _ = r.prov.Listen(r.ctx) }()
	if _, err := r.prov.PublishLocation(3600); err != nil {
		t.Fatal(err)
	}
	r.consentProvisioner(t)
	return r
}

// up brings a parent process up on the rig's key: actor, spawner,
// parent.Parent, the agent-events server. Called by setup and again by
// restart.
func (r *rig) up(t *testing.T) {
	t.Helper()
	ep, err := r.net.Bind(r.signer.ActorID(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	r.pa = actor.New(r.signer, ep)
	r.pa.Clock = r.clock.Load
	r.pa.SeqBase = r.clock.Load
	r.sp = spawn.New(r.pa)
	r.sp.Postage = postage.Require(8)
	r.sp.Window = 10 * 60
	r.sp.KitTTL = 20 * 60
	r.sp.Provisioner = r.prov.ID()
	r.p = parent.New(parent.Options{Actor: r.pa, Spawner: r.sp, Store: r.store, Image: image, Grace: 50 * time.Millisecond, Retries: r.retries})
	r.srv, r.ln = facethttp.Server(child.FacetAgentEvents, r.p.Handler())
	go func() { _ = r.srv.Serve(r.ln) }()
	r.pctx, r.pstop = context.WithCancel(r.ctx)
	t.Cleanup(func() { r.srv.Close() })
}

func (r *rig) consentProvisioner(t *testing.T) {
	t.Helper()
	c, err := cert.Sign(cert.Cert{
		Aud: string(r.pa.ID()),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{r.prov.ID()}, Facet: []string{spawn.FacetSpawn, spawn.FacetExtend, spawn.FacetKill}},
		Iat: t0,
		Exp: t0 + 24*3600,
	}, r.prov.Signer)
	if err != nil {
		t.Fatal(err)
	}
	r.prov.Consents = []cert.Cert{c}
}

// listen starts the parent actor and the housekeeping loop (after
// Restore, if any) and tells it where the provisioner is.
func (r *rig) listen(t *testing.T) {
	t.Helper()
	go func() { _ = r.pa.Listen(r.pctx) }()
	go r.p.Run(r.pctx)
	if _, err := r.pa.PublishLocation(3600); err != nil {
		t.Fatal(err)
	}
	if err := r.pa.UpdateLocation(r.prov.ID(), r.prov.CurrentLocation()); err != nil {
		t.Fatal(err)
	}
}

// restart kills the parent process and brings a new one up on the same
// key and store: Restore, then listen.
func (r *rig) restart(t *testing.T) []string {
	t.Helper()
	r.pstop()
	r.srv.Close()
	r.pa.Transport.(*actor.MemoryEndpoint).Close()
	time.Sleep(20 * time.Millisecond)
	r.up(t)
	lost, err := r.p.Restore()
	if err != nil {
		t.Fatal(err)
	}
	r.listen(t)
	return lost
}

func (r *rig) actor(name string) *actor.Actor {
	r.t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		r.t.Fatal(err)
	}
	s := cert.NewEdSigner(priv)
	ep, err := r.net.Bind(s.ActorID(), name)
	if err != nil {
		r.t.Fatal(err)
	}
	a := actor.New(s, ep)
	a.Clock = r.clock.Load
	return a
}

// dial is the child's Dialer: the parent's Admit over the preamble,
// then a pipe into the facet server — what cmd/parent's accept loop
// does over iroh.
func (r *rig) dial(ctx context.Context, _ cert.ActorID, _ []string, preamble []byte) (io.ReadWriteCloser, error) {
	// The peer is whichever child dialled: the test rig has one live
	// child per session and learns it from the driver.
	var peer cert.ActorID
	var chain []cert.Cert
	if c, _, err := child.DecodePreamble(preamble); err == nil {
		chain = c
	}
	if len(chain) > 0 {
		peer = cert.ActorID(chain[len(chain)-1].Aud)
	}
	if err := r.p.Admit(peer, preamble); err != nil {
		return nil, err
	}
	c1, c2 := net.Pipe()
	go r.ln.Push(ctx, c2, cert.Identity{Key: peer}, peer)
	return c1, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func (r *rig) status(id string) string {
	x, err := r.store.Get(id)
	if err != nil {
		return "?"
	}
	return x.Status
}

func types(evs []parent.Event) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return strings.Join(out, " ")
}

// TestSubmitRunsToSettled: Submit → the child is born, tasked, streams
// into the store, settles with the pointer shape, ships its session,
// and its lease is killed; the transcript is searchable by the owner.
func TestSubmitRunsToSettled(t *testing.T) {
	r := setup(t, false)
	r.listen(t)
	sess, err := r.p.Submit(r.pctx, owner, child.TaskRequest{Prompt: "say hi to the zebra"})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Status != parent.StatusPending || sess.Owner != owner || sess.Image != image {
		t.Fatalf("submitted %+v", sess)
	}
	waitFor(t, "settled", func() bool { return r.status(sess.ID) == parent.StatusSettled })
	x, _ := r.store.Get(sess.ID)
	evs, _ := r.store.Events(sess.ID, 0, 0)
	got := types(evs)
	want := "response agent_start turn_start message_end message_start message_update message_end turn_end agent_end agent_settled response fake_eof child_settled"
	if got != want {
		t.Fatalf("stream:\n got %s\nwant %s", got, want)
	}
	if x.Child == "" || x.Lease == "" || x.Run == "" || x.Events != len(evs) || x.Started == 0 || x.Settled == 0 {
		t.Fatalf("settled session %+v", x)
	}
	waitFor(t, "session file", func() bool { x, _ = r.store.Get(sess.ID); return x.File != "" })
	if b, _ := os.ReadFile(x.File); string(b) != fakeSession {
		t.Fatalf("session file %q", b)
	}
	waitFor(t, "lease killed", func() bool { _, k := r.drv.seen(); return k == 1 })
	hits, _ := r.store.Search(parent.Viewer{Wallet: owner}, "zebra", 0)
	if len(hits) != 2 {
		t.Fatalf("search hits %+v, want the prompt and the answer", hits)
	}
	if hits, _ = r.store.Search(parent.Viewer{Wallet: "0x1111111111111111111111111111111111111111"}, "zebra", 0); len(hits) != 0 {
		t.Fatal("a stranger searches the transcript")
	}

	// A session the parent believes running on a child that is idle
	// (its run ended but the stream never arrived — the pre-resume
	// child, live 2026-10-18): #abort is answered "no task is running"
	// and the parent loses the session instead of waiting for ever.
	if err := r.store.Create(parent.Session{ID: "stuck", Owner: owner, Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Started("stuck", x.Child, x.Lease); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Abort(r.pctx, "stuck", owner); err != nil {
		t.Fatalf("abort on an idle child: %v", err)
	}
	y, _ := r.store.Get("stuck")
	evs, _ = r.store.Events("stuck", 0, 0)
	if y.Status != parent.StatusLost || len(evs) != 1 || evs[0].Type != "child_settled" || evs[0].From != owner {
		t.Fatalf("stuck session after abort: %+v %+v", y, evs)
	}
}

// TestSteerAbortAndRestart: a hanging run takes a steer stamped with
// the sender's wallet; then the parent process restarts — the session
// survives as running, the child's beat still extends the lease
// through the new process, and an abort from the new process ends it
// aborted. A session with no child at restart is lost.
func TestSteerAbortAndRestart(t *testing.T) {
	r := setup(t, true)
	r.listen(t)
	sess, err := r.p.Submit(r.pctx, owner, child.TaskRequest{Prompt: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "message_start", func() bool {
		evs, _ := r.store.Events(sess.ID, 0, 0)
		return strings.HasSuffix(types(evs), "message_start")
	})
	if err := r.p.Steer(r.pctx, sess.ID, owner, "go left"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	waitFor(t, "steer stamped", func() bool {
		evs, _ := r.store.Events(sess.ID, 0, 0)
		return strings.HasSuffix(types(evs), "steer")
	})
	evs, _ := r.store.Events(sess.ID, 0, 0)
	last := evs[len(evs)-1]
	if last.From != owner || !strings.Contains(string(last.Raw), `"queued"`) {
		t.Fatalf("steer line %+v", last)
	}
	// A pending session whose spawn is in flight at the restart.
	r.store.Create(parent.Session{ID: "orphan", Owner: owner, Prompt: "never born"})

	// Let one beat snapshot the born table and consents.
	r.p.Beat(r.pctx)
	kids := r.sp.Children()
	if len(kids) != 1 {
		t.Fatalf("born table %+v", kids)
	}
	// Count extends from before the restart: the child's beat may land
	// at the new process the moment it listens.
	before, _ := r.drv.seen()
	lost := r.restart(t)
	if len(lost) != 1 || lost[0] != "orphan" {
		t.Fatalf("lost %v, want [orphan]", lost)
	}
	if r.status(sess.ID) != parent.StatusRunning || r.status("orphan") != parent.StatusLost {
		t.Fatalf("after restart: %s / %s", r.status(sess.ID), r.status("orphan"))
	}
	if got := r.sp.Children(); len(got) != 1 || got[0].ID != kids[0].ID || got[0].Lease != kids[0].Lease {
		t.Fatalf("adopted %+v want %+v", got, kids)
	}
	// The child's beat renews at the new process and the lease follows.
	r.clock.Add(r.sp.KitTTL / 2)
	waitFor(t, "#extend through the restarted parent", func() bool { n, _ := r.drv.seen(); return n > before })
	// Abort from the new process: the child settles aborted and ships.
	if err := r.p.Abort(r.pctx, sess.ID, owner); err != nil {
		t.Fatalf("abort: %v", err)
	}
	waitFor(t, "aborted", func() bool { return r.status(sess.ID) == parent.StatusAborted })
	evs, _ = r.store.Events(sess.ID, 0, 0)
	if !strings.Contains(types(evs), "abort") || !strings.HasSuffix(types(evs), "child_settled") {
		t.Fatalf("stream %s", types(evs))
	}
	waitFor(t, "lease killed", func() bool { _, k := r.drv.seen(); return k == 1 })
	if err := r.p.Steer(r.pctx, sess.ID, owner, "too late"); err == nil {
		t.Fatal("steer on an ended session accepted")
	}
}

// TestLapsedChildIsLost: a running session whose child vanishes from
// the born table (lapsed at the provisioner) is lost on the beat.
func TestLapsedChildIsLost(t *testing.T) {
	r := setup(t, true)
	r.listen(t)
	sess, err := r.p.Submit(r.pctx, owner, child.TaskRequest{Prompt: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return r.status(sess.ID) == parent.StatusRunning })
	// Past every deadline: the spawner sweeps the child on the beat.
	r.clock.Add(r.sp.KitTTL + 1)
	r.p.Beat(r.pctx)
	if r.status(sess.ID) != parent.StatusLost {
		t.Fatalf("status %s", r.status(sess.ID))
	}
}
