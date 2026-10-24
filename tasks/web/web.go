// Package web is the shared HTML plumbing: one layout, a render
// helper, and the mount hook systems implement to expose views.
// Views are plain server-rendered forms (POST → redirect back).
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
)

//go:embed layout.html
var layoutFS embed.FS

// Mounter is implemented by systems that expose HTTP views.
type Mounter interface {
	Mount(mux *http.ServeMux, w *ecs.World)
}

// Panel is a fragment another system contributes to the task detail
// page. Registration lets core render dag's dependency editor without
// core depending on dag: the arrow stays view → owner.
type Panel struct {
	Name   string
	Render func(w *ecs.World, id ecs.EntityID) (template.HTML, error)
}

var panels []Panel

// RegisterPanel adds a detail-page panel; call from Mount.
func RegisterPanel(p Panel) { panels = append(panels, p) }

// Panels returns the registered panels in registration order.
func Panels() []Panel { return panels }

// Story is one event told in prose, plus the entities it touches so a
// per-task narrative can be filtered without knowing event shapes.
type Story struct {
	Text     string
	Entities []ecs.EntityID
}

// Narrator turns one of its own system's events into a Story; ok=false
// for events it does not own.
type Narrator func(ev ecs.Event, r ecs.Reader) (s Story, ok bool)

var narrators []Narrator

// RegisterNarrator adds a narrator; call from Mount. The event
// vocabulary stays with the system that owns it.
func RegisterNarrator(n Narrator) { narrators = append(narrators, n) }

// Tell runs the narrators over ev; falls back to the raw event name.
func Tell(ev ecs.Event, r ecs.Reader) Story {
	for _, n := range narrators {
		if s, ok := n(ev, r); ok {
			return s
		}
	}
	return Story{Text: ev.Name(), Entities: []ecs.EntityID{ev.Entity}}
}

// Funcs are available to every page.
var Funcs = template.FuncMap{
	"date": func(t time.Time) string { return t.Local().Format("Mon 2 Jan") },
	"when": func(t time.Time) string { return t.Local().Format("2006-01-02 15:04") },
	"q":    url.QueryEscape,
}

// Page parses files from fsys on top of the shared layout. Each page
// template must define "content"; executing the result renders the
// layout with it.
func Page(fsys fs.FS, files ...string) *template.Template {
	t := template.Must(template.New("layout").Funcs(Funcs).ParseFS(layoutFS, "layout.html"))
	return template.Must(t.ParseFS(fsys, files...))
}

// Render executes the page layout with data.
func Render(w http.ResponseWriter, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		log.Printf("render: %v", err)
	}
}

// Back redirects to ?back=, else the Referer, else fallback.
func Back(w http.ResponseWriter, r *http.Request, fallback string) {
	to := r.FormValue("back")
	if to == "" {
		to = r.Referer()
	}
	if to == "" {
		to = fallback
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// Fail writes a 4xx/5xx with the error text.
func Fail(w http.ResponseWriter, err error, code int) {
	log.Printf("http %d: %v", code, err)
	http.Error(w, err.Error(), code)
}
