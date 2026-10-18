package parent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/spawn"
)

const (
	owner = "0xF56863bF40A75dC3c632C17fc65AA2EB06a89406"
	other = "0x1111111111111111111111111111111111111111"
	third = "0x2222222222222222222222222222222222222222"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustCreate(t *testing.T, s *Store, id, who, prompt string) {
	t.Helper()
	if err := s.Create(Session{ID: id, Owner: who, Prompt: prompt, Image: "img@sha256:x"}); err != nil {
		t.Fatal(err)
	}
}

// TestSessionLifecycle: pending → running → settled with the pointer;
// a second settle does not overwrite the first word; events count.
func TestSessionLifecycle(t *testing.T) {
	s := open(t)
	mustCreate(t, s, "t1", owner, "fix the bug")
	x, err := s.Get("t1")
	if err != nil || x.Status != StatusPending || x.Owner != strings.ToLower(owner) || !x.Live() {
		t.Fatalf("created: %+v %v", x, err)
	}
	if err := s.Started("t1", "ed:child", "lease-1"); err != nil {
		t.Fatal(err)
	}
	if x, _ = s.ByChild("ed:child", true); x.ID != "t1" || x.Status != StatusRunning {
		t.Fatalf("by child: %+v", x)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Append("t1", "", "", json.RawMessage(`{"type":"turn_start"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Settle("t1", StatusSettled, "", "agent/t1", "abc", "http://git/pr/1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Settle("t1", StatusLost, "restart", "", "", ""); err != nil {
		t.Fatal(err)
	}
	x, _ = s.Get("t1")
	if x.Status != StatusSettled || x.Branch != "agent/t1" || x.PR != "http://git/pr/1" || x.Events != 3 || x.Live() || x.Settled == 0 {
		t.Fatalf("settled: %+v", x)
	}
	if _, err := s.ByChild("ed:child", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("settled session still live by child: %v", err)
	}
	if x, err := s.ByChild("ed:child", false); err != nil || x.ID != "t1" {
		t.Fatalf("settled session by child: %+v %v", x, err)
	}
	if err := s.SetTask("t1", "task-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTask("t1", "task-a"); err != nil {
		t.Fatal("same task again refused")
	}
	if err := s.SetTask("t1", "task-b"); err == nil {
		t.Fatal("second task id accepted")
	}
	if x, err := s.ByTask("task-a"); err != nil || x.ID != "t1" {
		t.Fatalf("by task: %+v %v", x, err)
	}
	if _, err := s.ByTask(""); !errors.Is(err, ErrNotFound) {
		t.Fatal("empty task found something")
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.Started("nope", "ed:x", "l"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("start missing: %v", err)
	}
	if _, err := s.Append("nope", "", "", json.RawMessage(`{}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("append to missing: %v", err)
	}
	if err := s.Create(Session{ID: "bad id!", Owner: owner, Prompt: "x"}); err == nil {
		t.Fatal("bad id accepted")
	}
}

// TestEventsAndWatch: seq is per session and dense; Events pages by
// seq; a subscriber sees what is appended after it subscribed and is
// dropped (channel closed) when it falls behind.
func TestEventsAndWatch(t *testing.T) {
	s := open(t)
	mustCreate(t, s, "a", owner, "p")
	mustCreate(t, s, "b", owner, "p")
	for i := 0; i < 5; i++ {
		if _, err := s.Append("a", "", "", json.RawMessage(`{"type":"x"}`)); err != nil {
			t.Fatal(err)
		}
	}
	ev, _ := s.Append("b", "", "", json.RawMessage(`{"type":"y"}`))
	if ev.Seq != 1 || ev.Type != "y" {
		t.Fatalf("b's first event %+v", ev)
	}
	got, err := s.Events("a", 3, 0)
	if err != nil || len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 5 {
		t.Fatalf("events after 3: %+v %v", got, err)
	}

	ch, cancel := s.Subscribe("a")
	defer cancel()
	stamped, _ := s.Append("a", "steer", other, json.RawMessage(`{"type":"steer","message":"stop"}`))
	select {
	case got := <-ch:
		if got.Seq != 6 || got.From != strings.ToLower(other) && got.From != other || got.Type != "steer" {
			t.Fatalf("watched %+v want %+v", got, stamped)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber saw nothing")
	}
	// b's events do not reach a's watcher.
	s.Append("b", "", "", json.RawMessage(`{"type":"y"}`))
	select {
	case got := <-ch:
		t.Fatalf("a's watcher got b's %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	// Overflow: more than the buffer without draining ⇒ closed.
	for i := 0; i < 300; i++ {
		s.Append("a", "", "", json.RawMessage(`{"type":"x"}`))
	}
	n := 0
	for range ch {
		n++
	}
	if n != 256 {
		t.Fatalf("drained %d before close, want the buffer (256)", n)
	}
	cancel() // idempotent after the drop
}

// TestACL: owner sees and steers; a view share sees but cannot steer;
// a steer share does both; a group share works through Groups; admins
// view everything and steer nothing they do not own; strangers see
// nothing — in List, Can and Search alike.
func TestACL(t *testing.T) {
	s := open(t)
	mustCreate(t, s, "mine", owner, "the needle prompt")
	mustCreate(t, s, "theirs", other, "another needle")
	mustCreate(t, s, "shared-view", other, "needle three")
	mustCreate(t, s, "shared-steer", other, "needle four")
	mustCreate(t, s, "shared-group", other, "needle five")
	if err := s.Share("shared-view", owner, RightView); err != nil {
		t.Fatal(err)
	}
	if err := s.Share("shared-steer", strings.ToUpper(owner), RightSteer); err != nil {
		t.Fatal(err)
	}
	if err := s.Share("shared-group", "group:family", RightView); err != nil {
		t.Fatal(err)
	}
	if err := s.Share("mine", "bogus", RightView); err == nil {
		t.Fatal("bogus principal accepted")
	}
	if err := s.Share("mine", other, "admin"); err == nil {
		t.Fatal("bogus right accepted")
	}
	if err := s.Share("nope", other, RightView); !errors.Is(err, ErrNotFound) {
		t.Fatalf("share on missing: %v", err)
	}
	// The prompt is the first message_end of every run.
	for _, id := range []string{"mine", "theirs", "shared-view", "shared-steer", "shared-group"} {
		x, _ := s.Get(id)
		line, _ := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": x.Prompt}})
		if _, err := s.Append(id, "", "", line); err != nil {
			t.Fatal(err)
		}
	}

	me := Viewer{Wallet: owner}
	fam := Viewer{Wallet: third, Groups: []string{"family"}}
	admin := Viewer{Wallet: third, Groups: []string{"admins"}}
	stranger := Viewer{Wallet: third}

	ids := func(v Viewer) string {
		xs, err := s.List(v, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range xs {
			out = append(out, x.ID)
		}
		return strings.Join(out, ",")
	}
	// List is newest first; all created within the same second, so
	// compare as sets.
	set := func(csv string) map[string]bool {
		m := map[string]bool{}
		for _, x := range strings.Split(csv, ",") {
			if x != "" {
				m[x] = true
			}
		}
		return m
	}
	eq := func(got, want string) {
		t.Helper()
		g, w := set(got), set(want)
		if len(g) != len(w) {
			t.Fatalf("list %q want %q", got, want)
		}
		for k := range w {
			if !g[k] {
				t.Fatalf("list %q want %q", got, want)
			}
		}
	}
	eq(ids(me), "mine,shared-view,shared-steer")
	eq(ids(fam), "shared-group")
	eq(ids(admin), "mine,theirs,shared-view,shared-steer,shared-group")
	eq(ids(stranger), "")

	can := func(id string, v Viewer, right string) bool {
		ok, err := s.Can(id, v, right)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !can("mine", me, RightSteer) || !can("mine", me, RightView) {
		t.Fatal("owner lacks rights")
	}
	if !can("shared-view", me, RightView) || can("shared-view", me, RightSteer) {
		t.Fatal("view share wrong")
	}
	if !can("shared-steer", me, RightView) || !can("shared-steer", me, RightSteer) {
		t.Fatal("steer share wrong")
	}
	if !can("shared-group", fam, RightView) || can("shared-group", fam, RightSteer) || can("shared-group", stranger, RightView) {
		t.Fatal("group share wrong")
	}
	if !can("theirs", admin, RightView) || can("theirs", admin, RightSteer) {
		t.Fatal("admin rights wrong")
	}
	if can("theirs", stranger, RightView) {
		t.Fatal("stranger views")
	}
	if _, err := s.Can("nope", me, RightView); !errors.Is(err, ErrNotFound) {
		t.Fatalf("can on missing: %v", err)
	}

	hits := func(v Viewer, q string) int {
		hs, err := s.Search(v, q, 0)
		if err != nil {
			t.Fatal(err)
		}
		return len(hs)
	}
	if hits(me, "needle") != 3 || hits(admin, "needle") != 5 || hits(fam, "needle") != 1 || hits(stranger, "needle") != 0 {
		t.Fatalf("search hits me=%d admin=%d fam=%d stranger=%d", hits(me, "needle"), hits(admin, "needle"), hits(fam, "needle"), hits(stranger, "needle"))
	}
	if hits(me, `need"le OR "`) != 0 || hits(me, "") != 0 {
		t.Fatal("hostile / empty query")
	}
	if hits(me, "NEEDL") != 3 {
		t.Fatal("prefix, case-insensitive")
	}

	// Unshare and share update.
	if err := s.Unshare("shared-view", owner); err != nil {
		t.Fatal(err)
	}
	if can("shared-view", me, RightView) {
		t.Fatal("unshared still views")
	}
	s.Share("shared-group", "group:family", RightSteer)
	if shs, _ := s.Shares("shared-group"); len(shs) != 1 || shs[0].Right != RightSteer {
		t.Fatalf("shares %+v", shs)
	}
}

// TestIndexText pins the FTS policy over pi's record shapes.
func TestIndexText(t *testing.T) {
	cases := []struct{ typ, raw, want string }{
		{"message_end", `{"message":{"role":"user","content":"Review this repository"}}`, "Review this repository"},
		{"message_end", `{"message":{"role":"assistant","content":[{"type":"text","text":"Hello"},{"type":"toolCall","name":"bash","arguments":{"command":"ls"}}]}}`, "Hello\nbash {\"command\":\"ls\"}"},
		{"message_end", `{"message":{"role":"toolResult","content":[{"type":"toolResult","content":[{"type":"text","text":"out"}]}]}}`, "out"},
		{"tool_execution_end", `{"toolName":"bash","result":{"content":[{"type":"text","text":"complete output"}]}}`, "bash complete output"},
		{"message_update", `{"assistantMessageEvent":{"type":"text_delta","delta":"Hello"}}`, ""},
		{"steer", `{"type":"steer","message":"try harder"}`, "try harder"},
		{"child_settled", `{"type":"child_settled","status":"failed","reason":"pi exited 1"}`, "pi exited 1"},
		{"turn_start", `{"type":"turn_start"}`, ""},
		{"message_end", `not json`, ""},
	}
	for _, c := range cases {
		if got := IndexText(c.typ, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("%s %s: %q want %q", c.typ, c.raw, got, c.want)
		}
	}
}

// TestFile: the shipped session JSONL lands under the dir and on the row.
func TestFile(t *testing.T) {
	s := open(t)
	mustCreate(t, s, "f", owner, "p")
	n, err := s.PutFile("f", strings.NewReader("{\"type\":\"session\"}\n"))
	if err != nil || n != 19 {
		t.Fatalf("put: %d %v", n, err)
	}
	x, _ := s.Get("f")
	if !strings.HasSuffix(x.File, "/sessions/f.jsonl") {
		t.Fatalf("file %q", x.File)
	}
	if _, err := s.PutFile("nope", strings.NewReader("")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("put missing: %v", err)
	}
	if _, err := s.PutFile("../etc", strings.NewReader("")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("put traversal: %v", err)
	}
}

// TestCaches: children round-trip with the restart floor; consents
// round-trip through the wire encoding and drop expired ones; LoseLive
// ends what the keep predicate does not vouch for and stamps a line.
func TestCaches(t *testing.T) {
	s := open(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := cert.NewEdSigner(priv)
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	childA, childB := cert.NewEdSigner(privA).ActorID(), cert.NewEdSigner(privB).ActorID()
	now := time.Now().Unix()

	in := []spawn.Adopted{
		{ID: "ed:aa", Lease: spawn.Lease{Provisioner: "ed:pp", ID: "l1"}, Until: now - 10},
		{ID: "ed:bb", Lease: spawn.Lease{Provisioner: "ed:pp", ID: "l2"}, Until: now + 500},
	}
	if err := s.PutChildren(in); err != nil {
		t.Fatal(err)
	}
	out, err := s.Children(now + 60)
	if err != nil || len(out) != 2 {
		t.Fatalf("children %+v %v", out, err)
	}
	byID := map[cert.ActorID]spawn.Adopted{}
	for _, c := range out {
		byID[c.ID] = c
	}
	if byID["ed:aa"].Until != now+60 || byID["ed:bb"].Until != now+500 || byID["ed:bb"].Lease != in[1].Lease {
		t.Fatalf("children %+v", out)
	}
	if err := s.PutChildren(nil); err != nil {
		t.Fatal(err)
	}
	if out, _ = s.Children(0); len(out) != 0 {
		t.Fatalf("children after clear %+v", out)
	}

	live, _ := cert.Sign(cert.Cert{Aud: string(childA), Can: cert.VerbInvoke, Cav: cert.Caveats{Target: []cert.ActorID{signer.ActorID()}, Facet: []string{"agent-events"}}, Iat: now, Exp: now + 300}, signer)
	dead, _ := cert.Sign(cert.Cert{Aud: string(childB), Can: cert.VerbInvoke, Cav: cert.Caveats{Target: []cert.ActorID{signer.ActorID()}, Facet: []string{"x"}}, Iat: now - 100, Exp: now - 1}, signer)
	if err := s.PutConsents([]cert.Cert{live, dead, {}}, now); err != nil {
		t.Fatal(err)
	}
	cs, err := s.Consents(now)
	if err != nil || len(cs) != 1 || string(cs[0].Sig) != string(live.Sig) || cs[0].Aud != string(childA) {
		t.Fatalf("consents %+v %v", cs, err)
	}
	if err := cert.Verify(cs[0]); err != nil {
		t.Fatal("round-tripped consent does not verify")
	}

	mustCreate(t, s, "r1", owner, "p")
	mustCreate(t, s, "r2", owner, "p")
	mustCreate(t, s, "r3", owner, "p")
	s.Started("r1", "ed:aa", "l1")
	s.Started("r2", "ed:bb", "l2")
	s.Settle("r3", StatusSettled, "", "", "", "")
	lost, err := s.LoseLive("parent restarted", func(c cert.ActorID) bool { return c == "ed:bb" })
	if err != nil || len(lost) != 1 || lost[0] != "r1" {
		t.Fatalf("lost %v %v", lost, err)
	}
	x, _ := s.Get("r1")
	evs, _ := s.Events("r1", 0, 0)
	if x.Status != StatusLost || x.Reason != "parent restarted" || len(evs) != 1 || evs[0].Type != "child_settled" {
		t.Fatalf("lost session %+v %+v", x, evs)
	}
	if x, _ = s.Get("r2"); x.Status != StatusRunning {
		t.Fatalf("kept session %+v", x)
	}
}
