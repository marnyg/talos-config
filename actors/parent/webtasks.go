package parent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The task layer's routes (spike y8m0): projects with goals, tasks on
// a flow, one transition call. Same gate as the sessions: the cookie's
// principal against the store; a task is the project owner's, the
// task owner's, and admins'.

func (w *Web) taskRoutes(mux *http.ServeMux) {
	// HTML
	mux.HandleFunc("GET /tasks", w.page(w.board))
	mux.HandleFunc("GET /t/{id}", w.page(w.taskPage))
	mux.HandleFunc("POST /projects", w.page(w.projectForm))
	mux.HandleFunc("POST /p/{id}/goals", w.page(w.goalsForm))
	mux.HandleFunc("POST /tasks", w.page(w.taskForm))
	mux.HandleFunc("POST /t/{id}/transition", w.page(w.transitionForm))

	// JSON
	mux.HandleFunc("GET /api/flows", w.api(w.apiFlows))
	mux.HandleFunc("GET /api/flows/{id}", w.api(w.apiFlow))
	mux.HandleFunc("GET /api/projects", w.api(w.apiProjects))
	mux.HandleFunc("POST /api/projects", w.api(w.apiCreateProject))
	mux.HandleFunc("GET /api/projects/{id}", w.api(w.withProject(w.apiProject)))
	mux.HandleFunc("PUT /api/projects/{id}/goals", w.api(w.withProject(w.apiPutGoals)))
	mux.HandleFunc("PUT /api/projects/{id}/flow", w.api(w.withProject(w.apiPutFlow)))
	mux.HandleFunc("GET /api/tasks", w.api(w.apiTasks))
	mux.HandleFunc("POST /api/tasks", w.api(w.apiCreateTask))
	mux.HandleFunc("GET /api/tasks/{id}", w.api(w.withTask(w.apiTask)))
	mux.HandleFunc("POST /api/tasks/{id}/transition", w.api(w.withTask(w.apiTransition)))
}

type projectHandler func(http.ResponseWriter, *http.Request, Principal, Project)
type taskHandler func(http.ResponseWriter, *http.Request, Principal, Task)

func (w *Web) canProject(p Project, v Viewer) bool {
	return v.Admin() || strings.EqualFold(p.Owner, v.Wallet)
}

func (w *Web) withProject(h projectHandler) handler {
	return func(rw http.ResponseWriter, r *http.Request, p Principal) {
		pr, err := w.Store.Project(r.PathValue("id"))
		if err != nil || !w.canProject(pr, p.Viewer()) {
			writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such project"})
			return
		}
		h(rw, r, p, pr)
	}
}

func (w *Web) withTask(h taskHandler) handler {
	return func(rw http.ResponseWriter, r *http.Request, p Principal) {
		t, err := w.Store.Task(r.PathValue("id"))
		if err != nil || !w.Store.CanTask(t, p.Viewer()) {
			writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such task"})
			return
		}
		h(rw, r, p, t)
	}
}

// ---- views ------------------------------------------------------------------

// taskView is a task with what the page and the API show beside it.
type taskView struct {
	Task
	Project  Project    `json:"-"`
	GoalText string     `json:"goal_text,omitempty"`
	Driver   string     `json:"driver"`
	Doc      string     `json:"doc,omitempty"`
	Next     []string   `json:"next"`
	Terminal bool       `json:"terminal"`
	CanMove  bool       `json:"can_move"`
	Log      []LogEntry `json:"log"`
	Children []Task     `json:"children"`
	Sessions []Session  `json:"sessions"`
	ParentT  *Task      `json:"parent_task,omitempty"`
}

func (w *Web) taskView(t Task, v Viewer) (taskView, error) {
	tv := taskView{Task: t}
	var err error
	if tv.Project, err = w.Store.Project(t.Project); err != nil {
		return tv, err
	}
	f, err := w.Store.Flow(t.Flow, t.FlowVersion)
	if err != nil {
		return tv, err
	}
	st := f.States[t.State]
	tv.Driver, tv.Doc, tv.Next, tv.Terminal = st.Driver, st.Doc, st.Next, st.Terminal()
	if tv.Next == nil {
		tv.Next = []string{}
	}
	goals, _ := w.Store.Goals(t.Project)
	tv.GoalText = goalText(goals, t.Goal)
	live := false
	if t.Session != "" {
		if s, err := w.Store.Get(t.Session); err == nil {
			live = s.Live()
		}
	}
	tv.CanMove = w.Store.CanTask(t, v) && !tv.Terminal && st.Driver != DriverChildren && (st.Driver == DriverHuman || !live)
	if tv.Log, err = w.Store.Log(t.ID); err != nil {
		return tv, err
	}
	if tv.Children, err = w.Store.ChildTasks(t.ID); err != nil {
		return tv, err
	}
	if tv.Children == nil {
		tv.Children = []Task{}
	}
	if tv.Sessions, err = w.Store.SessionsOfTask(t.ID); err != nil {
		return tv, err
	}
	if tv.Sessions == nil {
		tv.Sessions = []Session{}
	}
	if t.Parent != "" {
		if pt, err := w.Store.Task(t.Parent); err == nil {
			tv.ParentT = &pt
		}
	}
	return tv, nil
}

// ---- HTML -------------------------------------------------------------------

type boardData struct {
	Me       Principal
	Projects []Project
	Project  Project
	Goals    []Goal
	Flow     Flow
	States   []string
	ByState  map[string][]Task
	Count    map[string]int // tasks in the state, shown or not
	Hidden   map[string]int // of those, cut by boardPerState
	Error    string
	Session  Session // for head.html's title
	Repos    []Repo  // the picker's options
}

// boardPerState caps each column; the rest is counted, not listed.
const boardPerState = 50

type taskData struct {
	Me      Principal
	T       taskView
	Session Session // for head.html's title
	Error   string
}

func (w *Web) board(rw http.ResponseWriter, r *http.Request, p Principal) {
	d := boardData{Me: p, ByState: map[string][]Task{}, Hidden: map[string]int{}, Error: r.URL.Query().Get("error")}
	var err error
	if d.Projects, err = w.Store.Projects(p.Viewer()); err != nil {
		d.Error = err.Error()
	}
	id := r.URL.Query().Get("project")
	for _, pr := range d.Projects {
		if pr.ID == id || (id == "" && d.Project.ID == "") {
			d.Project = pr
		}
	}
	if d.Project.ID != "" {
		d.Goals, _ = w.Store.Goals(d.Project.ID)
		if d.Flow, err = w.Store.Flow(d.Project.Flow, d.Project.FlowVersion); err == nil {
			d.States = d.Flow.StateNames()
		}
		tasks, count, err := w.Store.BoardTasks(p.Viewer(), d.Project.ID, boardPerState)
		if err != nil {
			d.Error = err.Error()
		}
		d.Count = count
		for _, t := range tasks {
			d.ByState[t.State] = append(d.ByState[t.State], t)
		}
		for st, n := range count {
			if h := n - len(d.ByState[st]); h > 0 {
				d.Hidden[st] = h
			}
		}
		d.Repos = w.Forge.Repos(r.Context())
	}
	w.renderAny(rw, "tasks.html", d)
}

func (w *Web) taskPage(rw http.ResponseWriter, r *http.Request, p Principal) {
	t, err := w.Store.Task(r.PathValue("id"))
	if err != nil || !w.Store.CanTask(t, p.Viewer()) {
		http.NotFound(rw, r)
		return
	}
	tv, err := w.taskView(t, p.Viewer())
	d := taskData{Me: p, T: tv, Error: r.URL.Query().Get("error")}
	if err != nil {
		d.Error = err.Error()
	}
	w.renderAny(rw, "task.html", d)
}

func (w *Web) renderAny(rw http.ResponseWriter, name string, d any) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := w.tpl.ExecuteTemplate(rw, name, d); err != nil {
		w.Log.Warn("web: render", "page", name, "err", err)
	}
}

func (w *Web) projectForm(rw http.ResponseWriter, r *http.Request, p Principal) {
	_ = r.ParseForm()
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		id = slug(r.FormValue("name"))
	}
	pr, err := w.Store.CreateProject(Project{ID: id, Name: r.FormValue("name"), Owner: p.Wallet})
	if err == nil {
		err = w.Store.PutGoals(pr.ID, parseGoals(r.FormValue("goals")))
	}
	if err != nil {
		http.Redirect(rw, r, "/tasks?error="+queryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(rw, r, "/tasks?project="+pr.ID, http.StatusSeeOther)
}

func (w *Web) goalsForm(rw http.ResponseWriter, r *http.Request, p Principal) {
	pr, err := w.Store.Project(r.PathValue("id"))
	if err != nil || !w.canProject(pr, p.Viewer()) {
		http.NotFound(rw, r)
		return
	}
	_ = r.ParseForm()
	if err := w.Store.PutGoals(pr.ID, parseGoals(r.FormValue("goals"))); err != nil {
		http.Redirect(rw, r, "/tasks?project="+pr.ID+"&error="+queryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(rw, r, "/tasks?project="+pr.ID, http.StatusSeeOther)
}

func (w *Web) taskForm(rw http.ResponseWriter, r *http.Request, p Principal) {
	_ = r.ParseForm()
	project := r.FormValue("project")
	pr, err := w.Store.Project(project)
	if err != nil || !w.canProject(pr, p.Viewer()) {
		http.NotFound(rw, r)
		return
	}
	spec := TaskSpec{Title: r.FormValue("title"), Description: strings.TrimSpace(r.FormValue("description")),
		Goal: r.FormValue("goal"), Why: strings.TrimSpace(r.FormValue("why")), Repo: strings.TrimSpace(r.FormValue("repo")), Ref: strings.TrimSpace(r.FormValue("ref"))}
	t, err := w.P.CreateTask(context.WithoutCancel(r.Context()), p.Wallet, project, spec, r.FormValue("start") != "")
	if err != nil {
		http.Redirect(rw, r, "/tasks?project="+project+"&error="+queryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(rw, r, "/t/"+t.ID, http.StatusSeeOther)
}

func (w *Web) transitionForm(rw http.ResponseWriter, r *http.Request, p Principal) {
	t, err := w.Store.Task(r.PathValue("id"))
	if err != nil || !w.Store.CanTask(t, p.Viewer()) {
		http.NotFound(rw, r)
		return
	}
	_ = r.ParseForm()
	tr := Transition{To: r.FormValue("to"), Note: r.FormValue("note")}
	if _, err := w.P.Transition(context.WithoutCancel(r.Context()), t.ID, p.Wallet, tr); err != nil {
		http.Redirect(rw, r, "/t/"+t.ID+"?error="+queryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(rw, r, "/t/"+t.ID, http.StatusSeeOther)
}

// parseGoals reads "id: text" lines (a bare line gets g<n> as id).
func parseGoals(s string) []Goal {
	var out []Goal
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		g := Goal{Text: line}
		if i := strings.Index(line, ":"); i > 0 && i < 32 && ValidID(strings.TrimSpace(line[:i])) {
			g.ID, g.Text = strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		}
		if g.ID == "" {
			g.ID = "g" + strconv.Itoa(len(out)+1)
		}
		out = append(out, g)
	}
	return out
}

// slug makes a project id from a name.
func slug(name string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == ' ', c == '-', c == '_', c == '/':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func queryEscape(s string) string { return url.QueryEscape(s) }

// ---- JSON -------------------------------------------------------------------

func (w *Web) apiFlows(rw http.ResponseWriter, _ *http.Request, _ Principal) {
	fs, err := w.Store.Flows()
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if fs == nil {
		fs = []Flow{}
	}
	writeJSON(rw, http.StatusOK, fs)
}

func (w *Web) apiFlow(rw http.ResponseWriter, r *http.Request, _ Principal) {
	f, err := w.Store.Flow(r.PathValue("id"), 0)
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, f)
}

func (w *Web) apiProjects(rw http.ResponseWriter, _ *http.Request, p Principal) {
	ps, err := w.Store.Projects(p.Viewer())
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if ps == nil {
		ps = []Project{}
	}
	writeJSON(rw, http.StatusOK, ps)
}

func (w *Web) apiCreateProject(rw http.ResponseWriter, r *http.Request, p Principal) {
	var req struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Flow  string `json:"flow"`
		Goals []Goal `json:"goals"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.ID == "" {
		req.ID = slug(req.Name)
	}
	pr, err := w.Store.CreateProject(Project{ID: req.ID, Name: req.Name, Owner: p.Wallet, Flow: req.Flow})
	if err == nil && len(req.Goals) > 0 {
		err = w.Store.PutGoals(pr.ID, req.Goals)
	}
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusCreated, pr)
}

func (w *Web) apiProject(rw http.ResponseWriter, _ *http.Request, _ Principal, pr Project) {
	goals, _ := w.Store.Goals(pr.ID)
	if goals == nil {
		goals = []Goal{}
	}
	writeJSON(rw, http.StatusOK, struct {
		Project
		Goals []Goal `json:"goals"`
	}{pr, goals})
}

// apiPutFlow: {"flow": "default", "version": 0} — version 0 is the
// flow's latest; "" keeps the project's flow and only moves the version.
func (w *Web) apiPutFlow(rw http.ResponseWriter, r *http.Request, _ Principal, pr Project) {
	var req struct {
		Flow    string `json:"flow"`
		Version int    `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := w.Store.SetProjectFlow(pr.ID, req.Flow, req.Version); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrNoFlow) {
			code = http.StatusBadRequest
		}
		writeJSON(rw, code, map[string]string{"error": err.Error()})
		return
	}
	moved, err := w.Store.Project(pr.ID)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, moved)
}

func (w *Web) apiPutGoals(rw http.ResponseWriter, r *http.Request, _ Principal, pr Project) {
	var goals []Goal
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&goals); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := w.Store.PutGoals(pr.ID, goals); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, goals)
}

func (w *Web) apiTasks(rw http.ResponseWriter, r *http.Request, p Principal) {
	ts, err := w.Store.Tasks(p.Viewer(), r.URL.Query().Get("project"), 0)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if ts == nil {
		ts = []Task{}
	}
	writeJSON(rw, http.StatusOK, ts)
}

func (w *Web) apiCreateTask(rw http.ResponseWriter, r *http.Request, p Principal) {
	var req struct {
		Project string `json:"project"`
		Start   bool   `json:"start"`
		TaskSpec
	}
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	pr, err := w.Store.Project(req.Project)
	if err != nil || !w.canProject(pr, p.Viewer()) {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such project"})
		return
	}
	t, err := w.P.CreateTask(context.WithoutCancel(r.Context()), p.Wallet, req.Project, req.TaskSpec, req.Start)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusCreated, t)
}

func (w *Web) apiTask(rw http.ResponseWriter, _ *http.Request, p Principal, t Task) {
	tv, err := w.taskView(t, p.Viewer())
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, tv)
}

func (w *Web) apiTransition(rw http.ResponseWriter, r *http.Request, p Principal, t Task) {
	var tr Transition
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&tr); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	moved, err := w.P.Transition(context.WithoutCancel(r.Context()), t.ID, p.Wallet, tr)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, ErrNoTask) {
			code = http.StatusNotFound
		}
		writeJSON(rw, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, moved)
}
