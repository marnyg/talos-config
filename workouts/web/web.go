// Package web is the tracker's HTTP surface: server-rendered pages
// (html/template), a small JSON API the active-workout screen uses,
// media from the data dir, and the PWA shell (manifest + service
// worker) from embedded static files. No auth: the Ingress group gate
// in front of it is the boundary (ADR-0032).
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/marnyg/talos-config/workouts/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Handler serves the app.
type Handler struct {
	s     *store.Store
	media string
	loc   *time.Location
	// tpl is keyed by display weight unit, then page name: the unit
	// reaches templates as closures in the FuncMap, so each unit gets its
	// own parse (templates are immutable once executed).
	tpl map[weightUnit]map[string]*template.Template
	mux *http.ServeMux
}

// New builds the handler. media is the directory of exercise media.
func New(s *store.Store, media string, loc *time.Location) (*Handler, error) {
	h := &Handler{s: s, media: media, loc: loc, mux: http.NewServeMux()}
	if err := h.parseTemplates(); err != nil {
		return nil, err
	}
	h.routes()
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func (h *Handler) parseTemplates() error {
	h.tpl = map[weightUnit]map[string]*template.Template{}
	for _, u := range []weightUnit{unitKg, unitLb} {
		m, err := h.parseTemplatesFor(u)
		if err != nil {
			return err
		}
		h.tpl[u] = m
	}
	return nil
}

func (h *Handler) parseTemplatesFor(u weightUnit) (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"kg":      fmtKg,
		"wt":      u.fmtPtr,
		"wtf":     u.fmt,
		"wti":     u.fmtInt,
		"wunit":   func() string { return string(u) },
		"wstep":   u.step,
		"num":     fmtNum,
		"int":     func(f float64) int { return int(math.Round(f)) },
		"dur":     fmtDur,
		"secs":    fmtSecs,
		"date":    func(t time.Time) string { return t.In(h.loc).Format("Monday, 2 January 2006, 15:04") },
		"day":     func(t time.Time) string { return t.In(h.loc).Format("2 Jan 2006") },
		"month":   func(t time.Time) string { return t.In(h.loc).Format("January 2006") },
		"ago":     h.ago,
		"deref":   deref,
		"derefi":  derefi,
		"setKind": setKindLabel,
		"bestSet": bestSet,
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i + 1
			}
			return s
		},
		"json":  func(v any) template.JS { b, _ := json.Marshal(v); return template.JS(b) },
		"title": func(s string) string { return strings.ToUpper(s[:1]) + s[1:] },
		"label": func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "_", " "), "-", " ") },
		"chart": chartSVG,
		"add":   func(a, b int) int { return a + b },
		"deref2": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"secs2": func(i *int) string {
			if i == nil {
				return "-"
			}
			f := float64(*i)
			return fmtSecs(&f)
		},
		"div1000": func(f *float64) float64 {
			if f == nil {
				return 0
			}
			return *f / 1000
		},
		"join":    strings.Join,
		"metrics": func() any { return store.Metrics },
		"today":   func() string { return time.Now().In(h.loc).Format("2006-01-02") },
		"weightLabel": func(e store.Exercise) string {
			label := strings.ToUpper(string(u))
			if e.HasCell(store.CellAssistedBodyweight) {
				return "-" + label
			}
			if e.HasCell(store.CellWeightedBodyweight) {
				return "+" + label
			}
			return label
		},
		// plateCalc: the plate calculator applies to plain loaded weight
		// only; bodyweight flavours record assistance/added load, not a bar.
		"plateCalc": func(e store.Exercise) bool {
			return e.HasWeight() && !e.HasCell(store.CellAssistedBodyweight) && !e.HasCell(store.CellWeightedBodyweight)
		},
	}
	tpl := map[string]*template.Template{}
	pages, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		name := path.Base(p)
		if name == "layout.html" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		tpl[name] = t
	}
	return tpl, nil
}

type page struct {
	Title   string
	Tab     string
	Current *store.Workout // in-progress workout, for the banner
	Data    any
	Flash   string
	// NoBanner suppresses the in-progress banner (the log page itself).
	NoBanner bool
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	if p.Current == nil {
		if cur, err := h.s.CurrentWorkout(r.Context()); err == nil {
			p.Current = &cur
		}
	}
	if p.Flash == "" {
		p.Flash = r.URL.Query().Get("flash")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tpl[h.weightUnit(r.Context())][name].ExecuteTemplate(w, "layout.html", p); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("error: %v", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func (h *Handler) routes() {
	m := h.mux
	sub, _ := fs.Sub(staticFS, "static")
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(sub)))
	m.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		b, _ := staticFS.ReadFile("static/manifest.webmanifest")
		w.Write(b)
	})
	m.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-cache")
		b, _ := staticFS.ReadFile("static/sw.js")
		w.Write(b)
	})
	m.Handle("GET /media/", http.StripPrefix("/media/", cacheLong(http.FileServer(http.Dir(h.media)))))
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	m.HandleFunc("GET /{$}", h.home)
	m.HandleFunc("POST /workouts/start", h.startWorkout)
	m.HandleFunc("GET /log", h.logWorkout)
	m.HandleFunc("GET /log/add", h.logAdd)
	m.HandleFunc("POST /log/add", h.logAddExercise)
	m.HandleFunc("POST /log/finish", h.logFinish)
	m.HandleFunc("POST /log/cancel", h.logCancel)
	m.HandleFunc("POST /log/meta", h.logMeta)
	m.HandleFunc("POST /log/template", h.logSaveTemplate)
	m.HandleFunc("GET /history", h.history)
	m.HandleFunc("GET /workouts/{id}", h.workout)
	m.HandleFunc("POST /workouts/{id}/delete", h.workoutDelete)
	m.HandleFunc("GET /exercises", h.exercises)
	m.HandleFunc("GET /exercises/new", h.exerciseNew)
	m.HandleFunc("POST /exercises", h.exerciseCreate)
	m.HandleFunc("GET /exercises/{id}", h.exercise)
	m.HandleFunc("POST /exercises/{id}/archive", h.exerciseArchive)
	m.HandleFunc("GET /templates/{id}", h.template)
	m.HandleFunc("GET /templates/{id}/edit", h.templateEdit)
	m.HandleFunc("POST /templates/{id}", h.templateSave)
	m.HandleFunc("POST /templates/{id}/delete", h.templateDelete)
	m.HandleFunc("POST /folders", h.folderCreate)
	m.HandleFunc("GET /measure", h.measure)
	m.HandleFunc("GET /measure/{metric}", h.metric)
	m.HandleFunc("POST /measure/{metric}", h.metricAdd)
	m.HandleFunc("POST /measure/{metric}/{id}/delete", h.metricDelete)
	m.HandleFunc("GET /profile", h.profile)
	m.HandleFunc("POST /settings", h.settings)

	// JSON API for the live screen.
	m.HandleFunc("POST /api/sets/{id}", h.apiSetUpdate)
	m.HandleFunc("DELETE /api/sets/{id}", h.apiSetDelete)
	m.HandleFunc("POST /api/exercise-slots/{id}/sets", h.apiSetAdd)
	m.HandleFunc("POST /api/exercise-slots/{id}/warmups", h.apiWarmupsAdd)
	m.HandleFunc("POST /api/exercise-slots/{id}", h.apiSlotUpdate)
	m.HandleFunc("DELETE /api/exercise-slots/{id}", h.apiSlotDelete)
}

func cacheLong(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// ---- pages ----

type homeData struct {
	Folders []store.Folder
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	folders, err := h.s.ListFolders(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, "home.html", page{Title: "Workout", Tab: "workout", Data: homeData{Folders: folders}})
}

func (h *Handler) startWorkout(w http.ResponseWriter, r *http.Request) {
	var tpl *string
	if t := r.FormValue("template"); t != "" {
		tpl = &t
	}
	if _, err := h.s.StartWorkout(r.Context(), r.FormValue("name"), tpl, h.loc.String()); err != nil {
		if errors.Is(err, store.ErrInProgress) {
			http.Redirect(w, r, "/log?flash=A+workout+is+already+in+progress", http.StatusSeeOther)
			return
		}
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/log", http.StatusSeeOther)
}

type logData struct {
	Workout  store.Workout
	Previous map[string][]store.Set // by workout_exercise id
	RestDef  int
	Unit     weightUnit
	Plates   string // comma-separated kg, for the plate calculator
}

// defaultPlates is the per-side plate set the calculator assumes when the
// `plates` setting is unset.
const defaultPlates = "25,20,15,10,5,2.5,1.25"

func (h *Handler) logWorkout(w http.ResponseWriter, r *http.Request) {
	cur, err := h.s.CurrentWorkout(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	prev := map[string][]store.Set{}
	for _, we := range cur.Exercises {
		p, err := h.s.PreviousSets(r.Context(), we.ExerciseID, cur.ID)
		if err != nil {
			h.fail(w, err)
			return
		}
		prev[we.ID] = p
	}
	h.render(w, r, "log.html", page{Title: cur.Name, Tab: "workout", Current: &cur, NoBanner: true,
		Data: logData{Workout: cur, Previous: prev, RestDef: h.settingInt(r.Context(), "rest_s", 120),
			Unit: h.weightUnit(r.Context()), Plates: h.plates(r.Context())}})
}

// plates returns the `plates` setting normalised (trimmed, numeric,
// descending) or defaultPlates when unset/unparseable.
func (h *Handler) plates(ctx context.Context) string {
	v, _ := h.s.Setting(ctx, "plates")
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if f, err := strconv.ParseFloat(p, 64); err == nil && f > 0 {
			out = append(out, fmtNum(f))
		}
	}
	if len(out) == 0 {
		return defaultPlates
	}
	return strings.Join(out, ",")
}

func (h *Handler) settingInt(ctx context.Context, key string, def int) int {
	v, _ := h.s.Setting(ctx, key)
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

type pickerData struct {
	Exercises []store.Exercise
	Filter    store.ExerciseFilter
	BodyParts []string
	Cats      []string
	Return    string
}

var categories = []string{store.CatBarbell, store.CatDumbbell, store.CatMachineOther, store.CatBodyweight, store.CatAssistedBodyweight, store.CatRepsOnly, store.CatDuration, store.CatCardio}

func (h *Handler) picker(r *http.Request) (pickerData, error) {
	f := store.ExerciseFilter{Query: r.URL.Query().Get("q"), BodyPart: r.URL.Query().Get("body"), Category: r.URL.Query().Get("cat")}
	list, err := h.s.ListExercises(r.Context(), f)
	return pickerData{Exercises: list, Filter: f, BodyParts: store.BodyParts, Cats: categories}, err
}

func (h *Handler) logAdd(w http.ResponseWriter, r *http.Request) {
	d, err := h.picker(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	d.Return = "/log"
	h.render(w, r, "picker.html", page{Title: "Add exercise", Tab: "workout", Data: d})
}

func (h *Handler) logAddExercise(w http.ResponseWriter, r *http.Request) {
	cur, err := h.s.CurrentWorkout(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if _, err := h.s.AddWorkoutExercise(r.Context(), cur.ID, r.FormValue("exercise"), 3, true); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/log#bottom", http.StatusSeeOther)
}

func (h *Handler) logFinish(w http.ResponseWriter, r *http.Request) {
	cur, err := h.s.CurrentWorkout(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if cur.CompletedSets() == 0 {
		http.Redirect(w, r, "/log?flash=Complete+at+least+one+set+or+cancel", http.StatusSeeOther)
		return
	}
	fin, err := h.s.FinishWorkout(r.Context(), cur.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if r.FormValue("update_template") == "1" && fin.TemplateID != nil {
		if err := h.s.SaveWorkoutAsTemplate(r.Context(), fin.ID, *fin.TemplateID, nil, ""); err != nil {
			h.fail(w, err)
			return
		}
	}
	http.Redirect(w, r, "/workouts/"+fin.ID+"?flash=Workout+saved", http.StatusSeeOther)
}

func (h *Handler) logCancel(w http.ResponseWriter, r *http.Request) {
	cur, err := h.s.CurrentWorkout(r.Context())
	if err == nil {
		err = h.s.DeleteWorkout(r.Context(), cur.ID)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) logMeta(w http.ResponseWriter, r *http.Request) {
	cur, err := h.s.CurrentWorkout(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if err := h.s.UpdateWorkoutMeta(r.Context(), cur.ID, strings.TrimSpace(r.FormValue("name")), r.FormValue("notes")); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/log", http.StatusSeeOther)
}

func (h *Handler) logSaveTemplate(w http.ResponseWriter, r *http.Request) {
	cur, err := h.s.CurrentWorkout(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	var folder *string
	if f := r.FormValue("folder"); f != "" {
		folder = &f
	}
	if err := h.s.SaveWorkoutAsTemplate(r.Context(), cur.ID, "", folder, strings.TrimSpace(r.FormValue("name"))); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/log?flash=Template+saved", http.StatusSeeOther)
}

type monthGroup struct {
	Month    string
	Workouts []workoutRow
}

type workoutRow struct {
	store.Workout
	PRs int
}

type historyData struct {
	Months []monthGroup
	Total  int
	Page   int
	More   bool
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	const per = 30
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	list, err := h.s.ListWorkouts(r.Context(), per+1, pg*per)
	if err != nil {
		h.fail(w, err)
		return
	}
	total, _ := h.s.CountWorkouts(r.Context())
	d := historyData{Total: total, Page: pg, More: len(list) > per}
	if d.More {
		list = list[:per]
	}
	for _, wk := range list {
		prs, _ := h.s.WorkoutPRs(r.Context(), wk.ID)
		m := wk.StartedAt.In(h.loc).Format("January 2006")
		if len(d.Months) == 0 || d.Months[len(d.Months)-1].Month != m {
			d.Months = append(d.Months, monthGroup{Month: m})
		}
		g := &d.Months[len(d.Months)-1]
		g.Workouts = append(g.Workouts, workoutRow{Workout: wk, PRs: prs})
	}
	h.render(w, r, "history.html", page{Title: "History", Tab: "history", Data: d})
}

func (h *Handler) workout(w http.ResponseWriter, r *http.Request) {
	wk, err := h.s.GetWorkout(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if wk.InProgress() {
		http.Redirect(w, r, "/log", http.StatusSeeOther)
		return
	}
	prs, _ := h.s.WorkoutPRs(r.Context(), wk.ID)
	h.render(w, r, "workout.html", page{Title: wk.Name, Tab: "history", Data: workoutRow{Workout: wk, PRs: prs}})
}

func (h *Handler) workoutDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.s.DeleteWorkout(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/history?flash=Workout+deleted", http.StatusSeeOther)
}

func (h *Handler) exercises(w http.ResponseWriter, r *http.Request) {
	d, err := h.picker(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, "exercises.html", page{Title: "Exercises", Tab: "exercises", Data: d})
}

type exerciseData struct {
	store.Exercise
	Section string
	History []store.ExerciseHistoryEntry
	Records store.Records
	Charts  []chartData
}

type chartData struct {
	Title  string
	Points []store.Point
}

func (h *Handler) exercise(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e, err := h.s.GetExercise(ctx, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	d := exerciseData{Exercise: e, Section: r.URL.Query().Get("s")}
	switch d.Section {
	case "history":
		d.History, err = h.s.ExerciseHistory(ctx, e.ID, 50)
	case "records":
		d.Records, err = h.s.ExerciseRecords(ctx, e.ID)
	case "charts":
		for _, c := range []struct{ k, t string }{{"est_1rm", "Best set (est. 1RM)"}, {"weight_kg", "Best set (max weight)"}, {"volume_total", "Total volume (kg)"}, {"reps", "Best set (reps)"}} {
			pts, perr := h.s.ExerciseSeries(ctx, e.ID, c.k)
			if perr != nil {
				err = perr
				break
			}
			d.Charts = append(d.Charts, chartData{Title: c.t, Points: pts})
		}
	default:
		d.Section = "about"
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, "exercise.html", page{Title: e.Name, Tab: "exercises", Data: d})
}

func (h *Handler) exerciseNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "exercise_new.html", page{Title: "New exercise", Tab: "exercises", Data: pickerData{BodyParts: store.BodyParts, Cats: categories}})
}

func (h *Handler) exerciseCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/exercises/new?flash=Name+required", http.StatusSeeOther)
		return
	}
	var cells []string
	switch r.FormValue("cat") {
	case store.CatBarbell:
		cells = []string{store.CellBarbellWeight, store.CellReps, store.CellRPE}
	case store.CatDumbbell:
		cells = []string{store.CellDumbbellWeight, store.CellReps, store.CellRPE}
	case store.CatMachineOther:
		cells = []string{store.CellOtherWeight, store.CellReps, store.CellRPE}
	case store.CatBodyweight:
		cells = []string{store.CellWeightedBodyweight, store.CellReps, store.CellRPE}
	case store.CatAssistedBodyweight:
		cells = []string{store.CellAssistedBodyweight, store.CellReps, store.CellRPE}
	case store.CatDuration:
		cells = []string{store.CellDuration}
	case store.CatCardio:
		cells = []string{store.CellDistance, store.CellDuration}
	default:
		cells = []string{store.CellReps, store.CellRPE}
	}
	e := store.Exercise{ID: store.NewID(), Name: name, BodyPart: r.FormValue("body"), Category: r.FormValue("cat"), CellTypes: cells, Origin: "local"}
	if e.BodyPart == "" {
		e.BodyPart = "other"
	}
	if inst := strings.TrimSpace(r.FormValue("instructions")); inst != "" {
		for _, l := range strings.Split(inst, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				e.Instructions = append(e.Instructions, l)
			}
		}
	}
	if err := h.s.PutExercise(r.Context(), e); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/exercises/"+e.ID, http.StatusSeeOther)
}

func (h *Handler) exerciseArchive(w http.ResponseWriter, r *http.Request) {
	if err := h.s.SetExerciseArchived(r.Context(), r.PathValue("id"), r.FormValue("archived") != "0"); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/exercises/"+r.PathValue("id"), http.StatusSeeOther)
}

type templateData struct {
	store.Template
	Folders []store.Folder
	Picker  pickerData
}

func (h *Handler) template(w http.ResponseWriter, r *http.Request) {
	t, err := h.s.GetTemplate(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, "template.html", page{Title: t.Name, Tab: "workout", Data: templateData{Template: t}})
}

func (h *Handler) templateEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var t store.Template
	var err error
	if id := r.PathValue("id"); id == "new" {
		t = store.Template{ID: "new", Name: "New template"}
		if f := r.URL.Query().Get("folder"); f != "" {
			t.FolderID = &f
		}
	} else if t, err = h.s.GetTemplate(ctx, id); err != nil {
		h.fail(w, err)
		return
	}
	folders, err := h.s.ListFolders(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	pk, err := h.picker(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, "template_edit.html", page{Title: "Edit template", Tab: "workout", Data: templateData{Template: t, Folders: folders, Picker: pk}})
}

// templateSave takes the editor's JSON body: {name, folder, exercises:[{exercise, note, rest, sets:[{kind, weight, reps, duration, distance}]}]}.
func (h *Handler) templateSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string  `json:"name"`
		Folder    *string `json:"folder"`
		Notes     string  `json:"notes"`
		Exercises []struct {
			Exercise string `json:"exercise"`
			Note     string `json:"note"`
			Rest     *int   `json:"rest"`
			Sets     []struct {
				Kind     string   `json:"kind"`
				Weight   *float64 `json:"weight"`
				Reps     *int     `json:"reps"`
				Duration *float64 `json:"duration"`
				Distance *float64 `json:"distance"`
			} `json:"sets"`
		} `json:"exercises"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	t := store.Template{ID: id, Name: strings.TrimSpace(in.Name), Notes: in.Notes, FolderID: in.Folder, Origin: "local"}
	if in.Folder != nil && *in.Folder == "" {
		t.FolderID = nil
	}
	if id == "new" {
		t.ID = store.NewID()
	} else if old, err := h.s.GetTemplate(r.Context(), id); err == nil {
		t.Position = old.Position
	}
	if t.Name == "" {
		t.Name = "Template"
	}
	for _, e := range in.Exercises {
		te := store.TemplateExercise{ExerciseID: e.Exercise, Note: e.Note, RestS: e.Rest}
		for _, s := range e.Sets {
			te.Sets = append(te.Sets, store.Set{Kind: s.Kind, WeightKg: s.Weight, Reps: s.Reps, DurationS: s.Duration, DistanceM: s.Distance})
		}
		t.Exercises = append(t.Exercises, te)
	}
	if err := h.s.PutTemplate(r.Context(), t); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]string{"id": t.ID, "redirect": "/templates/" + t.ID})
}

func (h *Handler) templateDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.s.DeleteTemplate(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/?flash=Template+deleted", http.StatusSeeOther)
}

func (h *Handler) folderCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	folders, _ := h.s.ListFolders(r.Context())
	if err := h.s.PutFolder(r.Context(), store.Folder{ID: store.NewID(), Name: name, Position: len(folders)}); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type measureData struct {
	Latest map[string]store.MetricEntry
	Units  map[string]string
}

func (h *Handler) units(ctx context.Context) map[string]string {
	u := map[string]string{"weight": "kg", "percent": "%", "energy": "kcal", "length": "cm"}
	if v, _ := h.s.Setting(ctx, "length_unit"); v != "" {
		u["length"] = v
	}
	if v, _ := h.s.Setting(ctx, "weight_unit"); v != "" {
		u["weight"] = v
	}
	return u
}

func (h *Handler) measure(w http.ResponseWriter, r *http.Request) {
	latest, err := h.s.LatestMetrics(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	h.render(w, r, "measure.html", page{Title: "Measure", Tab: "measure", Data: measureData{Latest: latest, Units: h.units(r.Context())}})
}

type metricData struct {
	Key, Label, Unit string
	Entries          []store.MetricEntry
	Points           []store.Point
}

func metricMeta(key string) (label, unit string, ok bool) {
	for _, m := range store.Metrics {
		if m.Key == key {
			return m.Label, m.Unit, true
		}
	}
	return "", "", false
}

func (h *Handler) metric(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("metric")
	label, unit, ok := metricMeta(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	entries, err := h.s.MetricSeries(r.Context(), key, 200)
	if err != nil {
		h.fail(w, err)
		return
	}
	d := metricData{Key: key, Label: label, Unit: h.units(r.Context())[unit], Entries: entries}
	for i := len(entries) - 1; i >= 0; i-- {
		d.Points = append(d.Points, store.Point{At: entries[i].At, Value: entries[i].Value})
	}
	h.render(w, r, "metric.html", page{Title: label, Tab: "measure", Data: d})
}

func (h *Handler) metricAdd(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("metric")
	if _, _, ok := metricMeta(key); !ok {
		http.NotFound(w, r)
		return
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(r.FormValue("value"), ",", "."), 64)
	if err != nil {
		http.Redirect(w, r, "/measure/"+key+"?flash=Enter+a+number", http.StatusSeeOther)
		return
	}
	at := time.Now()
	if d := r.FormValue("date"); d != "" {
		if t, err := time.ParseInLocation("2006-01-02", d, h.loc); err == nil {
			at = t.Add(12 * time.Hour)
		}
	}
	if err := h.s.PutMetricEntry(r.Context(), store.MetricEntry{Metric: key, At: at, Value: v}); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/measure/"+key, http.StatusSeeOther)
}

func (h *Handler) metricDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.s.DeleteMetricEntry(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/measure/"+r.PathValue("metric"), http.StatusSeeOther)
}

type profileData struct {
	Workouts int
	Weeks    []store.Point
	Settings map[string]string
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, _ := h.s.CountWorkouts(ctx)
	weeks, err := h.s.WorkoutsPerWeek(ctx, 12)
	if err != nil {
		h.fail(w, err)
		return
	}
	set := map[string]string{}
	for _, k := range []string{"rest_s", "weight_unit", "length_unit", "first_weekday", "plates"} {
		set[k], _ = h.s.Setting(ctx, k)
	}
	h.render(w, r, "profile.html", page{Title: "Profile", Tab: "profile", Data: profileData{Workouts: n, Weeks: weeks, Settings: set}})
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, err)
		return
	}
	// Only keys the form submitted are written, so a submitted empty
	// value clears a setting back to its default.
	for _, k := range []string{"rest_s", "weight_unit", "length_unit", "first_weekday", "plates"} {
		if _, ok := r.Form[k]; ok {
			if err := h.s.SetSetting(r.Context(), k, strings.TrimSpace(r.FormValue(k))); err != nil {
				h.fail(w, err)
				return
			}
		}
	}
	http.Redirect(w, r, "/profile?flash=Saved", http.StatusSeeOther)
}

// ---- JSON API ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

type setJSON struct {
	ID        string   `json:"id"`
	Position  int      `json:"position"`
	Kind      string   `json:"kind"`
	Weight    *float64 `json:"weight"`
	Reps      *int     `json:"reps"`
	RPE       *float64 `json:"rpe"`
	Duration  *float64 `json:"duration"`
	Distance  *float64 `json:"distance"`
	Completed bool     `json:"completed"`
	Est1RM    float64  `json:"est_1rm"`
}

func toJSON(s store.Set) setJSON {
	return setJSON{ID: s.ID, Position: s.Position, Kind: s.Kind, Weight: s.WeightKg, Reps: s.Reps, RPE: s.RPE, Duration: s.DurationS, Distance: s.DistanceM, Completed: s.Completed, Est1RM: s.Est1RM()}
}

func (h *Handler) apiSetUpdate(w http.ResponseWriter, r *http.Request) {
	var in setJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st, _, err := h.s.GetSet(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	st.Kind, st.WeightKg, st.Reps, st.RPE, st.DurationS, st.DistanceM = in.Kind, in.Weight, in.Reps, in.RPE, in.Duration, in.Distance
	if st.Kind == "" {
		st.Kind = store.KindNormal
	}
	if in.Completed && !st.Completed {
		st.CompletedAt = nil
	}
	st.Completed = in.Completed
	if err := h.s.UpdateSet(r.Context(), st); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, toJSON(st))
}

func (h *Handler) apiSetDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.s.DeleteSet(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (h *Handler) apiSetAdd(w http.ResponseWriter, r *http.Request) {
	st, err := h.s.AddSet(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, toJSON(st))
}

// apiWarmupsAdd inserts a client-generated warm-up ramp in front of a
// slot's sets. The body is `[{kind:"warmup", weight, reps}]` in kg; the
// server does not compute the ramp (the client has the display unit and
// the plates), it only insists every row is a warm-up. Replies with the
// inserted sets in apiSetAdd's shape.
func (h *Handler) apiWarmupsAdd(w http.ResponseWriter, r *http.Request) {
	var in []setJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sets := make([]store.Set, 0, len(in))
	for _, s := range in {
		if s.Kind != store.KindWarmup {
			http.Error(w, "warmups: kind must be warmup", http.StatusBadRequest)
			return
		}
		sets = append(sets, store.Set{Kind: store.KindWarmup, WeightKg: s.Weight, Reps: s.Reps, DurationS: s.Duration, DistanceM: s.Distance})
	}
	out, err := h.s.InsertWarmups(r.Context(), r.PathValue("id"), sets)
	if err != nil {
		h.fail(w, err)
		return
	}
	res := make([]setJSON, 0, len(out))
	for _, st := range out {
		res = append(res, toJSON(st))
	}
	writeJSON(w, res)
}

func (h *Handler) apiSlotUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note     string `json:"note"`
		Rest     *int   `json:"rest"`
		Superset *int   `json:"superset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.s.UpdateWorkoutExercise(r.Context(), r.PathValue("id"), in.Note, in.Rest, in.Superset); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (h *Handler) apiSlotDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.s.RemoveWorkoutExercise(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- template helpers ----

// weightUnit is the display unit for weights. Storage is always kg
// (domain-model invariant 5); the unit only affects formatting and
// the /log input, whose JS converts back before POSTing.
type weightUnit string

const (
	unitKg weightUnit = "kg"
	unitLb weightUnit = "lb"

	lbPerKg = 2.2046226218
)

// weightUnit reads the `weight_unit` setting; kg unless it is exactly "lb".
func (h *Handler) weightUnit(ctx context.Context) weightUnit {
	if v, _ := h.s.Setting(ctx, "weight_unit"); v == string(unitLb) {
		return unitLb
	}
	return unitKg
}

// display converts a stored kg value into the unit. lb is rounded to one
// decimal; kg is returned untouched so stored values render verbatim.
func (u weightUnit) display(kg float64) float64 {
	if u == unitLb {
		return math.Round(kg*lbPerKg*10) / 10
	}
	return kg
}

func (u weightUnit) fmt(kg float64) string { return fmtNum(u.display(kg)) }

func (u weightUnit) fmtInt(kg float64) int { return int(math.Round(u.display(kg))) }

func (u weightUnit) fmtPtr(kg *float64) string {
	if kg == nil {
		return "-"
	}
	return u.fmt(*kg)
}

// step is the weight input's increment: the smallest common plate pair.
func (u weightUnit) step() string {
	if u == unitLb {
		return "2.5"
	}
	return "0.5"
}

func fmtNum(f float64) string {
	if f == math.Trunc(f) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func fmtKg(f *float64) string {
	if f == nil {
		return "-"
	}
	return fmtNum(*f)
}

func fmtDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func fmtSecs(f *float64) string {
	if f == nil {
		return "-"
	}
	s := int(*f)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (h *Handler) ago(s *string) string {
	if s == nil {
		return "-"
	}
	t, err := store.ParseTime(*s)
	if err != nil {
		return "-"
	}
	// Calendar days in the display zone, as Strong counts them.
	now := time.Now().In(h.loc)
	t = t.In(h.loc)
	days := int(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, h.loc).Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, h.loc)).Hours() / 24)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func derefi(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func setKindLabel(k string) string {
	switch k {
	case store.KindWarmup:
		return "W"
	case store.KindDrop:
		return "D"
	case store.KindFailure:
		return "F"
	}
	return ""
}

// bestSet picks the set with the highest est. 1RM (or weight, reps,
// duration, distance as fallbacks) among completed sets.
func bestSet(sets []store.Set) *store.Set {
	var best *store.Set
	score := func(s store.Set) float64 {
		if v := s.Est1RM(); v > 0 {
			return v
		}
		if s.WeightKg != nil {
			return *s.WeightKg
		}
		if s.DistanceM != nil {
			return *s.DistanceM / 1000
		}
		if s.DurationS != nil {
			return *s.DurationS / 3600
		}
		if s.Reps != nil {
			return float64(*s.Reps) / 1000
		}
		return 0
	}
	for i := range sets {
		if !sets[i].Completed || sets[i].Kind == store.KindWarmup {
			continue
		}
		if best == nil || score(sets[i]) > score(*best) {
			best = &sets[i]
		}
	}
	return best
}

// chartSVG renders a small responsive line chart.
func chartSVG(pts []store.Point) template.HTML {
	if len(pts) == 0 {
		return template.HTML(`<p class="muted">No data yet</p>`)
	}
	const W, H, pad = 320.0, 140.0, 24.0
	minV, maxV := pts[0].Value, pts[0].Value
	for _, p := range pts {
		minV = math.Min(minV, p.Value)
		maxV = math.Max(maxV, p.Value)
	}
	if maxV == minV {
		maxV = minV + 1
		minV = minV - 1
	}
	t0, t1 := pts[0].At, pts[len(pts)-1].At
	span := t1.Sub(t0).Seconds()
	if span == 0 {
		span = 1
	}
	x := func(t time.Time) float64 { return pad + (W-2*pad)*t.Sub(t0).Seconds()/span }
	y := func(v float64) float64 { return H - pad - (H-2*pad)*(v-minV)/(maxV-minV) }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %g %g" preserveAspectRatio="none">`, W, H)
	fmt.Fprintf(&b, `<text x="2" y="%g" class="axis">%s</text><text x="2" y="%g" class="axis">%s</text>`, pad, fmtNum(maxV), H-pad, fmtNum(minV))
	b.WriteString(`<polyline fill="none" stroke="currentColor" stroke-width="2" points="`)
	for _, p := range pts {
		fmt.Fprintf(&b, "%.1f,%.1f ", x(p.At), y(p.Value))
	}
	b.WriteString(`"/>`)
	for _, p := range pts {
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3"><title>%s: %s</title></circle>`, x(p.At), y(p.Value), p.At.Format("2 Jan 2006"), fmtNum(p.Value))
	}
	fmt.Fprintf(&b, `<text x="%g" y="%g" class="axis">%s</text><text x="%g" y="%g" class="axis" text-anchor="end">%s</text></svg>`,
		pad, H-4, t0.Format("Jan 06"), W-pad, H-4, t1.Format("Jan 06"))
	return template.HTML(b.String())
}
