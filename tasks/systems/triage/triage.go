// Package triage is the tinder-style deck: one eligible task at a
// time, three gestures — do (core.Done), groom (core.FlaggedForGrooming),
// sleep (core.Snoozed). It owns no components and emits no canonical
// events; it raises core's through core's endpoints.
package triage

import (
	"embed"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/systems/dag"
	"github.com/marnyg/talos-config/tasks/web"
)

const Name = "triage"

//go:embed templates/*.html
var templates embed.FS

var tplDeck = web.Page(templates, "templates/deck.html")

// System implements ecs.System with no state and web.Mounter.
type System struct{}

func (System) Name() string                                   { return Name }
func (System) Kinds() []ecs.Kind                              { return nil }
func (System) Reduce(ecs.Event, ecs.Reader, ecs.Writer) error { return nil }

// Deck returns today's eligible tasks in today's order.
//
// eligible = ¬Done ∧ ¬sleeping ∧ ¬flagged-for-grooming ∧ ¬blocked.
// Order is random but
// seeded per local calendar day, so a refresh does not reshuffle and
// the next card is stable until acted on.
func Deck(r ecs.Reader, now time.Time) []core.Task {
	var deck []core.Task
	g := dag.Load(r)
	for _, t := range core.All(r) {
		if t.IsDone() || t.Sleeping(now) || t.NeedsGrooming || g.Blocked(r, t.ID) {
			continue
		}
		deck = append(deck, t)
	}
	y, m, d := now.Local().Date()
	seed := uint64(y)*10000 + uint64(m)*100 + uint64(d)
	rng := rand.New(rand.NewPCG(seed, seed))
	rng.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	return deck
}

// Mount serves the deck at /.
func (System) Mount(mux *http.ServeMux, w *ecs.World) {
	mux.HandleFunc("GET /{$}", func(rw http.ResponseWriter, r *http.Request) {
		now := w.Now()
		var deck []core.Task
		w.Read(func(rd ecs.Reader) { deck = Deck(rd, now) })
		data := map[string]any{"Count": len(deck)}
		if len(deck) > 0 {
			data["Card"] = deck[0]
		}
		web.Render(rw, tplDeck, data)
	})
}
