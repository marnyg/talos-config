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
