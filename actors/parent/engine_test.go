package parent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/parent"
)

// testFlow is a flow whose personas are markers the fake pi answers
// to: `fake:to=<state>` ends the run with a transition there,
// `fake:children=<state>` with two children; "plain" ends with none.
func testFlow() parent.Flow {
	return parent.Flow{
		ID: "test", Version: 1, Initial: "captured", Leaf: "work", Stuck: "stuck",
		States: map[string]parent.State{
			"captured":   {Driver: parent.DriverHuman, Next: []string{"work", "split", "rejected"}},
			"split":      {Driver: parent.DriverAgent, Persona: "splitter", Next: []string{"delegated", "stuck"}},
			"delegated":  {Driver: parent.DriverChildren, Next: []string{"done"}},
			"work":       {Driver: parent.DriverAgent, Persona: "worker", Next: []string{"review", "stuck"}},
			"review":     {Driver: parent.DriverAgent, Persona: "reviewer", Next: []string{"done", "work", "stuck"}},
			"flaky":      {Driver: parent.DriverAgent, Persona: "plain", Next: []string{"done", "stuck"}},
			"broke":      {Driver: parent.DriverAgent, Persona: "broke", Next: []string{"done", "stuck"}},
			"throttled":  {Driver: parent.DriverAgent, Persona: "throttled", Next: []string{"done", "stuck"}},
			"pingwork":   {Driver: parent.DriverAgent, Persona: "pinger", Next: []string{"pingreview", "stuck"}},
			"pingreview": {Driver: parent.DriverAgent, Persona: "bouncer", Next: []string{"pingwork", "done", "stuck"}},
			"stuck":      {Driver: parent.DriverHuman, Next: []string{"work", "flaky", "broke", "throttled", "pingwork", "rejected"}},
			"done":       {Driver: parent.DriverHuman},
			"rejected":   {Driver: parent.DriverHuman},
		},
		Personas: map[string]string{
			"worker":    "You are the worker. fake:to=review",
			"reviewer":  "You are the reviewer. fake:to=done",
			"splitter":  "You split. fake:children=delegated",
			"plain":     "You say nothing useful.",
			"broke":     "The provider refuses you. fake:error=402",
			"throttled": "The provider throttles you. fake:error=429",
			"pinger":    "You work. fake:to=pingreview",
			"bouncer":   "You never approve. fake:to=pingwork",
		},
	}
}

func (r *rig) project(t *testing.T) parent.Project {
	t.Helper()
	if err := r.store.PutFlow(testFlow()); err != nil {
		t.Fatal(err)
	}
	pr, err := r.store.CreateProject(parent.Project{ID: "p1", Name: "Project one", Owner: owner, Flow: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.store.PutGoals("p1", []parent.Goal{{ID: "g1", Text: "ship it"}, {ID: "g2", Text: "keep it simple"}}); err != nil {
		t.Fatal(err)
	}
	return pr
}

func (r *rig) taskState(id string) string {
	x, err := r.store.Task(id)
	if err != nil {
		return "?"
	}
	return x.State
}

// TestTaskRunsThroughFlow: a task filed with start skips capture,
// the worker's session moves it to review, the reviewer's to done;
// every move is logged with the session that asked and its artifact;
// the rewrite ("set") lands on the task; the prompt carried the frame.
func TestTaskRunsThroughFlow(t *testing.T) {
	r := setup(t, false)
	r.listen(t)
	r.project(t)
	tk, err := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: "do a thing", Goal: "g1", Why: "it matters", Repo: ""}, true)
	if err != nil {
		t.Fatal(err)
	}
	if tk.State != "work" || tk.Flow != "test" || tk.FlowVersion != 1 {
		t.Fatalf("filed %+v", tk)
	}
	waitFor(t, "done", func() bool { return r.taskState(tk.ID) == "done" })
	x, _ := r.store.Task(tk.ID)
	if x.Session != "" || x.Retries != 0 || x.Why != "because the fake said so" {
		t.Fatalf("done task %+v", x)
	}
	log, _ := r.store.Log(tk.ID)
	var path []string
	for _, e := range log {
		path = append(path, e.From+">"+e.To)
	}
	if got := strings.Join(path, " "); got != ">captured captured>work work>review review>done" {
		t.Fatalf("log path %s", got)
	}
	if log[1].By != owner || !strings.HasPrefix(log[2].By, "session:") || log[2].Session == "" || string(log[2].Artifact) != `{"ok":true}` || log[2].Note != "fake moved on" {
		t.Fatalf("log entries %+v", log)
	}
	sessions, _ := r.store.SessionsOfTask(tk.ID)
	if len(sessions) != 2 || sessions[0].Task != tk.ID || sessions[0].Status != parent.StatusSettled {
		t.Fatalf("sessions %+v", sessions)
	}
	for _, want := range []string{"You are the worker.", "Project: Project one", "Goal: ship it", "Because: it matters", "Title: do a thing", "- review", "- stuck", "```transition"} {
		if !strings.Contains(sessions[0].Prompt, want) {
			t.Fatalf("worker prompt lacks %q:\n%s", want, sessions[0].Prompt)
		}
	}
	if !strings.Contains(sessions[1].Prompt, "Previous step\nwork → review by session:") || !strings.Contains(sessions[1].Prompt, `{"ok":true}`) {
		t.Fatalf("reviewer prompt lacks the previous step:\n%s", sessions[1].Prompt)
	}

	// A person may not move a task an agent holds (live session) or
	// take an exit the state lacks; from a human state they may.
	tk2, err := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: "captured only"}, false)
	if err != nil || tk2.State != "captured" {
		t.Fatalf("captured task %+v %v", tk2, err)
	}
	if _, err := r.p.Transition(r.pctx, tk2.ID, owner, parent.Transition{To: "done"}); err == nil {
		t.Fatal("captured → done is not an exit")
	}
	if _, err := r.p.Transition(r.pctx, tk2.ID, owner, parent.Transition{To: "work", Note: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "session live", func() bool {
		x, _ := r.store.Task(tk2.ID)
		return x.Session != "" && r.status(x.Session) == parent.StatusRunning
	})
	if _, err := r.p.Transition(r.pctx, tk2.ID, owner, parent.Transition{To: "stuck"}); err == nil || !strings.Contains(err.Error(), "live") {
		t.Fatalf("moved a task an agent holds: %v", err)
	}
	waitFor(t, "second task done", func() bool { return r.taskState(tk2.ID) == "done" })
}

// TestTaskRetriesThenParks: a run that ends without a transition block
// counts as failed; after Retries more attempts the task is parked in
// the stuck state with the reason. A terminal task does not move.
func TestTaskRetriesThenParks(t *testing.T) {
	r := setupWith(t, false, 1)
	r.listen(t)
	r.project(t)
	tk, err := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: "flaky"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// captured has no exit to flaky: set the scene through the store.
	if _, _, err := r.store.Move(tk.ID, "captured", "stuck", owner, "", parent.Transition{Note: "scene"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Transition(r.pctx, tk.ID, owner, parent.Transition{To: "flaky"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "parked", func() bool { return r.taskState(tk.ID) == "stuck" })
	log, _ := r.store.Log(tk.ID)
	last := log[len(log)-1]
	if last.By != "parent" || !strings.Contains(last.Note, "gave up after 2 attempts") || !strings.Contains(last.Note, "without a transition block") {
		t.Fatalf("park entry %+v", last)
	}
	sessions, _ := r.store.SessionsOfTask(tk.ID)
	if len(sessions) != 2 || !strings.Contains(sessions[1].Prompt, "attempt 2") || !strings.Contains(sessions[1].Prompt, "Previous attempt failed: the run ended without a transition block") {
		t.Fatalf("attempts %+v", sessions)
	}

	tk2, _ := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: "no"}, false)
	if _, err := r.p.Transition(r.pctx, tk2.ID, owner, parent.Transition{To: "rejected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Transition(r.pctx, tk2.ID, owner, parent.Transition{To: "work"}); err == nil {
		t.Fatal("moved a terminal task")
	}
}

// TestTaskBouncesThenParks: a reviewer that keeps sending the task
// back is bounded like failed attempts are — Retries bounces along the
// same edge, then the task is parked for a person with the count and
// the reviewer's note. The human's own re-sends are not counted.
func TestTaskBouncesThenParks(t *testing.T) {
	r := setupWith(t, false, 1)
	r.listen(t)
	r.project(t)
	tk := r.stuckAt(t, "ping-pong", "pingwork")
	waitFor(t, "parked", func() bool { return r.taskState(tk.ID) == "stuck" })
	log, _ := r.store.Log(tk.ID)
	var path []string
	for _, e := range log {
		path = append(path, e.From+">"+e.To)
	}
	if got := strings.Join(path, " "); got != ">captured captured>stuck stuck>pingwork pingwork>pingreview pingreview>pingwork pingwork>pingreview pingreview>stuck" {
		t.Fatalf("log path %s", got)
	}
	last := log[len(log)-1]
	if last.By != "parent" || !strings.Contains(last.Note, "sent pingreview → pingwork by an agent for the 2nd time: fake moved on") {
		t.Fatalf("park entry %+v", last)
	}
	// A person sending it round again starts the count afresh only in
	// the sense that their move is not a bounce: the agents' two are
	// still on the log, so the next agent bounce parks it at once.
	if _, err := r.p.Transition(r.pctx, tk.ID, owner, parent.Transition{To: "pingwork", Note: "once more"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pingreview", func() bool { return r.taskState(tk.ID) == "pingreview" })
	waitFor(t, "parked again", func() bool { return r.taskState(tk.ID) == "stuck" })
	sessions, _ := r.store.SessionsOfTask(tk.ID)
	if len(sessions) != 6 {
		t.Fatalf("%d sessions, want 6", len(sessions))
	}
}

// stuckAt sets the scene for a stuck-state exit: a captured task moved
// to stuck through the store, then sent to state by a person.
func (r *rig) stuckAt(t *testing.T, title, state string) parent.Task {
	t.Helper()
	tk, err := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: title}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.store.Move(tk.ID, "captured", "stuck", owner, "", parent.Transition{Note: "scene"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Transition(r.pctx, tk.ID, owner, parent.Transition{To: state}); err != nil {
		t.Fatal(err)
	}
	return tk
}

// TestTaskModelErrors: a run whose model call the provider refused is
// reported as that, not as a missing transition block. A refusal no
// retry can change (402 billing) parks the task at once, keeping its
// attempts; a throttle (429) is retried like any failure, the reason
// carried on the task and into the next prompt.
func TestTaskModelErrors(t *testing.T) {
	r := setupWith(t, false, 1)
	r.listen(t)
	r.project(t)

	broke := r.stuckAt(t, "no credits", "broke")
	waitFor(t, "parked on 402", func() bool { return r.taskState(broke.ID) == "stuck" })
	log, _ := r.store.Log(broke.ID)
	last := log[len(log)-1]
	if last.By != "parent" || !strings.HasPrefix(last.Note, "not retried, model error: 402 ") || !strings.Contains(last.Note, "fake refusal") {
		t.Fatalf("402 park entry %+v", last)
	}
	if x, _ := r.store.Task(broke.ID); x.Retries != 0 {
		t.Fatalf("402 counted as an attempt %+v", x)
	}
	if sessions, _ := r.store.SessionsOfTask(broke.ID); len(sessions) != 1 {
		t.Fatalf("402 retried: %d sessions", len(sessions))
	}

	throttled := r.stuckAt(t, "rate limited", "throttled")
	waitFor(t, "parked on 429", func() bool { return r.taskState(throttled.ID) == "stuck" })
	log, _ = r.store.Log(throttled.ID)
	last = log[len(log)-1]
	if !strings.Contains(last.Note, "gave up after 2 attempts: model error: 429 ") {
		t.Fatalf("429 park entry %+v", last)
	}
	sessions, _ := r.store.SessionsOfTask(throttled.ID)
	if len(sessions) != 2 || !strings.Contains(sessions[1].Prompt, "Previous attempt failed: model error: 429 ") {
		t.Fatalf("429 attempts %+v", sessions)
	}
}

// TestTaskRetryBackoff: with a backoff set, a failed attempt is not
// respawned at once nor by a beat that comes too early; it is once
// the backoff has run out.
func TestTaskRetryBackoff(t *testing.T) {
	r := setupBackoff(t, false, 1, time.Hour)
	r.listen(t)
	r.project(t)
	tk := r.stuckAt(t, "flaky", "flaky")
	waitFor(t, "first attempt failed", func() bool { x, _ := r.store.Task(tk.ID); return x.Retries == 1 })
	r.p.Beat(r.pctx)
	r.p.Beat(r.pctx)
	x, _ := r.store.Task(tk.ID)
	if x.Session != "" || x.State != "flaky" || !strings.Contains(x.Error, "without a transition block") {
		t.Fatalf("respawned inside the backoff: %+v", x)
	}
	if sessions, _ := r.store.SessionsOfTask(tk.ID); len(sessions) != 1 {
		t.Fatalf("%d sessions inside the backoff", len(sessions))
	}
	// Age the failure past the backoff: the next beat spawns attempt 2.
	if err := r.store.AgeTask(tk.ID, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	r.p.Beat(r.pctx)
	waitFor(t, "parked", func() bool { return r.taskState(tk.ID) == "stuck" })
	if sessions, _ := r.store.SessionsOfTask(tk.ID); len(sessions) != 2 {
		t.Fatalf("%d sessions after the backoff", len(sessions))
	}
}

// TestTaskSplitsIntoChildren: the splitter carves two children (at
// the leaf state) and a follow-up (at initial); the parent waits in
// its children state and moves on by itself once both children end.
func TestTaskSplitsIntoChildren(t *testing.T) {
	r := setup(t, false)
	r.listen(t)
	r.project(t)
	tk, err := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: "big thing", Goal: "g2", Why: "spans repos"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Transition(r.pctx, tk.ID, owner, parent.Transition{To: "split"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "delegated", func() bool { return r.taskState(tk.ID) == "delegated" })
	kids, _ := r.store.ChildTasks(tk.ID)
	if len(kids) != 2 || kids[0].Title != "part a" || kids[0].Goal != "g2" || kids[0].Why != "spans repos" || kids[0].Parent != tk.ID {
		t.Fatalf("children %+v", kids)
	}
	all, _ := r.store.Tasks(parent.Viewer{Wallet: owner}, "p1", 0)
	var followup parent.Task
	for _, x := range all {
		if x.Origin == tk.ID {
			followup = x
		}
	}
	if followup.ID == "" || followup.State != "captured" || followup.Title != "later" {
		t.Fatalf("follow-up %+v", followup)
	}
	if _, err := r.p.Transition(r.pctx, tk.ID, owner, parent.Transition{To: "done"}); err == nil {
		t.Fatal("a person moved a task waiting for children")
	}
	waitFor(t, "parent done", func() bool { return r.taskState(tk.ID) == "done" })
	for _, k := range kids {
		if r.taskState(k.ID) != "done" {
			t.Fatalf("child %s is %s", k.ID, r.taskState(k.ID))
		}
	}
	log, _ := r.store.Log(tk.ID)
	last := log[len(log)-1]
	if last.By != "parent" || last.From != "delegated" || !strings.Contains(string(last.Artifact), `"part a"`) {
		t.Fatalf("join entry %+v", last)
	}

	// The child's prompt carried its parent's frame.
	ks, _ := r.store.SessionsOfTask(kids[0].ID)
	if len(ks) == 0 || !strings.Contains(ks[0].Prompt, "Part of: big thing — spans repos") || !strings.Contains(ks[0].Prompt, "Title: part a") {
		t.Fatalf("child prompt:\n%s", ks[0].Prompt)
	}
}

// TestTaskReconcile: an abort parks the task for a person; a session
// the parent lost is retried by the beat; a task moved but never
// spawned (a crash between move and spawn) is spawned by the beat.
func TestTaskReconcile(t *testing.T) {
	r := setup(t, true) // hanging fake: runs never end on their own
	r.listen(t)
	r.project(t)
	tk, err := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: "survive"}, true)
	if err != nil {
		t.Fatal(err)
	}
	running := func(id string) func() bool {
		return func() bool {
			x, _ := r.store.Task(id)
			return x.Session != "" && r.status(x.Session) == parent.StatusRunning
		}
	}
	waitFor(t, "running", running(tk.ID))
	first, _ := r.store.Task(tk.ID)
	// Started lands at birth, before #task: wait for the run's stream.
	waitFor(t, "streaming", func() bool { x, _ := r.store.Get(first.Session); return x.Events >= 5 })
	if err := r.p.Abort(r.pctx, first.Session, owner); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "parked after abort", func() bool { return r.taskState(tk.ID) == "stuck" })
	log, _ := r.store.Log(tk.ID)
	if last := log[len(log)-1]; last.By != "parent" || !strings.HasPrefix(last.Note, "run aborted") || last.Session != first.Session {
		t.Fatalf("abort entry %+v", last)
	}

	if _, err := r.p.Transition(r.pctx, tk.ID, owner, parent.Transition{To: "work", Note: "again"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running again", running(tk.ID))
	second, _ := r.store.Task(tk.ID)
	// The parent loses the session (what a restart does to a child it
	// cannot re-adopt); the next beat retries with a fake that answers.
	t.Setenv("PARENT_FAKE_HANG", "0")
	if err := r.store.Settle(second.Session, parent.StatusLost, "test lost it", "", "", ""); err != nil {
		t.Fatal(err)
	}
	r.p.Beat(r.pctx)
	waitFor(t, "retried", func() bool { x, _ := r.store.Task(tk.ID); return x.Session != "" && x.Session != second.Session })
	if x, _ := r.store.Task(tk.ID); x.Retries != 1 || x.State != "work" {
		t.Fatalf("after loss %+v", x)
	}
	waitFor(t, "done", func() bool { return r.taskState(tk.ID) == "done" })

	// Moved but never spawned.
	tk2, _ := r.p.CreateTask(r.pctx, owner, "p1", parent.TaskSpec{Title: "orphan"}, false)
	if _, _, err := r.store.Move(tk2.ID, "captured", "work", owner, "", parent.Transition{}); err != nil {
		t.Fatal(err)
	}
	r.p.Beat(r.pctx)
	waitFor(t, "orphan done", func() bool { return r.taskState(tk2.ID) == "done" })
}
