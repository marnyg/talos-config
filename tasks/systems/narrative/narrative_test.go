package narrative

import (
	"net/http"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/tasks/ecs"
	"github.com/marnyg/talos-config/tasks/systems/core"
	"github.com/marnyg/talos-config/tasks/systems/dag"
	"github.com/marnyg/talos-config/tasks/web"
)

func TestStoryFollowsEdges(t *testing.T) {
	w, err := ecs.New(&ecs.MemLog{}, core.System{}, dag.System{}, System{})
	if err != nil {
		t.Fatal(err)
	}
	// Mount registers the narrators; a throwaway mux is fine.
	mux := http.NewServeMux()
	core.System{}.Mount(mux, w)
	dag.System{}.Mount(mux, w)

	a, _ := core.Create(w, "paint", "")
	b, _ := core.Create(w, "sand", "")
	e, _ := dag.Link(w, b, a)
	core.Edit(w, a, "title", "paint the door")
	dag.Unlink(w, e)

	story := func(id ecs.EntityID) []string {
		lines, err := Tell(w, func(_ ecs.Event, s web.Story) bool {
			for _, x := range s.Entities {
				if x == id {
					return true
				}
			}
			return false
		})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range lines {
			out = append(out, l.Text)
		}
		return out
	}
	got := strings.Join(story(a), " | ")
	want := []string{"created as “paint”", "“sand” now blocks “paint the door”", "renamed to “paint the door”", "“sand” no longer blocks “paint the door”"}
	for _, s := range want {
		if !strings.Contains(got, s) {
			t.Errorf("story(a) missing %q:\n%s", s, got)
		}
	}
	if sb := story(b); len(sb) != 3 { // created, linked, unlinked
		t.Errorf("story(b) = %v", sb)
	}
}
