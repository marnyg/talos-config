// Package parent is the long-lived parent actor of ADR-0037: the agent
// backend. It spawns agents as children (protocol/spawn), is the sink
// of each child's event stream (child.FacetAgentEvents), keeps every
// run as a Session in its own store, and serves the list/watch/steer/
// search surface behind the person gate. This file is the store.
//
// The store is the one durable thing the parent owns besides its key:
// SQLite (modernc.org/sqlite, pure Go — the package stays C-free) with
// an FTS5 index over the transcript text, under a directory the
// manifest mounts. Root invariant 2's data-plane reading applies:
// sessions and share lists are bookkeeping about payload the parent
// holds, replicated and backed up like any volume (bsj), never
// re-derived from git. Two tables are caches of actor state rather
// than payload — children (the spawner's born table, spawn.Adopt) and
// consents (what this key has issued) — so a restarted process picks
// its children up where it left them; losing either refuses, never
// grants (ADR-0019's safe-to-lose test).
package parent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/spawn"

	_ "modernc.org/sqlite"
)

// Session statuses. A session is created pending (the #spawn is out),
// runs once the child acked #task, and ends in whatever status the
// child's child_settled line carries (child.Settled: settled, aborted,
// failed) — or lost, when the parent gave up on it without one (the
// child lapsed or the birth never came).
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusSettled = "settled"
	StatusAborted = "aborted"
	StatusFailed  = "failed"
	StatusLost    = "lost"
)

// Share rights (ADR-0037 "Who may see and steer"): view is the live
// tail, transcript and search; steer adds #steer/#abort and implies
// view. The owner holds both; admins hold view on everything.
const (
	RightView  = "view"
	RightSteer = "steer"
)

// Session is one agent run as the parent keeps it.
type Session struct {
	ID     string       `json:"id"`
	Owner  string       `json:"owner"` // the submitting wallet, lower-case 0x…
	Prompt string       `json:"prompt"`
	Repo   string       `json:"repo,omitempty"`
	Ref    string       `json:"ref,omitempty"`
	Title  string       `json:"title,omitempty"`
	Image  string       `json:"image"`
	Status string       `json:"status"`
	Child  cert.ActorID `json:"child,omitempty"`
	Lease  string       `json:"lease,omitempty"`
	// Task is the id the child minted for the run (#task's ack; the
	// stream's path) — set when the ack lands or the stream opens,
	// whichever is first.
	Task    string `json:"task,omitempty"`
	Created int64  `json:"created"`
	Started int64  `json:"started,omitempty"`
	Settled int64  `json:"settled,omitempty"`
	Events  int    `json:"events"`
	Reason  string `json:"reason,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head,omitempty"`
	PR      string `json:"pr,omitempty"`
	// File is set once the child shipped its session JSONL (what pi's
	// --session/--fork resume from): the path under the store's dir.
	File string `json:"file,omitempty"`
}

// Live reports whether the session may still produce events.
func (s Session) Live() bool { return s.Status == StatusPending || s.Status == StatusRunning }

// Event is one line of a session's stream: a pirpc record the child
// relayed, the child_settled line, or something the parent stamped in
// itself (a steer, with the sender's wallet — ADR-0037).
type Event struct {
	Session string          `json:"session"`
	Seq     int64           `json:"seq"`
	At      int64           `json:"at"`
	Type    string          `json:"type"`
	From    string          `json:"from,omitempty"` // the wallet behind a steer/abort; "" for the child's own
	Raw     json.RawMessage `json:"raw"`
}

// Share is one entry of a session's share list: a wallet (0x…) or a
// group (group:<name>) at one right.
type Share struct {
	Principal string `json:"principal"`
	Right     string `json:"right"`
}

// Viewer is who is asking: the wallet behind the person gate, the
// groups siwe-oidc put in its token, and whether admins is one of them.
type Viewer struct {
	Wallet string
	Groups []string
}

// Admin reports membership of the admins group.
func (v Viewer) Admin() bool {
	for _, g := range v.Groups {
		if g == "admins" {
			return true
		}
	}
	return false
}

// principals is every share-list key that names v.
func (v Viewer) principals() []string {
	out := []string{strings.ToLower(v.Wallet)}
	for _, g := range v.Groups {
		out = append(out, "group:"+g)
	}
	return out
}

// Hit is one search result.
type Hit struct {
	Session string `json:"session"`
	Seq     int64  `json:"seq"`
	Snippet string `json:"snippet"`
}

// ErrNotFound is the missing session.
var ErrNotFound = errors.New("parent: no such session")

// Store is the parent's durable state. One process opens it (the PVC
// is RWO and the Deployment Recreate); one connection serves it, which
// is SQLite's own writer model and keeps the FTS and seq bookkeeping
// trivially serial.
type Store struct {
	dir string
	db  *sql.DB

	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

// Open opens (creating) the store under dir: parent.db and sessions/.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "parent.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{dir: dir, db: db, subs: make(map[string]map[chan Event]struct{})}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id       TEXT PRIMARY KEY,
	owner    TEXT NOT NULL,
	prompt   TEXT NOT NULL,
	repo     TEXT NOT NULL DEFAULT '',
	ref      TEXT NOT NULL DEFAULT '',
	title    TEXT NOT NULL DEFAULT '',
	image    TEXT NOT NULL DEFAULT '',
	status   TEXT NOT NULL,
	child    TEXT NOT NULL DEFAULT '',
	lease    TEXT NOT NULL DEFAULT '',
	task     TEXT NOT NULL DEFAULT '',
	created  INTEGER NOT NULL,
	started  INTEGER NOT NULL DEFAULT 0,
	settled  INTEGER NOT NULL DEFAULT 0,
	events   INTEGER NOT NULL DEFAULT 0,
	reason   TEXT NOT NULL DEFAULT '',
	branch   TEXT NOT NULL DEFAULT '',
	head     TEXT NOT NULL DEFAULT '',
	pr       TEXT NOT NULL DEFAULT '',
	file     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_owner ON sessions(owner, created DESC);
CREATE INDEX IF NOT EXISTS sessions_child ON sessions(child);
CREATE INDEX IF NOT EXISTS sessions_task ON sessions(task);
CREATE TABLE IF NOT EXISTS events (
	session  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq      INTEGER NOT NULL,
	at       INTEGER NOT NULL,
	type     TEXT NOT NULL,
	sender   TEXT NOT NULL DEFAULT '',
	raw      BLOB NOT NULL,
	PRIMARY KEY (session, seq)
);
CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
	text, session UNINDEXED, seq UNINDEXED, tokenize = 'porter unicode61'
);
CREATE TABLE IF NOT EXISTS shares (
	session   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	principal TEXT NOT NULL,
	right     TEXT NOT NULL,
	PRIMARY KEY (session, principal)
);
CREATE TABLE IF NOT EXISTS children (
	id          TEXT PRIMARY KEY,
	provisioner TEXT NOT NULL,
	lease       TEXT NOT NULL,
	until       INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS consents (
	sig  BLOB PRIMARY KEY,
	exp  INTEGER NOT NULL,
	cert BLOB NOT NULL
);
`

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

// ---- sessions ---------------------------------------------------------------

var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidID reports whether id is a session id this store accepts (the
// task-id shape the child puts on the wire).
func ValidID(id string) bool { return sessionID.MatchString(id) }

// Create records a new session. ID, Owner and Prompt are required;
// Status defaults to pending and Created to now.
func (s *Store) Create(sess Session) error {
	if !ValidID(sess.ID) || sess.Owner == "" || sess.Prompt == "" {
		return errors.New("parent: session needs id, owner and prompt")
	}
	if sess.Status == "" {
		sess.Status = StatusPending
	}
	if sess.Created == 0 {
		sess.Created = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO sessions (id, owner, prompt, repo, ref, title, image, status, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, strings.ToLower(sess.Owner), sess.Prompt, sess.Repo, sess.Ref, sess.Title, sess.Image, sess.Status, sess.Created)
	return err
}

const sessionCols = `id, owner, prompt, repo, ref, title, image, status, child, lease, task, created, started, settled, events, reason, branch, head, pr, file`

func scanSession(r interface{ Scan(...any) error }) (Session, error) {
	var x Session
	var child string
	err := r.Scan(&x.ID, &x.Owner, &x.Prompt, &x.Repo, &x.Ref, &x.Title, &x.Image, &x.Status, &child, &x.Lease, &x.Task,
		&x.Created, &x.Started, &x.Settled, &x.Events, &x.Reason, &x.Branch, &x.Head, &x.PR, &x.File)
	x.Child = cert.ActorID(child)
	return x, err
}

// Get returns one session; ErrNotFound when absent.
func (s *Store) Get(id string) (Session, error) {
	x, err := scanSession(s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return x, err
}

// ByChild returns the newest session a child ran — only a live one
// when live is set.
func (s *Store) ByChild(child cert.ActorID, live bool) (Session, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions WHERE child = ?`
	args := []any{string(child)}
	if live {
		q += ` AND status IN (?, ?)`
		args = append(args, StatusPending, StatusRunning)
	}
	x, err := scanSession(s.db.QueryRow(q+` ORDER BY created DESC LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return x, err
}

// ByTask returns the session the child's task id names.
func (s *Store) ByTask(task string) (Session, error) {
	if task == "" {
		return Session{}, ErrNotFound
	}
	x, err := scanSession(s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE task = ? LIMIT 1`, task))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return x, err
}

// SetTask records the child's task id on the session (first writer
// wins; a later different id is an error).
func (s *Store) SetTask(id, task string) error {
	x, err := s.Get(id)
	if err != nil {
		return err
	}
	switch x.Task {
	case task:
		return nil
	case "":
		return s.exec1(`UPDATE sessions SET task = ? WHERE id = ? AND task = ''`, task, id)
	}
	return fmt.Errorf("parent: session %s runs task %s, not %s", id, x.Task, task)
}

// Started moves a pending session to running on the born child.
func (s *Store) Started(id string, child cert.ActorID, lease string) error {
	return s.exec1(`UPDATE sessions SET status = ?, child = ?, lease = ?, started = ? WHERE id = ?`,
		StatusRunning, string(child), lease, time.Now().Unix(), id)
}

// Settle ends a session: the status, reason and result pointer of the
// child's last line (or lost, by the parent). Idempotent on a session
// already ended: the first word stands.
func (s *Store) Settle(id, status, reason string, branch, head, pr string) error {
	res, err := s.db.Exec(`UPDATE sessions SET status = ?, reason = ?, branch = ?, head = ?, pr = ?, settled = ?,
		events = (SELECT COUNT(*) FROM events WHERE session = sessions.id)
		WHERE id = ? AND status IN (?, ?)`,
		status, reason, branch, head, pr, time.Now().Unix(), id, StatusPending, StatusRunning)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.Get(id); err != nil {
			return err
		}
	}
	return nil
}

// List returns the sessions v may view, newest first, at most limit.
func (s *Store) List(v Viewer, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 100
	}
	where, args := s.visible(v)
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions WHERE `+where+` ORDER BY created DESC LIMIT ?`, append(args, limit)...)
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

// visible is the SQL predicate (over the sessions table, aliased
// nothing) for "v may view this session", with its arguments.
func (s *Store) visible(v Viewer) (string, []any) {
	if v.Admin() {
		return "1=1", nil
	}
	ps := v.principals()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ps)), ",")
	args := []any{ps[0]}
	for _, p := range ps {
		args = append(args, p)
	}
	return `(owner = ? OR EXISTS (SELECT 1 FROM shares WHERE shares.session = sessions.id AND shares.principal IN (` + marks + `)))`, args
}

// Can reports whether v holds right on the session: the owner holds
// every right, admins view, a share grants its right (steer implies
// view). ErrNotFound for a missing session.
func (s *Store) Can(id string, v Viewer, right string) (bool, error) {
	x, err := s.Get(id)
	if err != nil {
		return false, err
	}
	if strings.EqualFold(x.Owner, v.Wallet) {
		return true, nil
	}
	if right == RightView && v.Admin() {
		return true, nil
	}
	shares, err := s.Shares(id)
	if err != nil {
		return false, err
	}
	for _, sh := range shares {
		for _, p := range v.principals() {
			if sh.Principal == p && (sh.Right == right || sh.Right == RightSteer) {
				return true, nil
			}
		}
	}
	return false, nil
}

// ---- shares -----------------------------------------------------------------

// Share grants principal (0x… wallet or group:<name>) right on the
// session, replacing an earlier entry for the same principal.
func (s *Store) Share(id, principal, right string) error {
	principal = normPrincipal(principal)
	if principal == "" || (right != RightView && right != RightSteer) {
		return errors.New("parent: share needs a wallet or group:<name> and a right of view|steer")
	}
	if _, err := s.Get(id); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO shares (session, principal, right) VALUES (?, ?, ?)
		ON CONFLICT(session, principal) DO UPDATE SET right = excluded.right`, id, principal, right)
	return err
}

// Unshare drops principal from the session's share list.
func (s *Store) Unshare(id, principal string) error {
	_, err := s.db.Exec(`DELETE FROM shares WHERE session = ? AND principal = ?`, id, normPrincipal(principal))
	return err
}

// Shares lists the session's share list.
func (s *Store) Shares(id string) ([]Share, error) {
	rows, err := s.db.Query(`SELECT principal, right FROM shares WHERE session = ? ORDER BY principal`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Share
	for rows.Next() {
		var sh Share
		if err := rows.Scan(&sh.Principal, &sh.Right); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

var wallet = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
var group = regexp.MustCompile(`^group:[A-Za-z0-9_-]{1,64}$`)

func normPrincipal(p string) string {
	p = strings.TrimSpace(p)
	switch {
	case wallet.MatchString(p):
		return strings.ToLower(p)
	case strings.HasPrefix(p, "0X") && wallet.MatchString("0x"+p[2:]):
		return strings.ToLower(p)
	case group.MatchString(p):
		return p
	}
	return ""
}

// ---- events -----------------------------------------------------------------

// Append adds one line to the session's stream, indexes its text and
// wakes watchers. typ is read off raw when empty. Returns the line's
// seq.
func (s *Store) Append(id string, typ, from string, raw json.RawMessage) (Event, error) {
	if typ == "" {
		var t struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &t)
		typ = t.Type
	}
	ev := Event{Session: id, At: time.Now().Unix(), Type: typ, From: from, Raw: raw}
	tx, err := s.db.Begin()
	if err != nil {
		return ev, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE session = ?`, id).Scan(&ev.Seq); err != nil {
		return ev, err
	}
	if _, err := tx.Exec(`INSERT INTO events (session, seq, at, type, sender, raw) VALUES (?, ?, ?, ?, ?, ?)`,
		id, ev.Seq, ev.At, ev.Type, ev.From, []byte(raw)); err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return ev, ErrNotFound
		}
		return ev, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET events = ? WHERE id = ?`, ev.Seq, id); err != nil {
		return ev, err
	}
	if text := IndexText(ev.Type, raw); text != "" {
		if _, err := tx.Exec(`INSERT INTO events_fts (text, session, seq) VALUES (?, ?, ?)`, text, id, ev.Seq); err != nil {
			return ev, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ev, err
	}
	s.notify(ev)
	return ev, nil
}

// ChildLines counts the lines the child itself sent (sender ""), what a
// resuming upload continues from.
func (s *Store) ChildLines(id string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE session = ? AND sender = ''`, id).Scan(&n)
	return n, err
}

// Events returns the session's lines with seq > after, in order, at
// most limit (0 ⇒ 1000).
func (s *Store) Events(id string, after int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT seq, at, type, sender, raw FROM events WHERE session = ? AND seq > ? ORDER BY seq LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev := Event{Session: id}
		var raw []byte
		if err := rows.Scan(&ev.Seq, &ev.At, &ev.Type, &ev.From, &raw); err != nil {
			return nil, err
		}
		ev.Raw = json.RawMessage(raw)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Subscribe delivers every line appended to the session from now on,
// until cancel. The channel is buffered; a watcher that falls more than
// its buffer behind is dropped (the channel is closed) and should
// resume from Events(after: last seen) — the store never blocks on a
// slow reader.
func (s *Store) Subscribe(id string) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	s.mu.Lock()
	if s.subs[id] == nil {
		s.subs[id] = make(map[chan Event]struct{})
	}
	s.subs[id][ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			if set, ok := s.subs[id]; ok {
				if _, ok := set[ch]; ok {
					delete(set, ch)
					close(ch)
				}
				if len(set) == 0 {
					delete(s.subs, id)
				}
			}
			s.mu.Unlock()
		})
	}
	return ch, cancel
}

func (s *Store) notify(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.subs[ev.Session]
	for ch := range set {
		select {
		case ch <- ev:
		default:
			delete(set, ch)
			close(ch)
		}
	}
	if len(set) == 0 {
		delete(s.subs, ev.Session)
	}
}

// Search runs an FTS5 query over the transcripts v may view, newest
// sessions first.
func (s *Store) Search(v Viewer, query string, limit int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	where, args := s.visible(v)
	q := `SELECT f.session, f.seq, snippet(events_fts, 0, '[', ']', '…', 12)
		FROM events_fts f JOIN sessions ON sessions.id = f.session
		WHERE events_fts MATCH ? AND ` + where + `
		ORDER BY sessions.created DESC, rank LIMIT ?`
	rows, err := s.db.Query(q, append(append([]any{ftsQuery(query)}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Session, &h.Seq, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsQuery turns free text into a safe FTS5 query: each word a quoted
// prefix term, ANDed. FTS5's own operators are deliberately not
// exposed — a stray quote or colon is a syntax error there.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.Fields(q) {
		w = strings.ReplaceAll(w, `"`, `""`)
		terms = append(terms, `"`+w+`"*`)
	}
	return strings.Join(terms, " ")
}

// IndexText is the FTS policy: what of a stream line is searchable.
// Finished messages (message_end: the user's prompt, the assistant's
// text and tool calls), finished tool results (tool_execution_end),
// steers, and the settled line's reason. Deltas are not indexed —
// their message_end carries the same text once.
func IndexText(typ string, raw json.RawMessage) string {
	var sb strings.Builder
	switch typ {
	case "message_end":
		var m struct {
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &m) != nil {
			return ""
		}
		contentText(&sb, m.Message.Content)
	case "tool_execution_end":
		var m struct {
			ToolName string `json:"toolName"`
			Result   struct {
				Content json.RawMessage `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &m) != nil {
			return ""
		}
		sb.WriteString(m.ToolName)
		sb.WriteByte(' ')
		contentText(&sb, m.Result.Content)
	case "steer", "child_settled":
		var m struct {
			Message string `json:"message"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &m)
		sb.WriteString(m.Message)
		sb.WriteString(m.Reason)
	}
	return strings.TrimSpace(sb.String())
}

// contentText flattens pi's message content: a string, or an array of
// parts {type: text|thinking|toolCall|toolResult, ...}.
func contentText(sb *strings.Builder, content json.RawMessage) {
	var str string
	if json.Unmarshal(content, &str) == nil {
		sb.WriteString(str)
		return
	}
	var parts []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Thinking  string          `json:"thinking"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return
	}
	for _, p := range parts {
		switch p.Type {
		case "text":
			sb.WriteString(p.Text)
		case "thinking":
			sb.WriteString(p.Thinking)
		case "toolCall":
			sb.WriteString(p.Name)
			sb.WriteByte(' ')
			sb.Write(p.Arguments)
		case "toolResult":
			contentText(sb, p.Content)
		}
		sb.WriteByte('\n')
	}
}

// ---- session files ----------------------------------------------------------

// PutFile stores the session JSONL the child shipped at settle and
// records its path on the session.
func (s *Store) PutFile(id string, r io.Reader) (int64, error) {
	if !ValidID(id) {
		return 0, ErrNotFound
	}
	if _, err := s.Get(id); err != nil {
		return 0, err
	}
	path := filepath.Join(s.dir, "sessions", id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	return n, s.exec1(`UPDATE sessions SET file = ? WHERE id = ?`, path, id)
}

// ---- actor-state caches -----------------------------------------------------

// PutChildren replaces the children cache with the spawner's current
// born table (spawn.Spawner.Children), on the owner's beat.
func (s *Store) PutChildren(cs []spawn.Adopted) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM children`); err != nil {
		return err
	}
	for _, c := range cs {
		if _, err := tx.Exec(`INSERT INTO children (id, provisioner, lease, until) VALUES (?, ?, ?, ?)`,
			string(c.ID), string(c.Lease.Provisioner), c.Lease.ID, c.Until); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Children returns the cached born table for spawn.Spawner.Adopt, each
// deadline raised to at least floor (the restart grace: a stale
// deadline must not sweep a child that is about to knock).
func (s *Store) Children(floor int64) ([]spawn.Adopted, error) {
	rows, err := s.db.Query(`SELECT id, provisioner, lease, until FROM children`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []spawn.Adopted
	for rows.Next() {
		var c spawn.Adopted
		var id, prov string
		if err := rows.Scan(&id, &prov, &c.Lease.ID, &c.Until); err != nil {
			return nil, err
		}
		c.ID, c.Lease.Provisioner = cert.ActorID(id), cert.ActorID(prov)
		if c.Until < floor {
			c.Until = floor
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PutConsents records the consents this actor holds (actor.Authority's
// first return), replacing the cache; expired ones are dropped.
func (s *Store) PutConsents(cs []cert.Cert, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM consents`); err != nil {
		return err
	}
	for _, c := range cs {
		if c.Exp <= now || len(c.Sig) == 0 {
			continue
		}
		raw, err := cert.Encode(c)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO consents (sig, exp, cert) VALUES (?, ?, ?)`, []byte(c.Sig), c.Exp, raw); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Consents returns the cached consents still live at now.
func (s *Store) Consents(now int64) ([]cert.Cert, error) {
	rows, err := s.db.Query(`SELECT cert FROM consents WHERE exp > ?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cert.Cert
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		c, err := cert.DecodeCert(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LoseLive marks every live session keep does not vouch for lost, with
// reason: what a restarted parent does to the runs it cannot re-adopt,
// and the beat to runs whose child lapsed. keep sees "" for a session
// with no child yet (its #spawn is in flight). Returns the ids it ended.
func (s *Store) LoseLive(reason string, keep func(child cert.ActorID) bool) ([]string, error) {
	rows, err := s.db.Query(`SELECT id, child FROM sessions WHERE status IN (?, ?)`, StatusPending, StatusRunning)
	if err != nil {
		return nil, err
	}
	var lose []string
	for rows.Next() {
		var id, child string
		if err := rows.Scan(&id, &child); err != nil {
			rows.Close()
			return nil, err
		}
		if keep != nil && keep(cert.ActorID(child)) {
			continue
		}
		lose = append(lose, id)
	}
	rows.Close()
	for _, id := range lose {
		if err := s.Settle(id, StatusLost, reason, "", "", ""); err != nil {
			return lose, err
		}
		line, _ := json.Marshal(map[string]any{"type": "child_settled", "status": StatusLost, "reason": reason, "session": id})
		if _, err := s.Append(id, "child_settled", "", line); err != nil {
			return lose, err
		}
	}
	return lose, nil
}

func (s *Store) exec1(q string, args ...any) error {
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Ping checks the database is usable (readiness).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// String is for logs.
func (s *Store) String() string { return fmt.Sprintf("parent store at %s", s.dir) }
