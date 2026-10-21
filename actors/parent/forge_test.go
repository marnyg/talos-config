package parent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/parent"
)

// forgejo is a stub of GET /api/v1/user/repos: n repos, paged at 50,
// token-checked; `down` makes it 503.
type forgejo struct {
	srv   *httptest.Server
	n     int
	calls atomic.Int32
	down  atomic.Bool
}

func newForgejo(t *testing.T, n int) *forgejo {
	t.Helper()
	f := &forgejo{n: n}
	f.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path != "/api/v1/user/repos" || r.Header.Get("Authorization") != "token ro" {
			http.Error(rw, "nope", http.StatusUnauthorized)
			return
		}
		if f.down.Load() {
			http.Error(rw, "down", http.StatusServiceUnavailable)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var out []map[string]string
		for i := (page - 1) * limit; i < f.n && i < page*limit; i++ {
			out = append(out, map[string]string{"full_name": fmt.Sprintf("agents/r%03d", f.n-1-i), "default_branch": "main"})
		}
		out = append(out, map[string]string{"full_name": "bad name/with space", "default_branch": "x"})
		if page > 1 {
			out = out[:len(out)-1]
		}
		json.NewEncoder(rw).Encode(out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// TestForgeRepos: the walk pages at 50, drops names a #task would
// refuse, sorts; a second call inside TTL does not hit the forge; a
// forge that is down past TTL serves the last good list once and the
// cache cadence holds; a wrong token yields nothing, not an error.
func TestForgeRepos(t *testing.T) {
	fj := newForgejo(t, 120)
	f := &parent.Forge{URL: fj.srv.URL + "/", Token: "ro", TTL: 50 * time.Millisecond}
	ctx := context.Background()

	got := f.Repos(ctx)
	if len(got) != 120 || got[0].Name != "agents/r000" || got[119].Name != "agents/r119" || got[0].Default != "main" {
		t.Fatalf("repos: %d %+v…", len(got), got[:min(3, len(got))])
	}
	if c := fj.calls.Load(); c != 3 {
		t.Fatalf("pages fetched: %d", c)
	}
	f.Repos(ctx)
	if c := fj.calls.Load(); c != 3 {
		t.Fatalf("cached call hit the forge: %d", c)
	}

	fj.down.Store(true)
	time.Sleep(60 * time.Millisecond)
	if got = f.Repos(ctx); len(got) != 120 {
		t.Fatalf("stale on failure: %d", len(got))
	}
	if c := fj.calls.Load(); c != 4 {
		t.Fatalf("one refresh attempt: %d", c)
	}
	f.Repos(ctx) // inside the back-off
	if c := fj.calls.Load(); c != 4 {
		t.Fatalf("hammered a down forge: %d", c)
	}

	bad := &parent.Forge{URL: fj.srv.URL, Token: "wrong"}
	if got := bad.Repos(ctx); got != nil {
		t.Fatalf("wrong token: %+v", got)
	}
	var none *parent.Forge
	if none.Repos(ctx) != nil {
		t.Fatal("nil forge lists")
	}
}

// TestWebRepoPicker: both new-forms carry the datalist with the
// forge's repos; without a forge the input is still there, free text.
func TestWebRepoPicker(t *testing.T) {
	r := setup(t, true)
	is := newIssuer(t)
	fj := newForgejo(t, 2)
	login := &parent.Login{Issuer: is.srv.URL, ClientID: "agents", Key: []byte(strings.Repeat("k", 32))}
	w := &parent.Web{P: r.p, Store: r.store, Login: login, Forge: &parent.Forge{URL: fj.srv.URL, Token: "ro"}}
	ui := httptest.NewServer(w.Handler())
	login.RedirectURL = ui.URL + "/callback"
	t.Cleanup(ui.Close)
	me := browser(t, is, ui, owner)

	var pr parent.Project
	if code := postJSON(t, me, ui.URL+"/api/projects", map[string]any{"name": "Picker"}, &pr); code != 201 {
		t.Fatalf("project: %d", code)
	}
	for _, u := range []string{"/", "/tasks?project=" + pr.ID} {
		resp, _ := me.Get(ui.URL + u)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		s := string(body)
		if resp.StatusCode != 200 || !strings.Contains(s, `<input name="repo" list="repos"`) ||
			!strings.Contains(s, `<datalist id="repos"><option value="agents/r000">main</option><option value="agents/r001">main</option></datalist>`) {
			t.Fatalf("page %s: %d\n%s", u, resp.StatusCode, s)
		}
	}

	w.Forge = nil
	resp, _ := me.Get(ui.URL + "/")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `<datalist id="repos"></datalist>`) {
		t.Fatalf("no forge: %s", body)
	}
}
