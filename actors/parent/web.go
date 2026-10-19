package parent

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/marnyg/talos-config/actors/child"
)

//go:embed ui
var uiFS embed.FS

// Web is the parent's surface behind the person gate (ADR-0037 "Who
// may see and steer"): list, watch (SSE tail), search, steer/abort,
// share — every route decides from the cookie's principal against the
// store's ACL; nothing is public but /healthz and the login dance.
type Web struct {
	P     *Parent
	Store *Store
	Login *Login
	Log   *slog.Logger

	tpl *template.Template
}

// Handler builds the mux.
func (w *Web) Handler() http.Handler {
	if w.Log == nil {
		w.Log = slog.Default()
	}
	w.tpl = template.Must(template.New("").Funcs(template.FuncMap{
		"ago":   ago,
		"short": shortWallet,
		"json":  prettyJSON,
	}).ParseFS(uiFS, "ui/*.html"))
	static, _ := fs.Sub(uiFS, "ui")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, r *http.Request) {
		if err := w.Store.Ping(r.Context()); err != nil {
			http.Error(rw, err.Error(), http.StatusServiceUnavailable)
			return
		}
		rw.Write([]byte("ok\n"))
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /login", w.login)
	mux.HandleFunc("GET /callback", w.callback)
	mux.HandleFunc("POST /logout", func(rw http.ResponseWriter, r *http.Request) {
		w.Login.ClearCookie(rw)
		http.Redirect(rw, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /cli/authorize", w.page(w.cliAuthorize))
	mux.HandleFunc("POST /cli/authorize", w.page(w.cliAuthorize))

	// HTML
	mux.HandleFunc("GET /{$}", w.page(w.index))
	mux.HandleFunc("GET /s/{id}", w.page(w.session))
	mux.HandleFunc("POST /sessions", w.page(w.submitForm))

	// JSON
	mux.HandleFunc("GET /api/me", w.api(func(rw http.ResponseWriter, r *http.Request, p Principal) {
		writeJSON(rw, http.StatusOK, p)
	}))
	mux.HandleFunc("GET /api/sessions", w.api(w.apiList))
	mux.HandleFunc("POST /api/sessions", w.api(w.apiSubmit))
	mux.HandleFunc("GET /api/search", w.api(w.apiSearch))
	mux.HandleFunc("GET /api/sessions/{id}", w.api(w.withSession(RightView, w.apiSession)))
	mux.HandleFunc("GET /api/sessions/{id}/events", w.api(w.withSession(RightView, w.apiEvents)))
	mux.HandleFunc("GET /api/sessions/{id}/stream", w.api(w.withSession(RightView, w.apiStream)))
	mux.HandleFunc("GET /api/sessions/{id}/file", w.api(w.withSession(RightView, w.apiFile)))
	mux.HandleFunc("GET /api/sessions/{id}/shares", w.api(w.withSession(RightView, w.apiShares)))
	mux.HandleFunc("POST /api/sessions/{id}/steer", w.api(w.withSession(RightSteer, w.apiSteer)))
	mux.HandleFunc("POST /api/sessions/{id}/abort", w.api(w.withSession(RightSteer, w.apiAbort)))
	mux.HandleFunc("POST /api/sessions/{id}/shares", w.api(w.withOwner(w.apiShare)))
	mux.HandleFunc("DELETE /api/sessions/{id}/shares/{principal}", w.api(w.withOwner(w.apiUnshare)))
	w.taskRoutes(mux)
	return mux
}

// ---- auth plumbing ----------------------------------------------------------

type handler func(http.ResponseWriter, *http.Request, Principal)
type sessionHandler func(http.ResponseWriter, *http.Request, Principal, Session)

// page gates an HTML route: no principal ⇒ redirect to /login.
func (w *Web) page(h handler) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		p, ok := w.Login.FromRequest(r)
		if !ok {
			http.Redirect(rw, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			http.Error(rw, "cross-site request", http.StatusForbidden)
			return
		}
		h(rw, r, p)
	}
}

// api gates a JSON route: no principal ⇒ 401; mutations must be
// same-origin.
// A bearer token (cmd/sap) is the cookie carried by hand — no cookie,
// so no cross-site rule to apply.
func (w *Web) api(h handler) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if p, ok := w.Login.FromBearer(r); ok {
			h(rw, r, p)
			return
		}
		p, ok := w.Login.FromRequest(r)
		if !ok {
			writeJSON(rw, http.StatusUnauthorized, map[string]string{"error": "login required"})
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			writeJSON(rw, http.StatusForbidden, map[string]string{"error": "cross-site request"})
			return
		}
		h(rw, r, p)
	}
}

// sameOrigin is the CSRF rule for mutations: the cookie is SameSite=Lax
// (a cross-site POST carries none), and an Origin header, when the
// browser sends one, must name this host.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// withSession resolves {id} and checks right; 404 for what the
// principal may not see at all (no existence leak), 403 for a right
// it lacks on a session it may view.
func (w *Web) withSession(right string, h sessionHandler) handler {
	return func(rw http.ResponseWriter, r *http.Request, p Principal) {
		id := r.PathValue("id")
		sess, err := w.Store.Get(id)
		if err != nil {
			writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such session"})
			return
		}
		ok, err := w.Store.Can(id, p.Viewer(), right)
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !ok {
			if view, _ := w.Store.Can(id, p.Viewer(), RightView); view {
				writeJSON(rw, http.StatusForbidden, map[string]string{"error": "no " + right + " right on this session"})
			} else {
				writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such session"})
			}
			return
		}
		h(rw, r, p, sess)
	}
}

// withOwner: the share list is the owner's alone.
func (w *Web) withOwner(h sessionHandler) handler {
	return w.withSession(RightView, func(rw http.ResponseWriter, r *http.Request, p Principal, sess Session) {
		if !strings.EqualFold(sess.Owner, p.Wallet) {
			writeJSON(rw, http.StatusForbidden, map[string]string{"error": "only the owner shares a session"})
			return
		}
		h(rw, r, p, sess)
	})
}

func (w *Web) login(rw http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	to, err := w.Login.Start(r.Context(), next)
	if err != nil {
		w.Log.Warn("web: login start", "err", err)
		http.Error(rw, "login unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(rw, r, to, http.StatusSeeOther)
}

func (w *Web) callback(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Error(rw, "login refused: "+e+" "+q.Get("error_description"), http.StatusForbidden)
		return
	}
	p, next, err := w.Login.Finish(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		w.Log.Warn("web: login finish", "err", err)
		http.Error(rw, "login failed: "+err.Error(), http.StatusForbidden)
		return
	}
	w.Login.SetCookie(rw, p)
	w.Log.Info("web: login", "wallet", p.Wallet, "user", p.Username, "groups", p.Groups)
	http.Redirect(rw, r, next, http.StatusSeeOther)
}

// ---- HTML -------------------------------------------------------------------

type pageData struct {
	Me       Principal
	Sessions []Session
	Session  Session
	Shares   []Share
	Owner    bool
	CanSteer bool
	Query    string
	Hits     []Hit
	Error    string
	CLI      cliAsk
}

// cliAsk is what /cli/authorize shows: the local port and nonce the
// CLI is waiting on.
type cliAsk struct {
	Port  int
	State string
}

// cliState is the nonce shape cmd/sap mints (url-safe base64).
var cliState = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// cliAuthorize hands a logged-in browser's principal to a CLI waiting
// on 127.0.0.1 (cmd/sap login). GET asks; POST (same-origin, so a
// cross-site link cannot push a token at a stranger's local port)
// mints a Token and redirects to the CLI's callback with the nonce it
// chose. The token is the cookie's twin: same signer, same TTL.
func (w *Web) cliAuthorize(rw http.ResponseWriter, r *http.Request, p Principal) {
	port, _ := strconv.Atoi(r.FormValue("port"))
	state := r.FormValue("state")
	if port < 1 || port > 65535 || !cliState.MatchString(state) {
		http.Error(rw, "cli/authorize: want port=1..65535 and state=[A-Za-z0-9_-]{16,128}", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		w.render(rw, "cli.html", pageData{Me: p, CLI: cliAsk{Port: port, State: state}})
		return
	}
	q := url.Values{"state": {state}, "token": {w.Login.Token(p)}}
	w.Log.Info("web: cli token", "wallet", p.Wallet, "user", p.Username, "port", port)
	http.Redirect(rw, r, fmt.Sprintf("http://127.0.0.1:%d/?%s", port, q.Encode()), http.StatusSeeOther)
}

func (w *Web) render(rw http.ResponseWriter, name string, d pageData) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := w.tpl.ExecuteTemplate(rw, name, d); err != nil {
		w.Log.Warn("web: render", "page", name, "err", err)
	}
}

func (w *Web) index(rw http.ResponseWriter, r *http.Request, p Principal) {
	d := pageData{Me: p, Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	var err error
	if d.Query != "" {
		d.Hits, err = w.Store.Search(p.Viewer(), d.Query, 50)
	}
	if err == nil {
		d.Sessions, err = w.Store.List(p.Viewer(), 200)
	}
	if err != nil {
		d.Error = err.Error()
	}
	w.render(rw, "index.html", d)
}

func (w *Web) session(rw http.ResponseWriter, r *http.Request, p Principal) {
	id := r.PathValue("id")
	sess, err := w.Store.Get(id)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	if ok, _ := w.Store.Can(id, p.Viewer(), RightView); !ok {
		http.NotFound(rw, r)
		return
	}
	d := pageData{Me: p, Session: sess, Owner: strings.EqualFold(sess.Owner, p.Wallet)}
	d.CanSteer, _ = w.Store.Can(id, p.Viewer(), RightSteer)
	if d.Owner {
		d.Shares, _ = w.Store.Shares(id)
	}
	w.render(rw, "session.html", d)
}

func (w *Web) submitForm(rw http.ResponseWriter, r *http.Request, p Principal) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	sess, err := w.P.Submit(context.WithoutCancel(r.Context()), p.Wallet, child.TaskRequest{
		Prompt: strings.TrimSpace(r.FormValue("prompt")),
		Repo:   strings.TrimSpace(r.FormValue("repo")),
		Ref:    strings.TrimSpace(r.FormValue("ref")),
		Title:  strings.TrimSpace(r.FormValue("title")),
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(rw, r, "/s/"+sess.ID, http.StatusSeeOther)
}

// ---- JSON -------------------------------------------------------------------

func writeJSON(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v)
}

func (w *Web) apiList(rw http.ResponseWriter, r *http.Request, p Principal) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	xs, err := w.Store.List(p.Viewer(), limit)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if xs == nil {
		xs = []Session{}
	}
	writeJSON(rw, http.StatusOK, xs)
}

func (w *Web) apiSubmit(rw http.ResponseWriter, r *http.Request, p Principal) {
	var req child.TaskRequest
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sess, err := w.P.Submit(context.WithoutCancel(r.Context()), p.Wallet, req)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusCreated, sess)
}

func (w *Web) apiSearch(rw http.ResponseWriter, r *http.Request, p Principal) {
	hits, err := w.Store.Search(p.Viewer(), r.URL.Query().Get("q"), 50)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if hits == nil {
		hits = []Hit{}
	}
	writeJSON(rw, http.StatusOK, hits)
}

func (w *Web) apiSession(rw http.ResponseWriter, _ *http.Request, _ Principal, sess Session) {
	writeJSON(rw, http.StatusOK, sess)
}

func (w *Web) apiEvents(rw http.ResponseWriter, r *http.Request, _ Principal, sess Session) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := w.Store.Events(sess.ID, after, limit)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if evs == nil {
		evs = []Event{}
	}
	writeJSON(rw, http.StatusOK, evs)
}

// apiStream is the live tail as server-sent events: catch-up from
// ?after, then every appended line; `end` once the session has
// ended. A watcher that falls behind is caught up from the store.
func (w *Web) apiStream(rw http.ResponseWriter, r *http.Request, _ Principal, sess Session) {
	fl, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("X-Accel-Buffering", "no")
	rw.WriteHeader(http.StatusOK)
	last, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	send := func(ev Event) bool {
		b, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(rw, "id: %d\nevent: line\ndata: %s\n\n", ev.Seq, b); err != nil {
			return false
		}
		fl.Flush()
		last = ev.Seq
		return ev.Type != child.TypeSettled
	}
	catchUp := func() (live bool, ok bool) {
		for {
			evs, err := w.Store.Events(sess.ID, last, 500)
			if err != nil || len(evs) == 0 {
				return true, err == nil
			}
			for _, ev := range evs {
				if !send(ev) {
					return false, true
				}
			}
			if len(evs) < 500 {
				return true, true
			}
		}
	}
	end := func() {
		fmt.Fprintf(rw, "event: end\ndata: {}\n\n")
		fl.Flush()
	}
	ctx := r.Context()
	for {
		ch, cancel := w.Store.Subscribe(sess.ID)
		live, ok := catchUp()
		if !ok || !live {
			cancel()
			if ok {
				end()
			}
			return
		}
		if x, err := w.Store.Get(sess.ID); err == nil && !x.Live() {
			// Ended between the last line and now (a lost session has
			// its line; a race on settle is covered by one more catch-up).
			cancel()
			if live, _ = catchUp(); !live {
				end()
				return
			}
			end()
			return
		}
		keep := true
		for keep {
			select {
			case <-ctx.Done():
				cancel()
				return
			case ev, open := <-ch:
				if !open {
					keep = false // fell behind: resubscribe and catch up
					break
				}
				if ev.Seq <= last {
					continue
				}
				if !send(ev) {
					cancel()
					end()
					return
				}
			case <-time.After(25 * time.Second):
				fmt.Fprint(rw, ": keepalive\n\n")
				fl.Flush()
			}
		}
		cancel()
	}
}

func (w *Web) apiFile(rw http.ResponseWriter, r *http.Request, _ Principal, sess Session) {
	if sess.File == "" {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no session file (not settled, or the child shipped none)"})
		return
	}
	f, err := os.Open(sess.File)
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	rw.Header().Set("Content-Type", "application/x-ndjson")
	rw.Header().Set("Content-Disposition", `attachment; filename="`+sess.ID+`.jsonl"`)
	st, _ := f.Stat()
	http.ServeContent(rw, r, sess.ID+".jsonl", st.ModTime(), f)
}

func (w *Web) apiSteer(rw http.ResponseWriter, r *http.Request, p Principal, sess Session) {
	msg, err := textField(rw, r, "message")
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := w.P.Steer(r.Context(), sess.ID, p.Wallet, msg); err != nil {
		writeJSON(rw, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]string{"status": "queued"})
}

func (w *Web) apiAbort(rw http.ResponseWriter, r *http.Request, p Principal, sess Session) {
	if err := w.P.Abort(r.Context(), sess.ID, p.Wallet); err != nil {
		writeJSON(rw, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]string{"status": "aborting"})
}

func (w *Web) apiShares(rw http.ResponseWriter, _ *http.Request, _ Principal, sess Session) {
	shs, err := w.Store.Shares(sess.ID)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if shs == nil {
		shs = []Share{}
	}
	writeJSON(rw, http.StatusOK, shs)
}

func (w *Web) apiShare(rw http.ResponseWriter, r *http.Request, _ Principal, sess Session) {
	var sh Share
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<12)).Decode(&sh); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	} else {
		_ = r.ParseForm()
		sh = Share{Principal: r.FormValue("principal"), Right: r.FormValue("right")}
	}
	if err := w.Store.Share(sess.ID, sh.Principal, sh.Right); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !strings.HasPrefix(ct, "application/json") {
		http.Redirect(rw, r, "/s/"+sess.ID, http.StatusSeeOther)
		return
	}
	writeJSON(rw, http.StatusOK, sh)
}

func (w *Web) apiUnshare(rw http.ResponseWriter, r *http.Request, _ Principal, sess Session) {
	if err := w.Store.Unshare(sess.ID, r.PathValue("principal")); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]string{"status": "removed"})
}

// textField reads one string field from JSON or a form body.
func textField(rw http.ResponseWriter, r *http.Request, name string) (string, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]string
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&m); err != nil {
			return "", err
		}
		if strings.TrimSpace(m[name]) == "" {
			return "", errors.New("empty " + name)
		}
		return m[name], nil
	}
	if err := r.ParseForm(); err != nil {
		return "", err
	}
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		return "", errors.New("empty " + name)
	}
	return v, nil
}

// ---- template helpers -------------------------------------------------------

func ago(t int64) string {
	if t == 0 {
		return "—"
	}
	d := time.Since(time.Unix(t, 0)).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return time.Unix(t, 0).Format("2006-01-02")
}

func prettyJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

func shortWallet(s string) string {
	if len(s) > 12 {
		return s[:6] + "…" + s[len(s)-4:]
	}
	return s
}
