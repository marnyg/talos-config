package parent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/marnyg/talos-config/actors/child"
)

// The task engine (spike y8m0): tasks move through their project's
// flow by one operation, a transition, asked for by the agent running
// the state (its last message's ```transition block), by a person
// (the API, in a human state), or by the parent itself (a children
// state whose children all ended; an agent that kept failing). Entering
// an agent state spawns one session under the state's persona; the
// session ending is what moves the task on, or counts a retry.

// byParent is the actor name on transitions the engine takes itself.
const byParent = "parent"

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CreateTask files a task in project for owner at the flow's initial
// state; with start set it is moved straight on to the initial
// state's first exit (a person skipping the capture step).
func (p *Parent) CreateTask(ctx context.Context, owner, project string, spec TaskSpec, start bool) (Task, error) {
	pr, err := p.store.Project(project)
	if err != nil {
		return Task{}, err
	}
	f, err := p.store.Flow(pr.Flow, pr.FlowVersion)
	if err != nil {
		return Task{}, err
	}
	p.tmu.Lock()
	defer p.tmu.Unlock()
	t, err := p.file(owner, pr, f, spec, f.Initial, "", "", strings.ToLower(owner), "")
	if err != nil {
		return t, err
	}
	if init := f.States[f.Initial]; start && init.Driver == DriverHuman && !init.Terminal() {
		return p.apply(ctx, t, f, strings.ToLower(owner), "", Transition{To: init.Next[0], Note: "started on creation"})
	}
	p.enter(ctx, t, f)
	return t, nil
}

// file inserts one task; the caller holds tmu.
func (p *Parent) file(owner string, pr Project, f Flow, spec TaskSpec, state, parent, origin, by, note string) (Task, error) {
	fd, err := p.filed(owner, pr, f, spec, state, parent, origin, by, note)
	if err != nil {
		return Task{}, err
	}
	t, err := p.store.InsertTask(fd.Task, fd.By, fd.Note)
	if err != nil {
		return Task{}, err
	}
	p.logFiled(t, by)
	return t, nil
}

// filed shapes one task to insert (an id minted here).
func (p *Parent) filed(owner string, pr Project, f Flow, spec TaskSpec, state, parent, origin, by, note string) (Filed, error) {
	id, err := newID()
	if err != nil {
		return Filed{}, err
	}
	t := Task{ID: id, Project: pr.ID, Owner: owner, Title: spec.Title, Description: spec.Description, Goal: spec.Goal, Why: spec.Why,
		Parent: parent, Origin: origin, Repo: spec.Repo, Ref: spec.Ref, Flow: f.ID, FlowVersion: f.Version, State: state}
	return Filed{Task: t, By: by, Note: note}, nil
}

func (p *Parent) logFiled(t Task, by string) {
	p.log.Info("parent: task filed", "task", t.ID, "project", t.Project, "state", t.State, "by", by, "parent", t.Parent, "origin", t.Origin)
}

// Transition is a person asking: allowed in a human state, or in an
// agent state that has no live session (the engine gave up or has not
// started). The caller has checked CanTask.
func (p *Parent) Transition(ctx context.Context, id, wallet string, tr Transition) (Task, error) {
	p.tmu.Lock()
	defer p.tmu.Unlock()
	t, err := p.store.Task(id)
	if err != nil {
		return Task{}, err
	}
	f, err := p.store.Flow(t.Flow, t.FlowVersion)
	if err != nil {
		return Task{}, err
	}
	st := f.States[t.State]
	if st.Driver != DriverHuman && t.Session != "" {
		if sess, err := p.store.Get(t.Session); err == nil && sess.Live() {
			return Task{}, fmt.Errorf("parent: task %s is %s with session %s live; abort it first", id, t.State, t.Session)
		}
	}
	if st.Driver == DriverChildren {
		return Task{}, fmt.Errorf("parent: task %s waits for its children", id)
	}
	return p.apply(ctx, t, f, strings.ToLower(wallet), "", tr)
}

// apply validates and performs one transition, then enters the new
// state; the caller holds tmu. session is the session that asked, ""
// for a person or the parent.
func (p *Parent) apply(ctx context.Context, t Task, f Flow, by, session string, tr Transition) (Task, error) {
	from := f.States[t.State]
	if !from.Allows(tr.To) {
		return Task{}, fmt.Errorf("parent: %s → %s is not an exit of %s", t.State, tr.To, t.State)
	}
	to := f.States[tr.To]
	if to.Driver == DriverChildren && len(tr.Children) == 0 {
		return Task{}, fmt.Errorf("parent: %s needs children", tr.To)
	}
	if to.Driver != DriverChildren && len(tr.Children) > 0 {
		return Task{}, fmt.Errorf("parent: %s takes no children", tr.To)
	}
	for _, c := range append(append([]TaskSpec{}, tr.Children...), tr.Followups...) {
		if strings.TrimSpace(c.Title) == "" {
			return Task{}, errors.New("parent: every child and follow-up needs a title")
		}
	}
	if tr.Set != nil && tr.Set.Goal != nil && *tr.Set.Goal != "" {
		goals, err := p.store.Goals(t.Project)
		if err != nil {
			return Task{}, err
		}
		if goalText(goals, *tr.Set.Goal) == "" {
			return Task{}, fmt.Errorf("parent: goal %q is not one of the project's", *tr.Set.Goal)
		}
	}
	pr, err := p.store.Project(t.Project)
	if err != nil {
		return Task{}, err
	}
	// Children and follow-ups are filed in the move's own transaction:
	// a crash leaves the whole move or none of it. The goal and why
	// are inherited by a child that names none; the title stays.
	var file []Filed
	for _, c := range tr.Children {
		if c.Goal == "" {
			c.Goal, c.Why = t.Goal, t.Why
			if tr.Set != nil {
				if tr.Set.Goal != nil {
					c.Goal = *tr.Set.Goal
				}
				if tr.Set.Why != nil {
					c.Why = *tr.Set.Why
				}
			}
		}
		fd, err := p.filed(t.Owner, pr, f, c, f.Leaf, t.ID, "", by, "carved out of "+t.Title)
		if err != nil {
			return Task{}, err
		}
		file = append(file, fd)
	}
	for _, c := range tr.Followups {
		fd, err := p.filed(t.Owner, pr, f, c, f.Initial, "", t.ID, by, "follow-up of "+t.Title)
		if err != nil {
			return Task{}, err
		}
		file = append(file, fd)
	}
	moved, filed, err := p.store.Move(t.ID, t.State, tr.To, by, session, tr, file...)
	if err != nil {
		return Task{}, err
	}
	p.log.Info("parent: task moved", "task", t.ID, "from", t.State, "to", tr.To, "by", by, "note", tr.Note)
	for _, ft := range filed {
		p.logFiled(ft, by)
		if ft.Parent == moved.ID {
			p.enter(ctx, ft, f)
		}
	}
	p.enter(ctx, moved, f)
	return moved, nil
}

// enter does what arriving in t.State means: an agent state spawns
// its session; a children state and a terminal state check whether a
// parent may move on. The caller holds tmu.
func (p *Parent) enter(ctx context.Context, t Task, f Flow) {
	st := f.States[t.State]
	switch {
	case st.Driver == DriverAgent:
		p.spawnFor(ctx, t, f)
	case st.Driver == DriverChildren:
		p.join(ctx, t, f)
	case st.Terminal() && t.Parent != "":
		if parent, err := p.store.Task(t.Parent); err == nil {
			if pf, err := p.store.Flow(parent.Flow, parent.FlowVersion); err == nil {
				p.join(ctx, parent, pf)
			}
		}
	}
}

// spawnFor submits the session for one attempt at t's agent state.
func (p *Parent) spawnFor(ctx context.Context, t Task, f Flow) {
	in := promptInput{Task: t, Attempt: t.Retries + 1}
	var err error
	if in.Project, err = p.store.Project(t.Project); err != nil {
		p.log.Warn("parent: task project", "task", t.ID, "err", err)
		return
	}
	in.Goals, _ = p.store.Goals(t.Project)
	if t.Parent != "" {
		if pt, err := p.store.Task(t.Parent); err == nil {
			in.Parent = &pt
		}
	}
	if log, err := p.store.Log(t.ID); err == nil && len(log) > 0 {
		last := log[len(log)-1]
		in.Last = &last
	}
	prompt := f.RenderPrompt(t.State, in)
	sess, err := p.submit(ctx, t.Owner, child.TaskRequest{Prompt: prompt, Repo: t.Repo, Ref: t.Ref, Title: t.Title}, t.ID)
	if err != nil {
		// Counted like a run that failed (a refused #spawn already is:
		// run settles the session failed and sessionEnded retries), so a
		// submit that keeps failing is bounded and backed off, not
		// re-attempted uncounted on every beat.
		p.log.Warn("parent: task session", "task", t.ID, "state", t.State, "err", err)
		p.retry(ctx, t, f, "starting the run: "+err.Error())
		return
	}
	p.log.Info("parent: task session", "task", t.ID, "state", t.State, "attempt", in.Attempt, "session", sess.ID)
}

// join moves a children-state task on once every child ended.
func (p *Parent) join(ctx context.Context, t Task, f Flow) {
	st := f.States[t.State]
	if st.Driver != DriverChildren {
		return
	}
	kids, err := p.store.ChildTasks(t.ID)
	if err != nil || len(kids) == 0 {
		return
	}
	type done struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		State string `json:"state"`
	}
	var ended []done
	for _, k := range kids {
		kf := f
		if k.Flow != f.ID || k.FlowVersion != f.Version {
			if kf, err = p.store.Flow(k.Flow, k.FlowVersion); err != nil {
				return
			}
		}
		if !kf.States[k.State].Terminal() {
			return
		}
		ended = append(ended, done{k.ID, k.Title, k.State})
	}
	art, _ := json.Marshal(map[string]any{"children": ended})
	if _, err := p.apply(ctx, t, f, byParent, "", Transition{To: st.Next[0], Note: fmt.Sprintf("all %d children ended", len(ended)), Artifact: art}); err != nil {
		p.log.Warn("parent: join", "task", t.ID, "err", err)
	}
}

// sessionEnded is the engine's half of a session ending: a settled
// run is read for its transition; an aborted one parks the task for a
// person; a failed or lost one is retried until Options.Retries is
// spent. Idempotent: a session the task no longer points at is
// ignored.
func (p *Parent) sessionEnded(ctx context.Context, session string) {
	p.tmu.Lock()
	defer p.tmu.Unlock()
	t, err := p.store.TaskBySession(session)
	if err != nil || t.Session != session {
		return
	}
	sess, err := p.store.Get(session)
	if err != nil || sess.Live() {
		return
	}
	f, err := p.store.Flow(t.Flow, t.FlowVersion)
	if err != nil {
		p.log.Warn("parent: task flow", "task", t.ID, "err", err)
		return
	}
	if f.States[t.State].Driver != DriverAgent {
		return
	}
	switch sess.Status {
	case StatusSettled:
		end, err := p.store.LastAssistant(session)
		if err != nil {
			p.retry(ctx, t, f, "reading the run: "+err.Error())
			return
		}
		if end.Failed() {
			// The provider refused the call: say so, not "no transition
			// block". A refusal that no retry can change (credentials,
			// billing, a model that does not exist) parks the task now
			// rather than burning the remaining attempts on it.
			reason := "model error: " + clip(end.Error, 300)
			if fatalModelError(end.Error) {
				p.park(ctx, t, f, session, "not retried, "+reason)
				return
			}
			p.retry(ctx, t, f, reason)
			return
		}
		tr, ok, err := ParseTransition(end.Text)
		if !ok {
			p.retry(ctx, t, f, "the run ended without a transition block")
			return
		}
		if err != nil {
			p.retry(ctx, t, f, err.Error())
			return
		}
		if _, err := p.apply(ctx, t, f, "session:"+session, session, tr); err != nil {
			p.retry(ctx, t, f, err.Error())
		}
	case StatusAborted:
		p.park(ctx, t, f, session, "run aborted: "+sess.Reason)
	default:
		p.retry(ctx, t, f, sess.Status+": "+sess.Reason)
	}
}

// fatalModelErr matches a provider refusal no retry can change: the
// HTTP status pi prefixes (401 unauthenticated, 402 payment required,
// 403 forbidden, 404 no such model) or the error types behind them.
var fatalModelErr = regexp.MustCompile(`^40[1234]\b|billing_error|payment_required|authentication_error|invalid_api_key|insufficient_quota`)

func fatalModelError(msg string) bool { return fatalModelErr.MatchString(msg) }

// clip cuts s to at most n runes with an ellipsis, for a log line or
// a transition note.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\u2026"
}

// retry counts a failed attempt: another session while retries
// remain, else the task is parked in the flow's stuck state. The next
// attempt waits out its backoff first (a #task refusal or a provider
// outage must not burn every attempt in seconds); the beat spawns it
// once due.
func (p *Parent) retry(ctx context.Context, t Task, f Flow, reason string) {
	n, err := p.store.Retry(t.ID, reason)
	if err != nil {
		p.log.Warn("parent: retry", "task", t.ID, "err", err)
		return
	}
	if n > p.retries {
		p.park(ctx, t, f, t.Session, fmt.Sprintf("gave up after %d attempts: %s", n, reason))
		return
	}
	wait := p.backoffFor(n)
	p.log.Info("parent: task retry", "task", t.ID, "state", t.State, "attempt", n+1, "after", wait, "reason", reason)
	if wait > 0 {
		return // the beat spawns it: reconcileTasks checks retryDue
	}
	t.Retries, t.Error, t.Session = n, reason, ""
	p.spawnFor(ctx, t, f)
}

// backoffFor is the wait before attempt n+1 after n failed ones:
// Options.Backoff doubling per failure, capped at 16 times it.
func (p *Parent) backoffFor(n int) time.Duration {
	if p.backoff <= 0 || n <= 0 {
		return 0
	}
	if n > 5 {
		n = 5
	}
	return p.backoff << (n - 1)
}

// retryDue reports whether a task with no session may be (re)spawned:
// at once on its first attempt, after its backoff on the next ones
// (counted from the failure that Retry stamped on updated).
func (p *Parent) retryDue(t Task, now time.Time) bool {
	if t.Retries == 0 {
		return true
	}
	return !now.Before(time.Unix(t.Updated, 0).Add(p.backoffFor(t.Retries)))
}

// park moves the task to the stuck state for a person, unless it is
// already there (or stuck is not an exit — then it stays put with the
// reason on the task and logged).
func (p *Parent) park(ctx context.Context, t Task, f Flow, session, reason string) {
	if t.State == f.Stuck {
		return
	}
	tr := Transition{To: f.Stuck, Note: reason}
	if !f.States[t.State].Allows(f.Stuck) {
		p.log.Warn("parent: task stuck with no exit to "+f.Stuck, "task", t.ID, "state", t.State, "reason", reason)
		if err := p.store.Stall(t.ID, reason); err != nil {
			p.log.Warn("parent: stall", "task", t.ID, "err", err)
		}
		return
	}
	if _, err := p.apply(ctx, t, f, byParent, session, tr); err != nil {
		p.log.Warn("parent: park", "task", t.ID, "err", err)
	}
}

// reconcileTasks is the beat's safety net and the retry clock: an
// agent state with no session (a restart between move and spawn, or a
// failed attempt whose backoff has run out) is entered; one whose
// session ended without being heard (a restart lost it) is settled;
// a children state is re-checked.
func (p *Parent) reconcileTasks(ctx context.Context) {
	tasks, err := p.store.TasksInStates(func(f Flow, s string) bool {
		d := f.States[s].Driver
		return d == DriverAgent || d == DriverChildren
	})
	if err != nil {
		p.log.Warn("parent: reconcile tasks", "err", err)
		return
	}
	for _, t := range tasks {
		f, err := p.store.Flow(t.Flow, t.FlowVersion)
		if err != nil {
			continue
		}
		switch f.States[t.State].Driver {
		case DriverChildren:
			p.tmu.Lock()
			p.join(ctx, t, f)
			p.tmu.Unlock()
		case DriverAgent:
			if t.Session == "" {
				p.tmu.Lock()
				if cur, err := p.store.Task(t.ID); err == nil && cur.Session == "" && cur.State == t.State && p.retryDue(cur, time.Now()) {
					p.spawnFor(ctx, cur, f)
				}
				p.tmu.Unlock()
				continue
			}
			p.sessionEnded(ctx, t.Session)
		}
	}
}
