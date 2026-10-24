package core

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/web"
)

//go:embed templates/*.html
var templates embed.FS

var (
	tplList   = web.Page(templates, "templates/list.html")
	tplNew    = web.Page(templates, "templates/new.html")
	tplDetail = web.Page(templates, "templates/detail.html")
)

// Mount exposes the core views and the action endpoints every other
// view posts to:
//
//	GET  /tasks            list
//	GET  /tasks/new        create form
//	POST /tasks            create
//	GET  /tasks/{id}       detail + edit + history
//	POST /tasks/{id}/edit  field=title|body value=...
//	POST /tasks/{id}/{action}  done|reopen|snooze|wake|flag|groomed
//
// snooze takes until=tomorrow|week|YYYY-MM-DD.
func (System) Mount(mux *http.ServeMux, w *ecs.World) {
	mux.HandleFunc("GET /tasks", func(rw http.ResponseWriter, r *http.Request) {
		var tasks []Task
		w.Read(func(rd ecs.Reader) { tasks = All(rd) })
		web.Render(rw, tplList, map[string]any{"Tasks": tasks, "Now": w.Now()})
	})

	mux.HandleFunc("GET /tasks/new", func(rw http.ResponseWriter, r *http.Request) {
		web.Render(rw, tplNew, nil)
	})

	mux.HandleFunc("POST /tasks", func(rw http.ResponseWriter, r *http.Request) {
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			web.Fail(rw, errors.New("title required"), http.StatusBadRequest)
			return
		}
		if _, err := Create(w, title, strings.TrimSpace(r.FormValue("body"))); err != nil {
			web.Fail(rw, err, http.StatusBadRequest)
			return
		}
		web.Back(rw, r, "/tasks")
	})

	mux.HandleFunc("GET /tasks/{id}", func(rw http.ResponseWriter, r *http.Request) {
		id := ecs.EntityID(r.PathValue("id"))
		var (
			t  Task
			ok bool
		)
		w.Read(func(rd ecs.Reader) { t, ok = Load(rd, id) })
		if !ok {
			http.NotFound(rw, r)
			return
		}
		var history []ecs.Event
		if err := w.History(id, func(ev ecs.Event) { history = append(history, ev) }); err != nil {
			web.Fail(rw, err, http.StatusInternalServerError)
			return
		}
		type panel struct {
			Name string
			HTML template.HTML
		}
		var panels []panel
		for _, p := range web.Panels() {
			h, err := p.Render(w, id)
			if err != nil {
				web.Fail(rw, fmt.Errorf("panel %s: %w", p.Name, err), http.StatusInternalServerError)
				return
			}
			panels = append(panels, panel{p.Name, h})
		}
		web.Render(rw, tplDetail, map[string]any{"T": t, "Now": w.Now(), "History": history, "Panels": panels})
	})

	mux.HandleFunc("POST /tasks/{id}/edit", func(rw http.ResponseWriter, r *http.Request) {
		id := ecs.EntityID(r.PathValue("id"))
		if err := Edit(w, id, r.FormValue("field"), strings.TrimSpace(r.FormValue("value"))); err != nil {
			web.Fail(rw, err, http.StatusBadRequest)
			return
		}
		web.Back(rw, r, "/tasks/"+string(id))
	})

	mux.HandleFunc("POST /tasks/{id}/{action}", func(rw http.ResponseWriter, r *http.Request) {
		id := ecs.EntityID(r.PathValue("id"))
		var err error
		switch a := r.PathValue("action"); a {
		case "done":
			err = MarkDone(w, id)
		case "reopen":
			err = Reopen(w, id)
		case "snooze":
			var until time.Time
			until, err = ParseUntil(w.Now(), r.FormValue("until"))
			if err == nil {
				err = Snooze(w, id, until)
			}
		case "wake":
			err = Wake(w, id)
		case "flag":
			err = FlagForGrooming(w, id)
		case "groomed":
			err = Groom(w, id)
		default:
			err = fmt.Errorf("unknown action %q", a)
		}
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, ErrNoSuchTask) {
				code = http.StatusNotFound
			}
			web.Fail(rw, err, code)
			return
		}
		web.Back(rw, r, "/tasks/"+string(id))
	})
}

// ParseUntil turns a snooze choice into an instant: "tomorrow",
// "week", or a YYYY-MM-DD date — always start of that day in now's
// location.
func ParseUntil(now time.Time, s string) (time.Time, error) {
	day := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
	}
	switch s {
	case "tomorrow":
		return day(now.AddDate(0, 0, 1)), nil
	case "week":
		return day(now.AddDate(0, 0, 7)), nil
	case "":
		return time.Time{}, errors.New("until required")
	}
	d, err := time.ParseInLocation("2006-01-02", s, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("bad until %q", s)
	}
	return d, nil
}
