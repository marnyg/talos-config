// Package narrative renders the event log as prose: a global timeline
// at /log and a "Story" panel per task. It owns nothing and knows no
// event shapes — each system registers a web.Narrator for its own.
package narrative

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/web"
)

const Name = "narrative"

//go:embed templates/*.html
var templates embed.FS

var (
	tplLog   = web.Page(templates, "templates/log.html")
	tplPanel = template.Must(template.New("panel.html").Funcs(web.Funcs).ParseFS(templates, "templates/panel.html"))
)

const logCap = 300

// System implements ecs.System with no state.
type System struct{}

func (System) Name() string                                   { return Name }
func (System) Kinds() []ecs.Kind                              { return nil }
func (System) Reduce(ecs.Event, ecs.Reader, ecs.Writer) error { return nil }

// Line is one told event.
type Line struct {
	Seq     int64
	At      time.Time
	Text    string
	Subject *core.Task // the event's own entity if it is a task
	Raw     string
}

// Tell replays the log and narrates events for which keep returns true.
func Tell(w *ecs.World, keep func(ecs.Event, web.Story) bool) ([]Line, error) {
	var lines []Line
	var err error
	w.Read(func(r ecs.Reader) {
		err = w.History("", func(ev ecs.Event) {
			s := web.Tell(ev, r)
			if !keep(ev, s) {
				return
			}
			l := Line{Seq: ev.Seq, At: ev.At, Text: s.Text, Raw: ev.Name() + " " + string(ev.Payload)}
			if t, ok := core.Load(r, ev.Entity); ok {
				l.Subject = &t
			}
			lines = append(lines, l)
		})
	})
	return lines, err
}

// Mount serves /log and registers the Story panel.
func (System) Mount(mux *http.ServeMux, w *ecs.World) {
	mux.HandleFunc("GET /log", func(rw http.ResponseWriter, r *http.Request) {
		lines, err := Tell(w, func(ecs.Event, web.Story) bool { return true })
		if err != nil {
			web.Fail(rw, err, http.StatusInternalServerError)
			return
		}
		// newest first, capped
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
		total := len(lines)
		if len(lines) > logCap {
			lines = lines[:logCap]
		}
		web.Render(rw, tplLog, map[string]any{"Lines": lines, "Total": total})
	})

	web.RegisterPanel(web.Panel{Name: "Story", Render: func(w *ecs.World, id ecs.EntityID) (template.HTML, error) {
		lines, err := Tell(w, func(_ ecs.Event, s web.Story) bool {
			for _, e := range s.Entities {
				if e == id {
					return true
				}
			}
			return false
		})
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		if err := tplPanel.Execute(&buf, map[string]any{"Lines": lines}); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil
	}})
}
