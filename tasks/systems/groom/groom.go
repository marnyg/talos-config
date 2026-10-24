// Package groom is the grooming queue: tasks the user flagged as
// needing thought. It owns nothing; the task detail page (edit, deps,
// "groomed") is the grooming surface, this is the list into it.
package groom

import (
	"embed"
	"net/http"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/web"
)

const Name = "groom"

//go:embed templates/*.html
var templates embed.FS

var tplQueue = web.Page(templates, "templates/queue.html")

// System implements ecs.System with no state.
type System struct{}

func (System) Name() string                                   { return Name }
func (System) Kinds() []ecs.Kind                              { return nil }
func (System) Reduce(ecs.Event, ecs.Reader, ecs.Writer) error { return nil }

// Queue = NeedsGrooming ∧ ¬Done, oldest flag first.
func Queue(r ecs.Reader) []core.Task {
	var out []core.Task
	for _, t := range core.All(r) {
		if t.NeedsGrooming && !t.IsDone() {
			out = append(out, t)
		}
	}
	return out
}

// Mount serves /groom.
func (System) Mount(mux *http.ServeMux, w *ecs.World) {
	mux.HandleFunc("GET /groom", func(rw http.ResponseWriter, r *http.Request) {
		var q []core.Task
		w.Read(func(rd ecs.Reader) { q = Queue(rd) })
		web.Render(rw, tplQueue, map[string]any{"Queue": q})
	})
}
