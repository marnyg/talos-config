package kanban

import (
	"embed"
	"errors"
	"net/http"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/web"
)

//go:embed templates/*.html
var templates embed.FS

var tplBoard = web.Page(templates, "templates/board.html")

const doneCap = 25

// Mount serves the board and the move endpoint.
//
//	GET  /board
//	POST /kanban/{id}/move   dir=up|down
func (System) Mount(mux *http.ServeMux, w *ecs.World) {
	web.RegisterNarrator(Narrate)

	mux.HandleFunc("GET /board", func(rw http.ResponseWriter, r *http.Request) {
		type col struct {
			Name    Column
			Entries []Entry
			Hidden  int
		}
		var cols []col
		now := w.Now()
		w.Read(func(rd ecs.Reader) {
			board := Board(rd, now)
			for _, c := range Columns {
				x := col{Name: c, Entries: board[c]}
				if c == ColDone && len(x.Entries) > doneCap {
					x.Hidden = len(x.Entries) - doneCap
					x.Entries = x.Entries[:doneCap]
				}
				cols = append(cols, x)
			}
		})
		web.Render(rw, tplBoard, map[string]any{"Columns": cols, "Now": now})
	})

	mux.HandleFunc("POST /kanban/{id}/move", func(rw http.ResponseWriter, r *http.Request) {
		dir := map[string]int{"up": -1, "down": 1}[r.FormValue("dir")]
		if dir == 0 {
			web.Fail(rw, errors.New("dir must be up or down"), http.StatusBadRequest)
			return
		}
		err := Move(w, ecs.EntityID(r.PathValue("id")), dir)
		if err != nil && !errors.Is(err, ErrNoMove) {
			web.Fail(rw, err, http.StatusBadRequest)
			return
		}
		web.Back(rw, r, "/board")
	})
}

// Narrate tells kanban events.
func Narrate(ev ecs.Event, _ ecs.Reader) (web.Story, bool) {
	if !ev.Is(Name, EvMoved) {
		return web.Story{}, false
	}
	return web.Story{Text: "reordered on the board", Entities: []ecs.EntityID{ev.Entity}}, true
}
