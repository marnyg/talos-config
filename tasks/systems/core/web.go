package core

import (
	"bytes"
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
	tplTags   = template.Must(template.New("tags.html").Funcs(web.Funcs).ParseFS(templates, "templates/tags.html"))
)

// scopeCookie carries the active scope: a tag every view narrows to.
// UI state, not domain state "which context am I working in" is
// not a fact about any task, so it is neither an event nor a
// component.
const scopeCookie = "scope"

// Scope returns the request's active scope ("" = everything).
func Scope(r *http.Request) string {
	c, err := r.Cookie(scopeCookie)
	if err != nil {
		return ""
	}
	s, err := NormalizeTag(c.Value)
	if err != nil {
		return ""
	}
	return s
}

func setScope(rw http.ResponseWriter, scope string) {
	c := &http.Cookie{Name: scopeCookie, Value: scope, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode}
	if scope == "" {
		c.MaxAge = -1
	}
	http.SetCookie(rw, c)
}

// Mount exposes the core views and the action endpoints every other
// view posts to:
//
//	GET  /tasks            list
//	GET  /tasks/new        create form
//	POST /tasks            create
//	GET  /tasks/{id}       detail + edit + history
//	POST /tasks/{id}/edit  field=title|body value=...
//	POST /tasks/{id}/{action}  done|reopen|snooze|wake|flag|groomed
//	POST /tasks/{id}/tag       tag=<tag>   (add)
//	POST /tasks/{id}/untag     tag=<tag>
//	POST /scope                scope=<tag>|""  sets the scope cookie
//
// snooze takes until=tomorrow|week|YYYY-MM-DD. The create form takes
// tags= as a comma/space list; the new task carries them from birth.
func (System) Mount(mux *http.ServeMux, w *ecs.World) {
	web.RegisterNarrator(Narrate)
	web.RegisterChrome(func(r *http.Request) map[string]any {
		var vocab []string
		w.Read(func(rd ecs.Reader) { vocab = Vocabulary(rd) })
		return map[string]any{"Scope": Scope(r), "Scopes": vocab, "Path": r.URL.RequestURI()}
	})

	mux.HandleFunc("POST /scope", func(rw http.ResponseWriter, r *http.Request) {
		scope := ""
		if v := r.FormValue("scope"); v != "" {
			s, err := NormalizeTag(v)
			if err != nil {
				web.Fail(rw, err, http.StatusBadRequest)
				return
			}
			scope = s
		}
		setScope(rw, scope)
		web.Back(rw, r, "/")
	})

	mux.HandleFunc("GET /tasks", func(rw http.ResponseWriter, r *http.Request) {
		var tasks []Task
		w.Read(func(rd ecs.Reader) { tasks = Scoped(rd, Scope(r)) })
		web.Render(rw, r, tplList, map[string]any{"Tasks": tasks, "Now": w.Now()})
	})

	mux.HandleFunc("GET /tasks/new", func(rw http.ResponseWriter, r *http.Request) {
		web.Render(rw, r, tplNew, map[string]any{"Tags": Scope(r)})
	})

	mux.HandleFunc("POST /tasks", func(rw http.ResponseWriter, r *http.Request) {
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			web.Fail(rw, errors.New("title required"), http.StatusBadRequest)
			return
		}
		tags, err := ParseTags(r.FormValue("tags"))
		if err != nil {
			web.Fail(rw, err, http.StatusBadRequest)
			return
		}
		if _, err := Create(w, title, strings.TrimSpace(r.FormValue("body")), tags...); err != nil {
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
		web.Render(rw, r, tplDetail, map[string]any{"T": t, "Now": w.Now(), "Panels": panels})
	})

	mux.HandleFunc("POST /tasks/{id}/tag", func(rw http.ResponseWriter, r *http.Request) {
		id := ecs.EntityID(r.PathValue("id"))
		if err := AddTag(w, id, r.FormValue("tag")); err != nil {
			web.Fail(rw, err, http.StatusBadRequest)
			return
		}
		web.Back(rw, r, "/tasks/"+string(id))
	})

	mux.HandleFunc("POST /tasks/{id}/untag", func(rw http.ResponseWriter, r *http.Request) {
		id := ecs.EntityID(r.PathValue("id"))
		if err := RemoveTag(w, id, r.FormValue("tag")); err != nil {
			web.Fail(rw, err, http.StatusBadRequest)
			return
		}
		web.Back(rw, r, "/tasks/"+string(id))
	})

	// Tags panel first: registered before dag/narrative mount theirs.
	web.RegisterPanel(web.Panel{Name: "Tags", Render: func(w *ecs.World, id ecs.EntityID) (template.HTML, error) {
		data := map[string]any{"ID": id}
		w.Read(func(rd ecs.Reader) {
			t, _ := Load(rd, id)
			data["Tags"] = t.Tags
			var others []string
			for _, v := range Vocabulary(rd) {
				if !t.Has(v) {
					others = append(others, v)
				}
			}
			data["Others"] = others
		})
		var buf bytes.Buffer
		if err := tplTags.Execute(&buf, data); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil
	}})

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

// Narrate tells core events in prose.
func Narrate(ev ecs.Event, r ecs.Reader) (web.Story, bool) {
	if ev.System != Name {
		return web.Story{}, false
	}
	var text string
	switch ev.Type {
	case EvCreated:
		var p Created
		_ = ev.Decode(&p)
		text = fmt.Sprintf("created as “%s”", p.Title)
		if len(p.Tags) > 0 {
			text += " in #" + strings.Join(p.Tags, " #")
		}
	case EvEdited:
		var p Edited
		_ = ev.Decode(&p)
		if p.Field == "title" {
			text = fmt.Sprintf("renamed to “%s”", p.Value)
		} else {
			text = "rewrote the " + p.Field
		}
	case EvDone:
		text = "marked done"
	case EvReopened:
		text = "reopened"
	case EvSnoozed:
		var p Snoozed
		_ = ev.Decode(&p)
		text = "snoozed until " + p.Until.Local().Format("Mon 2 Jan")
	case EvWoken:
		text = "woken early"
	case EvFlaggedForGrooming:
		text = "flagged for grooming"
	case EvGroomed:
		text = "groomed"
	case EvTagged:
		var p Tag
		_ = ev.Decode(&p)
		text = "tagged #" + p.Tag
	case EvUntagged:
		var p Tag
		_ = ev.Decode(&p)
		text = "untagged #" + p.Tag
	default:
		return web.Story{}, false
	}
	return web.Story{Text: text, Entities: []ecs.EntityID{ev.Entity}}, true
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
