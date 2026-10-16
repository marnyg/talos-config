package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/workouts/store"
	"github.com/marnyg/talos-config/workouts/strongimport"
)

func newApp(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	media := t.TempDir()
	if _, err := strongimport.Import(context.Background(), s, "../strongimport/testdata/export", strongimport.Options{MediaDir: media}); err != nil {
		t.Fatal(err)
	}
	h, err := New(s, media, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.Close(); s.Close() })
	return srv, s
}

func get(t *testing.T, c *http.Client, url string, want ...string) string {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("GET %s: %d %s", url, res.StatusCode, b)
	}
	for _, w := range want {
		if !strings.Contains(string(b), w) {
			t.Fatalf("GET %s: missing %q in\n%s", url, w, b)
		}
	}
	return string(b)
}

func post(t *testing.T, c *http.Client, url, form string) *http.Response {
	t.Helper()
	res, err := c.Post(url, "application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestPagesAndLiveWorkout(t *testing.T) {
	srv, s := newApp(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u := srv.URL

	get(t, c, u+"/", "START AN EMPTY WORKOUT", "My Templates", "Upper", "2 × Bench Press")
	get(t, c, u+"/history", "Upper", "Afternoon Workout", "60 kg × 8", "PRs")
	get(t, c, u+"/workouts/w1", "PERFORM AGAIN", "felt heavy", "5 km")
	get(t, c, u+"/exercises", "Bench Press (Barbell)", "My Curl")
	get(t, c, u+"/exercises?q=curl&body=arms", "My Curl")
	get(t, c, u+"/exercises/bench", "Step one", "VIDaaa.mp4")
	get(t, c, u+"/exercises/bench?s=history", "Sets performed", "60 kg × 8")
	get(t, c, u+"/exercises/bench?s=records", "Estimated 1RM", "74 kg", "(×8)")
	get(t, c, u+"/exercises/bench?s=charts", "<svg", "est. 1RM")
	get(t, c, u+"/exercises/new", "Create")
	get(t, c, u+"/templates/t1", "START WORKOUT", "Pull Up")
	get(t, c, u+"/templates/t1/edit", "tpl-editor", "window.TEMPLATE")
	get(t, c, u+"/templates/new/edit", "tpl-editor")
	get(t, c, u+"/measure", "Weight", "Body fat")
	get(t, c, u+"/measure/weight_kg", "History")
	get(t, c, u+"/profile", "Workouts per week", "2</strong> workouts")
	get(t, c, u+"/manifest.webmanifest", "Workouts")
	get(t, c, u+"/sw.js", "caches")
	get(t, c, u+"/static/app.css", "--accent")
	get(t, c, u+"/media/VIDaaa.mp4", "mp4")

	// start from template → log page prefilled
	if res := post(t, c, u+"/workouts/start", "template=t1"); res.StatusCode != 303 || res.Header.Get("Location") != "/log" {
		t.Fatalf("start: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	body := get(t, c, u+"/log", "FINISH", "Bench Press (Barbell)", `value="60"`, "60 kg × 8", "-KG")
	get(t, c, u+"/", "in progress")
	if res := post(t, c, u+"/workouts/start", ""); res.StatusCode != 303 || !strings.Contains(res.Header.Get("Location"), "already") {
		t.Fatalf("second start: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// pick the first set id from the page and complete it via the API
	i := strings.Index(body, `class="set`)
	j := strings.Index(body[i:], `data-id="`) + i + len(`data-id="`)
	setID := body[j : j+strings.Index(body[j:], `"`)]
	w := 62.5
	reps := 9
	req, _ := http.NewRequest("POST", u+"/api/sets/"+setID, strings.NewReader(`{"kind":"normal","weight":62.5,"reps":9,"completed":true}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out setJSON
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if !out.Completed || *out.Weight != w || *out.Reps != reps || int(out.Est1RM+0.5) != 80 {
		t.Fatalf("api set: %+v", out)
	}
	// add exercise via picker, add a set to it, then finish updating the template
	get(t, c, u+"/log/add?q=curl", "My Curl")
	post(t, c, u+"/log/add", "exercise=custom1")
	body = get(t, c, u+"/log", "My Curl")
	k := strings.LastIndex(body, `class="slot" data-id="`) + len(`class="slot" data-id="`)
	slotID := body[k : k+strings.Index(body[k:], `"`)]
	req, _ = http.NewRequest("POST", u+"/api/exercise-slots/"+slotID+"/sets", nil)
	if res, err := c.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("add set: %v %v", err, res)
	}
	res = post(t, c, u+"/log/finish", "update_template=1")
	if res.StatusCode != 303 || !strings.HasPrefix(res.Header.Get("Location"), "/workouts/") {
		t.Fatalf("finish: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	get(t, c, u+res.Header.Get("Location"), "62.5 kg × 9", "1 PRs")
	tpl, _ := s.GetTemplate(context.Background(), "t1")
	if len(tpl.Exercises) != 1 || len(tpl.Exercises[0].Sets) != 1 || *tpl.Exercises[0].Sets[0].WeightKg != 62.5 {
		t.Fatalf("template not updated from workout: %+v", tpl.Exercises)
	}
	if n, _ := s.CountWorkouts(context.Background()); n != 3 {
		t.Fatalf("workouts %d", n)
	}

	// metrics + settings
	post(t, c, u+"/measure/weight_kg", "value=81,5&date=2026-10-16")
	get(t, c, u+"/measure", "81.5 kg")
	post(t, c, u+"/settings", "rest_s=90&weight_unit=kg")
	get(t, c, u+"/profile", `value="90"`)

	// new exercise + template save JSON
	res = post(t, c, u+"/exercises", "name=Cable+Fly&body=chest&cat=machine_other&instructions=a%0Ab")
	get(t, c, u+res.Header.Get("Location"), "Cable Fly", "Machine other")
	req, _ = http.NewRequest("POST", u+"/templates/new", strings.NewReader(`{"name":"Legs","folder":"u1-my-templates","exercises":[{"exercise":"bench","rest":90,"sets":[{"kind":"warmup","weight":40,"reps":10},{"kind":"normal","weight":80,"reps":5}]}]}`))
	res, _ = c.Do(req)
	var tr map[string]string
	json.NewDecoder(res.Body).Decode(&tr)
	get(t, c, u+tr["redirect"], "Legs", "2 × Bench Press")
	get(t, c, u+"/", "Legs")
}
