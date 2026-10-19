package parent

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateSessions: a store from before the task layer (sessions.task
// held the child's run id) opens with that column renamed to run, a
// fresh task column, and the default flow seeded.
func TestMigrateSessions(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "parent.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(schema, "\trun      TEXT NOT NULL DEFAULT '',\n\ttask     TEXT NOT NULL DEFAULT '',", "\ttask     TEXT NOT NULL DEFAULT '',", 1)
	if old == schema {
		t.Fatal("old schema not derived")
	}
	if _, err := db.Exec(old + `CREATE INDEX IF NOT EXISTS sessions_task ON sessions(task);`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, owner, prompt, status, task, created) VALUES ('s1', '0xab', 'p', 'settled', 'run-1', 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	x, err := s.Get("s1")
	if err != nil || x.Run != "run-1" || x.Task != "" {
		t.Fatalf("migrated session %+v %v", x, err)
	}
	if y, err := s.ByRun("run-1"); err != nil || y.ID != "s1" {
		t.Fatalf("ByRun after migration: %+v %v", y, err)
	}
	f, err := s.Flow(DefaultFlowID, 0)
	if err != nil || f.Version != 1 || f.Initial != "captured" {
		t.Fatalf("default flow %+v %v", f, err)
	}
	// Opening again is idempotent.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}

func TestFlowValidateAndParse(t *testing.T) {
	if err := DefaultFlow().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := DefaultFlow()
	bad.States["framed"] = State{Driver: DriverAgent, Persona: "nope", Next: []string{"groomed"}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "persona") {
		t.Fatalf("missing persona: %v", err)
	}
	bad = DefaultFlow()
	bad.States["lonely"] = State{Driver: DriverHuman}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("unreachable: %v", err)
	}
	bad = DefaultFlow()
	bad.Stuck = "framed"
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("stuck driver: %v", err)
	}

	tr, ok, err := ParseTransition("thinking…\n```transition\n{\"to\":\"a\"}\n```\nmore\n```transition\n{\"to\": \"reviewing\", \"note\": \"n\", \"artifact\": {\"pr\": 1}, \"set\": {\"goal\": \"g1\"}}\n```\n")
	if err != nil || !ok || tr.To != "reviewing" || tr.Note != "n" || string(tr.Artifact) != `{"pr": 1}` || tr.Set == nil || *tr.Set.Goal != "g1" {
		t.Fatalf("parse: %+v %v %v", tr, ok, err)
	}
	if _, ok, _ := ParseTransition("no block here"); ok {
		t.Fatal("found a block in nothing")
	}
	if _, ok, err := ParseTransition("```transition\n{not json\n```"); !ok || err == nil {
		t.Fatalf("bad json: %v %v", ok, err)
	}
	if _, ok, err := ParseTransition("```transition\n{\"note\":\"x\"}\n```"); !ok || err == nil {
		t.Fatalf("no to: %v %v", ok, err)
	}
}

func TestStoreProjectsAndTasks(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Flows are immutable per version.
	f := DefaultFlow()
	f.Personas["framer"] = "changed"
	if err := s.PutFlow(f); err == nil || !strings.Contains(err.Error(), "bump") {
		t.Fatalf("rewrote a flow version: %v", err)
	}
	f.Version = 2
	if err := s.PutFlow(f); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Flow(DefaultFlowID, 0); got.Version != 2 {
		t.Fatalf("latest %d", got.Version)
	}
	if fs, _ := s.Flows(); len(fs) != 1 {
		t.Fatalf("flows %+v", fs)
	}

	pr, err := s.CreateProject(Project{ID: "p", Name: " P ", Owner: "0xAB"})
	if err != nil || pr.Owner != "0xab" || pr.Name != "P" || pr.FlowVersion != 2 {
		t.Fatalf("project %+v %v", pr, err)
	}
	if _, err := s.CreateProject(Project{ID: "q", Name: "Q", Owner: "0xab", Flow: "missing"}); !errors.Is(err, ErrNoFlow) {
		t.Fatalf("missing flow: %v", err)
	}
	if err := s.PutGoals("p", []Goal{{ID: "a", Text: "A"}, {ID: "a", Text: "dup"}}); err == nil {
		t.Fatal("duplicate goal ids")
	}
	if err := s.PutGoals("p", []Goal{{ID: "b", Text: "B"}, {ID: "a", Text: "A"}}); err != nil {
		t.Fatal(err)
	}
	if gs, _ := s.Goals("p"); len(gs) != 2 || gs[0].ID != "b" {
		t.Fatalf("goals %+v", gs)
	}
	mine := Viewer{Wallet: "0xAB"}
	other := Viewer{Wallet: "0x1111111111111111111111111111111111111111"}
	admin := Viewer{Wallet: other.Wallet, Groups: []string{"admins"}}
	if ps, _ := s.Projects(other); len(ps) != 0 {
		t.Fatal("stranger lists projects")
	}
	if ps, _ := s.Projects(admin); len(ps) != 1 {
		t.Fatal("admin lists nothing")
	}

	tk, err := s.InsertTask(Task{ID: "t1", Project: "p", Owner: "0xAB", Title: " T ", State: "captured"}, "0xab", "filed")
	if err != nil || tk.Flow != DefaultFlowID || tk.FlowVersion != 2 || tk.Title != "T" {
		t.Fatalf("task %+v %v", tk, err)
	}
	if !s.CanTask(tk, mine) || s.CanTask(tk, other) || !s.CanTask(tk, admin) {
		t.Fatal("task ACL")
	}
	why := "because"
	moved, _, err := s.Move("t1", "captured", "framed", "0xab", "", Transition{Note: "go", Artifact: []byte(`{"x":1}`), Set: &TaskSet{Why: &why, Title: strPtr("  ")}})
	if err != nil || moved.State != "framed" || moved.Why != "because" || moved.Title != "T" {
		t.Fatalf("move %+v %v", moved, err)
	}
	if _, _, err := s.Move("t1", "captured", "framed", "0xab", "", Transition{}); err == nil {
		t.Fatal("moved from a state the task left")
	}
	if n, _ := s.Retry("t1", "boom"); n != 1 {
		t.Fatalf("retry %d", n)
	}
	if x, _ := s.Task("t1"); x.Error != "boom" || x.Session != "" {
		t.Fatalf("after retry %+v", x)
	}
	if err := s.Create(Session{ID: "s1", Owner: "0xab", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach("t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.TaskBySession("s1"); x.ID != "t1" || x.Session != "s1" {
		t.Fatalf("by session %+v", x)
	}
	if ss, _ := s.SessionsOfTask("t1"); len(ss) != 1 || ss[0].Task != "t1" {
		t.Fatalf("sessions of task %+v", ss)
	}
	log, _ := s.Log("t1")
	if len(log) != 2 || log[0].To != "captured" || log[1].From != "captured" || string(log[1].Artifact) != `{"x":1}` || log[1].Note != "go" {
		t.Fatalf("log %+v", log)
	}
	line := "{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[" +
		"{\"type\":\"toolCall\",\"name\":\"bash\",\"arguments\":{\"cmd\":\"echo '```transition'\"}}," +
		"{\"type\":\"text\",\"text\":\"done\\n```transition\\n{\\\"to\\\":\\\"x\\\"}\\n```\"}]}}"
	if _, err := s.Append("s1", "message_end", "", []byte(line)); err != nil {
		t.Fatal(err)
	}
	end, _ := s.LastAssistant("s1")
	if tr, ok, err := ParseTransition(end.Text); err != nil || !ok || tr.To != "x" || end.Failed() {
		t.Fatalf("last text %q → %+v %v %v", end.Text, tr, ok, err)
	}
	failed := `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"402 {\"type\":\"error\"}"}}`
	if _, err := s.Append("s1", "message_end", "", []byte(failed)); err != nil {
		t.Fatal(err)
	}
	if end, _ := s.LastAssistant("s1"); !end.Failed() || end.Text != "" || !strings.HasPrefix(end.Error, "402 ") {
		t.Fatalf("failed run %+v", end)
	}
	ts, _ := s.Tasks(mine, "", 0)
	if len(ts) != 1 {
		t.Fatalf("tasks %+v", ts)
	}
	if ts, _ := s.Tasks(other, "", 0); len(ts) != 0 {
		t.Fatal("stranger lists tasks")
	}
	agentOnes, _ := s.TasksInStates(func(f Flow, st string) bool { return f.States[st].Driver == DriverAgent })
	if len(agentOnes) != 1 {
		t.Fatalf("in agent states %+v", agentOnes)
	}
}

func strPtr(s string) *string { return &s }
