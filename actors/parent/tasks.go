package parent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Project groups tasks that serve one set of goals; repos are what its
// tasks touch, not what owns them (spike y8m0). Owner is the wallet
// that made it; it and admins see the project's tasks.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Flow        string `json:"flow"`
	FlowVersion int    `json:"flow_version"`
	Created     int64  `json:"created"`
}

// Goal is one of a project's values: what a task is framed against.
type Goal struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Task is the unit that moves through a project's flow. Session is
// the live session running its current agent state, "" otherwise.
type Task struct {
	ID          string `json:"id"`
	Project     string `json:"project"`
	Owner       string `json:"owner"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Goal        string `json:"goal,omitempty"`
	Why         string `json:"why,omitempty"`
	// Parent is the task this one was carved out of; Origin the task
	// whose run filed it as a follow-up.
	Parent      string `json:"parent,omitempty"`
	Origin      string `json:"origin,omitempty"`
	Repo        string `json:"repo,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Flow        string `json:"flow"`
	FlowVersion int    `json:"flow_version"`
	State       string `json:"state"`
	Retries     int    `json:"retries"`
	// Error is why the last attempt at this state failed, "" after a
	// move; the next attempt's prompt carries it.
	Error   string `json:"error,omitempty"`
	Session string `json:"session,omitempty"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
}

// LogEntry is one transition a task took.
type LogEntry struct {
	Task     string          `json:"task"`
	Seq      int64           `json:"seq"`
	At       int64           `json:"at"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	By       string          `json:"by"` // a wallet, session:<id>, or "parent"
	Session  string          `json:"session,omitempty"`
	Note     string          `json:"note,omitempty"`
	Artifact json.RawMessage `json:"artifact,omitempty"`
}

// ErrNoTask / ErrNoProject / ErrNoFlow: the missing row.
var (
	ErrNoTask    = errors.New("parent: no such task")
	ErrNoProject = errors.New("parent: no such project")
	ErrNoFlow    = errors.New("parent: no such flow")
)

const taskSchema = `
CREATE TABLE IF NOT EXISTS flows (
	id      TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc     BLOB NOT NULL,
	created INTEGER NOT NULL,
	PRIMARY KEY (id, version)
);
CREATE TABLE IF NOT EXISTS projects (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	owner        TEXT NOT NULL,
	flow         TEXT NOT NULL,
	flow_version INTEGER NOT NULL,
	created      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS goals (
	project  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	id       TEXT NOT NULL,
	text     TEXT NOT NULL,
	position INTEGER NOT NULL,
	PRIMARY KEY (project, id)
);
CREATE TABLE IF NOT EXISTS tasks (
	id           TEXT PRIMARY KEY,
	project      TEXT NOT NULL REFERENCES projects(id),
	owner        TEXT NOT NULL,
	title        TEXT NOT NULL,
	description  TEXT NOT NULL DEFAULT '',
	goal         TEXT NOT NULL DEFAULT '',
	why          TEXT NOT NULL DEFAULT '',
	parent       TEXT NOT NULL DEFAULT '',
	origin       TEXT NOT NULL DEFAULT '',
	repo         TEXT NOT NULL DEFAULT '',
	ref          TEXT NOT NULL DEFAULT '',
	flow         TEXT NOT NULL,
	flow_version INTEGER NOT NULL,
	state        TEXT NOT NULL,
	retries      INTEGER NOT NULL DEFAULT 0,
	error        TEXT NOT NULL DEFAULT '',
	session      TEXT NOT NULL DEFAULT '',
	created      INTEGER NOT NULL,
	updated      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_project ON tasks(project, updated DESC);
CREATE INDEX IF NOT EXISTS tasks_parent ON tasks(parent);
CREATE INDEX IF NOT EXISTS tasks_session ON tasks(session);
CREATE INDEX IF NOT EXISTS tasks_state ON tasks(flow, flow_version, state);
CREATE TABLE IF NOT EXISTS task_log (
	task       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	seq        INTEGER NOT NULL,
	at         INTEGER NOT NULL,
	from_state TEXT NOT NULL,
	to_state   TEXT NOT NULL,
	actor      TEXT NOT NULL,
	session    TEXT NOT NULL DEFAULT '',
	note       TEXT NOT NULL DEFAULT '',
	artifact   BLOB,
	PRIMARY KEY (task, seq)
);
`

// migrateSessions brings a sessions table from before the task layer
// up: its `task` column held the child's run id and becomes `run`, so
// that `task` can point at the task the session serves.
func (s *Store) migrateSessions() error {
	cols := map[string]bool{}
	rows, err := s.db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	if cols["task"] && !cols["run"] {
		if _, err := s.db.Exec(`ALTER TABLE sessions RENAME COLUMN task TO run`); err != nil {
			return fmt.Errorf("rename sessions.task: %w", err)
		}
		cols["run"], cols["task"] = true, false
	}
	if !cols["task"] {
		if _, err := s.db.Exec(`ALTER TABLE sessions ADD COLUMN task TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add sessions.task: %w", err)
		}
	}
	// sessions_task indexed the old column (renamed under it); the
	// name is reused for the task pointer.
	_, err = s.db.Exec(`DROP INDEX IF EXISTS sessions_task;
		CREATE INDEX IF NOT EXISTS sessions_run ON sessions(run);
		CREATE INDEX IF NOT EXISTS sessions_task ON sessions(task)`)
	return err
}

// ---- flows ------------------------------------------------------------------

// PutFlow stores one immutable version; a (id, version) already held
// must be byte-identical or it is an error.
func (s *Store) PutFlow(f Flow) error {
	if err := f.Validate(); err != nil {
		return err
	}
	doc, err := json.Marshal(f)
	if err != nil {
		return err
	}
	var have []byte
	err = s.db.QueryRow(`SELECT doc FROM flows WHERE id = ? AND version = ?`, f.ID, f.Version).Scan(&have)
	switch {
	case err == nil:
		if string(have) != string(doc) {
			return fmt.Errorf("parent: flow %s v%d exists and differs; bump the version", f.ID, f.Version)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return err
	}
	_, err = s.db.Exec(`INSERT INTO flows (id, version, doc, created) VALUES (?, ?, ?, ?)`, f.ID, f.Version, doc, time.Now().Unix())
	return err
}

// Flow returns one version; version 0 ⇒ the latest.
func (s *Store) Flow(id string, version int) (Flow, error) {
	q := `SELECT doc FROM flows WHERE id = ?`
	args := []any{id}
	if version > 0 {
		q += ` AND version = ?`
		args = append(args, version)
	}
	var doc []byte
	err := s.db.QueryRow(q+` ORDER BY version DESC LIMIT 1`, args...).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return Flow{}, ErrNoFlow
	}
	if err != nil {
		return Flow{}, err
	}
	var f Flow
	return f, json.Unmarshal(doc, &f)
}

// Flows lists every (id, latest version).
func (s *Store) Flows() ([]Flow, error) {
	rows, err := s.db.Query(`SELECT doc FROM flows f WHERE version = (SELECT MAX(version) FROM flows WHERE id = f.id) ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flow
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var f Flow
		if err := json.Unmarshal(doc, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---- projects ---------------------------------------------------------------

// CreateProject records a project on the flow's latest version (the
// default flow when Flow is "").
func (s *Store) CreateProject(p Project) (Project, error) {
	if !ValidID(p.ID) || strings.TrimSpace(p.Name) == "" || p.Owner == "" {
		return p, errors.New("parent: project needs id, name and owner")
	}
	if p.Flow == "" {
		p.Flow = DefaultFlowID
	}
	f, err := s.Flow(p.Flow, p.FlowVersion)
	if err != nil {
		return p, err
	}
	p.FlowVersion = f.Version
	p.Owner = strings.ToLower(p.Owner)
	p.Name = strings.TrimSpace(p.Name)
	if p.Created == 0 {
		p.Created = time.Now().Unix()
	}
	_, err = s.db.Exec(`INSERT INTO projects (id, name, owner, flow, flow_version, created) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Owner, p.Flow, p.FlowVersion, p.Created)
	return p, err
}

const projectCols = `id, name, owner, flow, flow_version, created`

func scanProject(r interface{ Scan(...any) error }) (Project, error) {
	var p Project
	err := r.Scan(&p.ID, &p.Name, &p.Owner, &p.Flow, &p.FlowVersion, &p.Created)
	return p, err
}

// Project returns one project.
func (s *Store) Project(id string) (Project, error) {
	p, err := scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNoProject
	}
	return p, err
}

// Projects lists the projects v may see: its own, or all for admins.
func (s *Store) Projects(v Viewer) ([]Project, error) {
	q := `SELECT ` + projectCols + ` FROM projects`
	var args []any
	if !v.Admin() {
		q += ` WHERE owner = ?`
		args = append(args, strings.ToLower(v.Wallet))
	}
	rows, err := s.db.Query(q+` ORDER BY created`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetProjectFlow moves a project to flow/version (version 0 ⇒ the
// flow's latest; flow "" keeps the project's) for the tasks filed from
// now on; tasks already filed keep the version they were filed under
// (engine.go reads the task's). This is how a project picks up a
// bumped DefaultFlow — CreateProject pins the version current then.
func (s *Store) SetProjectFlow(id, flow string, version int) error {
	if flow == "" {
		p, err := s.Project(id)
		if err != nil {
			return err
		}
		flow = p.Flow
	}
	f, err := s.Flow(flow, version)
	if err != nil {
		return err
	}
	return s.exec1(`UPDATE projects SET flow = ?, flow_version = ? WHERE id = ?`, f.ID, f.Version, id)
}

// PutGoals replaces the project's goals, in order. Ids must be
// unique and non-empty.
func (s *Store) PutGoals(project string, goals []Goal) error {
	if _, err := s.Project(project); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, g := range goals {
		if !ValidID(g.ID) || strings.TrimSpace(g.Text) == "" || seen[g.ID] {
			return fmt.Errorf("parent: goal needs a unique id and text (%q)", g.ID)
		}
		seen[g.ID] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM goals WHERE project = ?`, project); err != nil {
		return err
	}
	for i, g := range goals {
		if _, err := tx.Exec(`INSERT INTO goals (project, id, text, position) VALUES (?, ?, ?, ?)`, project, g.ID, strings.TrimSpace(g.Text), i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Goals lists the project's goals in order.
func (s *Store) Goals(project string) ([]Goal, error) {
	rows, err := s.db.Query(`SELECT id, text FROM goals WHERE project = ? ORDER BY position`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Goal
	for rows.Next() {
		var g Goal
		if err := rows.Scan(&g.ID, &g.Text); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ---- tasks ------------------------------------------------------------------

// Filed is a task to insert with a transition (Move) or on its own
// (InsertTask): the task, who filed it, and the note on its first
// log line.
type Filed struct {
	Task Task
	By   string
	Note string
}

// InsertTask records a task in its given state, with its first log
// line ("" → state by `by`). Flow and FlowVersion default to the
// project's.
func (s *Store) InsertTask(t Task, by, note string) (Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	t, err = s.insertTask(tx, Filed{Task: t, By: by, Note: note})
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

func (s *Store) insertTask(tx *sql.Tx, f Filed) (Task, error) {
	t := f.Task
	if !ValidID(t.ID) || t.Project == "" || t.Owner == "" || strings.TrimSpace(t.Title) == "" || t.State == "" {
		return t, errors.New("parent: task needs id, project, owner, title and state")
	}
	if t.Flow == "" {
		p, err := scanProject(tx.QueryRow(`SELECT `+projectCols+` FROM projects WHERE id = ?`, t.Project))
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrNoProject
		}
		if err != nil {
			return t, err
		}
		t.Flow, t.FlowVersion = p.Flow, p.FlowVersion
	}
	t.Owner = strings.ToLower(t.Owner)
	t.Title = strings.TrimSpace(t.Title)
	now := time.Now().Unix()
	if t.Created == 0 {
		t.Created = now
	}
	t.Updated = now
	t.Retries, t.Error, t.Session = 0, "", ""
	if _, err := tx.Exec(`INSERT INTO tasks (id, project, owner, title, description, goal, why, parent, origin, repo, ref, flow, flow_version, state, retries, error, session, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', '', ?, ?)`,
		t.ID, t.Project, t.Owner, t.Title, t.Description, t.Goal, t.Why, t.Parent, t.Origin, t.Repo, t.Ref, t.Flow, t.FlowVersion, t.State, t.Created, t.Updated); err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return t, ErrNoProject
		}
		return t, err
	}
	if _, err := tx.Exec(`INSERT INTO task_log (task, seq, at, from_state, to_state, actor, note) VALUES (?, 1, ?, '', ?, ?, ?)`,
		t.ID, now, t.State, f.By, f.Note); err != nil {
		return t, err
	}
	return t, nil
}

const taskCols = `id, project, owner, title, description, goal, why, parent, origin, repo, ref, flow, flow_version, state, retries, error, session, created, updated`

func scanTask(r interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := r.Scan(&t.ID, &t.Project, &t.Owner, &t.Title, &t.Description, &t.Goal, &t.Why, &t.Parent, &t.Origin, &t.Repo, &t.Ref,
		&t.Flow, &t.FlowVersion, &t.State, &t.Retries, &t.Error, &t.Session, &t.Created, &t.Updated)
	return t, err
}

func (s *Store) queryTasks(q string, args ...any) ([]Task, error) {
	rows, err := s.db.Query(`SELECT `+taskCols+` FROM tasks `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Task returns one task.
func (s *Store) Task(id string) (Task, error) {
	t, err := scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNoTask
	}
	return t, err
}

// TaskBySession returns the task a session serves, ErrNoTask when the
// session is not a task's.
func (s *Store) TaskBySession(session string) (Task, error) {
	if session == "" {
		return Task{}, ErrNoTask
	}
	t, err := scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = (SELECT task FROM sessions WHERE id = ?)`, session))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNoTask
	}
	return t, err
}

// Tasks lists the tasks v may see (its projects' and its own; all for
// admins), newest change first; project "" ⇒ every project.
func (s *Store) Tasks(v Viewer, project string, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 500
	}
	where, args := "1=1", []any{}
	if !v.Admin() {
		w := strings.ToLower(v.Wallet)
		where = `(owner = ? OR project IN (SELECT id FROM projects WHERE owner = ?))`
		args = append(args, w, w)
	}
	if project != "" {
		where += ` AND project = ?`
		args = append(args, project)
	}
	return s.queryTasks(`WHERE `+where+` ORDER BY updated DESC LIMIT ?`, append(args, limit)...)
}

// BoardTasks is the board's read of one project: the tasks v may see,
// at most perState per state (newest change first), and how many each
// state holds. A cap per state rather than overall keeps a column that
// only grows (done, rejected) from pushing the live ones off the board,
// and the counts say what the cap left out.
func (s *Store) BoardTasks(v Viewer, project string, perState int) ([]Task, map[string]int, error) {
	where, args := `project = ?`, []any{project}
	if !v.Admin() {
		w := strings.ToLower(v.Wallet)
		where += ` AND (owner = ? OR project IN (SELECT id FROM projects WHERE owner = ?))`
		args = append(args, w, w)
	}
	ts, err := s.queryTasks(`WHERE id IN (SELECT id FROM (
		SELECT id, ROW_NUMBER() OVER (PARTITION BY state ORDER BY updated DESC, id) AS n FROM tasks WHERE `+where+`
	) WHERE n <= ?) ORDER BY updated DESC, id`, append(append([]any{}, args...), perState)...)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.Query(`SELECT state, COUNT(*) FROM tasks WHERE `+where+` GROUP BY state`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, nil, err
		}
		counts[st] = n
	}
	return ts, counts, rows.Err()
}

// TasksInStates lists every task sitting in a state match picks, by
// walking the flows once and asking for those (flow, version, state)
// triples — what the beat reconciles.
func (s *Store) TasksInStates(match func(flow Flow, state string) bool) ([]Task, error) {
	rows, err := s.db.Query(`SELECT doc FROM flows WHERE (id, version) IN (SELECT DISTINCT flow, flow_version FROM tasks)`)
	if err != nil {
		return nil, err
	}
	var flows []Flow
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			rows.Close()
			return nil, err
		}
		var f Flow
		if err := json.Unmarshal(doc, &f); err != nil {
			rows.Close()
			return nil, err
		}
		flows = append(flows, f)
	}
	rows.Close()
	var out []Task
	for _, f := range flows {
		var states []string
		for name := range f.States {
			if match(f, name) {
				states = append(states, name)
			}
		}
		if len(states) == 0 {
			continue
		}
		sort.Strings(states)
		marks := strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")
		args := []any{f.ID, f.Version}
		for _, st := range states {
			args = append(args, st)
		}
		ts, err := s.queryTasks(`WHERE flow = ? AND flow_version = ? AND state IN (`+marks+`) ORDER BY updated`, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, ts...)
	}
	return out, nil
}

// Children lists the tasks carved out of parent.
func (s *Store) ChildTasks(parent string) ([]Task, error) {
	return s.queryTasks(`WHERE parent = ? ORDER BY created`, parent)
}

// CanTask reports whether v may act on the task: its owner, the
// project's owner, or an admin. (One right for now: a task's acts are
// the project owner's — spike y8m0 leaves finer rights to real use.)
func (s *Store) CanTask(t Task, v Viewer) bool {
	if v.Admin() || strings.EqualFold(t.Owner, v.Wallet) {
		return true
	}
	p, err := s.Project(t.Project)
	return err == nil && strings.EqualFold(p.Owner, v.Wallet)
}

// Move applies a transition: the task leaves its state for to, its
// retries reset, its session cleared, the rewrite applied, the log
// line appended, and the tasks the transition files (children,
// follow-ups) inserted — all in one transaction, so a crash leaves
// either the whole move or none of it. The task must still be in from
// (a concurrent move loses). Returns the moved task and the filed ones.
func (s *Store) Move(id, from, to, by, session string, tr Transition, file ...Filed) (Task, []Task, error) {
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, nil, err
	}
	defer tx.Rollback()
	sets, args := []string{"state = ?", "retries = 0", "error = ''", "session = ''", "updated = ?"}, []any{to, now}
	if tr.Set != nil {
		for col, v := range map[string]*string{"title": tr.Set.Title, "goal": tr.Set.Goal, "why": tr.Set.Why, "repo": tr.Set.Repo, "ref": tr.Set.Ref} {
			if v != nil && !(col == "title" && strings.TrimSpace(*v) == "") {
				sets = append(sets, col+" = ?")
				args = append(args, strings.TrimSpace(*v))
			}
		}
	}
	res, err := tx.Exec(`UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ? AND state = ?`, append(args, id, from)...)
	if err != nil {
		return Task{}, nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Task{}, nil, fmt.Errorf("parent: task %s is no longer in %s", id, from)
	}
	var art []byte
	if len(tr.Artifact) > 0 && string(tr.Artifact) != "null" {
		art = []byte(tr.Artifact)
	}
	if _, err := tx.Exec(`INSERT INTO task_log (task, seq, at, from_state, to_state, actor, session, note, artifact)
		VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM task_log WHERE task = ?), ?, ?, ?, ?, ?, ?, ?)`,
		id, id, now, from, to, by, session, strings.TrimSpace(tr.Note), art); err != nil {
		return Task{}, nil, err
	}
	var filed []Task
	for _, f := range file {
		ft, err := s.insertTask(tx, f)
		if err != nil {
			return Task{}, nil, err
		}
		filed = append(filed, ft)
	}
	t, err := scanTask(tx.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
	if err != nil {
		return Task{}, nil, err
	}
	return t, filed, tx.Commit()
}

// Attach binds a session to the task's current attempt; a session
// row gets its task set too.
func (s *Store) Attach(task, session string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tasks SET session = ?, updated = ? WHERE id = ?`, session, time.Now().Unix(), task); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET task = ? WHERE id = ?`, task, session); err != nil {
		return err
	}
	return tx.Commit()
}

// Retry counts one failed attempt with its reason and clears the
// session; returns the new count.
func (s *Store) Retry(task, reason string) (int, error) {
	if err := s.exec1(`UPDATE tasks SET retries = retries + 1, error = ?, session = '', updated = ? WHERE id = ?`, reason, time.Now().Unix(), task); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRow(`SELECT retries FROM tasks WHERE id = ?`, task).Scan(&n)
	return n, err
}

// Stall records why a task sits where it is with no session and no
// move to take (its state has no exit to the stuck state): the error
// shows on the board; retries are not counted.
func (s *Store) Stall(task, reason string) error {
	return s.exec1(`UPDATE tasks SET error = ?, session = '', updated = ? WHERE id = ?`, reason, time.Now().Unix(), task)
}

// Log returns the task's transitions in order.
func (s *Store) Log(task string) ([]LogEntry, error) {
	rows, err := s.db.Query(`SELECT seq, at, from_state, to_state, actor, session, note, artifact FROM task_log WHERE task = ? ORDER BY seq`, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		e := LogEntry{Task: task}
		var art []byte
		if err := rows.Scan(&e.Seq, &e.At, &e.From, &e.To, &e.By, &e.Session, &e.Note, &art); err != nil {
			return nil, err
		}
		if len(art) > 0 {
			e.Artifact = json.RawMessage(art)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RunEnd is the newest assistant message on a session's stream: its
// text (where an agent's transition block sits) and how the model
// call stopped — StopReason "error" with Error set when the provider
// refused it (pi's errorMessage, e.g. `402 {...billing_error...}`).
type RunEnd struct {
	Text       string
	StopReason string
	Error      string
}

// Failed reports a run whose last model call errored rather than
// answered.
func (e RunEnd) Failed() bool { return e.StopReason == "error" }

// LastAssistant reads the newest assistant message on a session's
// stream; a zero RunEnd when there is none.
func (s *Store) LastAssistant(session string) (RunEnd, error) {
	rows, err := s.db.Query(`SELECT raw FROM events WHERE session = ? AND type = 'message_end' ORDER BY seq DESC LIMIT 50`, session)
	if err != nil {
		return RunEnd{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return RunEnd{}, err
		}
		var m struct {
			Message struct {
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				StopReason string          `json:"stopReason"`
				Error      string          `json:"errorMessage"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Message.Role != "assistant" {
			continue
		}
		return RunEnd{Text: textParts(m.Message.Content), StopReason: m.Message.StopReason, Error: m.Message.Error}, nil
	}
	return RunEnd{}, rows.Err()
}

// textParts flattens only the text parts of pi's message content — a
// transition fence inside a tool call's arguments is not the agent's
// word.
func textParts(content json.RawMessage) string {
	var str string
	if json.Unmarshal(content, &str) == nil {
		return str
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			sb.WriteString(p.Text)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// SessionsOfTask lists the sessions that served the task, oldest
// first: one per attempt at an agent state.
func (s *Store) SessionsOfTask(task string) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions WHERE task = ? ORDER BY created`, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
