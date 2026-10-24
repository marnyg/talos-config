package dag

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/web"
)

//go:embed templates/*.html
var templates embed.FS

var (
	tplGraph = web.Page(templates, "templates/graph.html")
	tplPanel = template.Must(template.New("panel.html").Funcs(web.Funcs).ParseFS(templates, "templates/panel.html"))
)

// Mount serves the graph view and the link endpoints, and registers the
// dependency panel on the task detail page.
//
//	GET  /graph               topological layers of open tasks
//	POST /dag/link            from=<id> to=<id>  (from blocks to)
//	POST /dag/unlink/{edge}
func (System) Mount(mux *http.ServeMux, w *ecs.World) {
	mux.HandleFunc("GET /graph", func(rw http.ResponseWriter, r *http.Request) {
		type row struct {
			core.Task
			Blockers []core.Task
		}
		var layers [][]row
		w.Read(func(rd ecs.Reader) {
			g := Load(rd)
			for _, layer := range g.Layers(rd) {
				var rows []row
				for _, t := range layer {
					x := row{Task: t}
					for _, b := range g.Blockers(rd, t.ID) {
						if bt, ok := core.Load(rd, b); ok {
							x.Blockers = append(x.Blockers, bt)
						}
					}
					rows = append(rows, x)
				}
				layers = append(layers, rows)
			}
		})
		web.Render(rw, tplGraph, map[string]any{"Layers": layers})
	})

	mux.HandleFunc("POST /dag/link", func(rw http.ResponseWriter, r *http.Request) {
		from, to := ecs.EntityID(r.FormValue("from")), ecs.EntityID(r.FormValue("to"))
		if from == "" || to == "" {
			web.Fail(rw, errors.New("from and to required"), http.StatusBadRequest)
			return
		}
		if _, err := Link(w, from, to); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, core.ErrNoSuchTask) {
				code = http.StatusNotFound
			}
			web.Fail(rw, err, code)
			return
		}
		web.Back(rw, r, "/tasks/"+string(to))
	})

	mux.HandleFunc("POST /dag/unlink/{edge}", func(rw http.ResponseWriter, r *http.Request) {
		if err := Unlink(w, ecs.EntityID(r.PathValue("edge"))); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, ErrNoSuchEdge) {
				code = http.StatusNotFound
			}
			web.Fail(rw, err, code)
			return
		}
		web.Back(rw, r, "/graph")
	})

	web.RegisterNarrator(Narrate)

	web.RegisterPanel(web.Panel{Name: "Dependencies", Render: func(w *ecs.World, id ecs.EntityID) (template.HTML, error) {
		type rel struct {
			Edge ecs.EntityID
			Task core.Task
		}
		data := map[string]any{"ID": id}
		w.Read(func(rd ecs.Reader) {
			g := Load(rd)
			load := func(rels []Rel) []rel {
				var out []rel
				for _, x := range rels {
					if t, ok := core.Load(rd, x.Other); ok {
						out = append(out, rel{x.Edge, t})
					}
				}
				return out
			}
			data["BlockedBy"] = load(g.BlockedBy(id))
			data["Blocks"] = load(g.Blocks(id))
			// Candidates: open tasks not already adjacent and not self.
			adjacent := map[ecs.EntityID]bool{id: true}
			for _, x := range g.BlockedBy(id) {
				adjacent[x.Other] = true
			}
			for _, x := range g.Blocks(id) {
				adjacent[x.Other] = true
			}
			var cands []core.Task
			for _, t := range core.All(rd) {
				if !t.IsDone() && !adjacent[t.ID] {
					cands = append(cands, t)
				}
			}
			data["Candidates"] = cands
		})
		var buf bytes.Buffer
		if err := tplPanel.Execute(&buf, data); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil
	}})
}

// Narrate tells dag events in prose. Titles are resolved against the
// current state; a task that no longer exists is shown by id.
func Narrate(ev ecs.Event, r ecs.Reader) (web.Story, bool) {
	if ev.System != Name {
		return web.Story{}, false
	}
	var p EdgeData
	if err := ev.Decode(&p); err != nil {
		return web.Story{}, false
	}
	title := func(id ecs.EntityID) string {
		if t, ok := core.Load(r, id); ok {
			return "“" + t.Title + "”"
		}
		return string(id)
	}
	verb := map[string]string{EvLinked: "now blocks", EvUnlinked: "no longer blocks"}[ev.Type]
	if verb == "" {
		return web.Story{}, false
	}
	return web.Story{
		Text:     fmt.Sprintf("%s %s %s", title(p.From), verb, title(p.To)),
		Entities: []ecs.EntityID{p.From, p.To, ev.Entity},
	}, true
}
