// Package strongimport loads a Strong export (the REST API's HAL JSON,
// see ~/strong-export-<date>/README.md) into the store: the exercise
// library with its media, folders, templates and the workout log.
// Idempotent — rows keep Strong's ids, so re-running updates in place.
//
// Layout expected under dir:
//
//	api/measurements*.json           user-scoped exercises (global + custom)
//	api/global-_api_measurements*.json  the global library (optional)
//	api/folders.json  api/templates.json  api/logs.json
//	media/<file>                     downloaded files.strong.app objects
package strongimport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marnyg/talos-config/workouts/store"
)

// HAL envelopes.

type link struct {
	Href string `json:"href"`
}

type links map[string]json.RawMessage // a link or a list of links

func (l links) one(key string) string {
	raw, ok := l[key]
	if !ok {
		return ""
	}
	var single link
	if err := json.Unmarshal(raw, &single); err == nil && single.Href != "" {
		return single.Href
	}
	var many []link
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		return many[0].Href
	}
	return ""
}

func lastSeg(href string) string { return path.Base(href) }

type name map[string]string

func (n name) String() string {
	for _, k := range []string{"en", "custom"} {
		if v := strings.TrimSpace(n[k]); v != "" {
			return v
		}
	}
	for _, v := range n {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

type cell struct {
	CellType string `json:"cellType"`
	Value    string `json:"value"`
}

type cellSet struct {
	ID          string `json:"id"`
	Cells       []cell `json:"cells"`
	IsCompleted bool   `json:"isCompleted"`
	CellSetTag  string `json:"cellSetTag"`
}

type cellSetGroup struct {
	ID       string    `json:"id"`
	Links    links     `json:"_links"`
	CellSets []cellSet `json:"cellSets"`
}

type measurement struct {
	ID           string            `json:"id"`
	Links        links             `json:"_links"`
	Name         name              `json:"name"`
	Instructions map[string]string `json:"instructions"`
	Media        []struct {
		URL         string `json:"url"`
		ContentType string `json:"contentType"`
	} `json:"media"`
	CellTypeConfigs []struct {
		CellType  string `json:"cellType"`
		Mandatory bool   `json:"mandatory"`
	} `json:"cellTypeConfigs"`
	IsGlobal        bool   `json:"isGlobal"`
	MeasurementType string `json:"measurementType"`
}

type folder struct {
	ID       string `json:"id"`
	Name     name   `json:"name"`
	Index    int    `json:"index"`
	IsGlobal bool   `json:"isGlobal"`
}

type log struct {
	ID        string `json:"id"`
	Links     links  `json:"_links"`
	Name      name   `json:"name"`
	Index     int    `json:"index"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Timezone  string `json:"timezoneId"`
	LogType   string `json:"logType"`
	Embedded  struct {
		Groups []cellSetGroup `json:"cellSetGroup"`
	} `json:"_embedded"`
}

type page[T any] struct {
	Total    int            `json:"total"`
	Embedded map[string][]T `json:"_embedded"`
}

func readPages[T any](dir, glob string) ([]T, error) {
	files, err := filepath.Glob(filepath.Join(dir, glob))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []T
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var p page[T]
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, items := range p.Embedded {
			out = append(out, items...)
		}
	}
	return out, nil
}

// Report summarises an import.
type Report struct {
	Exercises, MediaCopied, MediaMissing, Folders, Templates, Workouts, Sets int
	Skipped                                                                  []string
}

// Options tunes Import.
type Options struct {
	// MediaDir receives copies of media/ files; "" skips media.
	MediaDir string
	// Log receives progress lines; nil for silent.
	Log io.Writer
}

// Import loads the export under dir into s.
func Import(ctx context.Context, s *store.Store, dir string, opt Options) (Report, error) {
	var r Report
	logf := func(format string, a ...any) {
		if opt.Log != nil {
			fmt.Fprintf(opt.Log, format+"\n", a...)
		}
	}

	// 1. Exercises: global library first, then the user's view (which
	//    repeats the globals and adds customs) — later wins.
	glob, err := readPages[measurement](filepath.Join(dir, "api"), "global-_api_measurements*.json")
	if err != nil {
		return r, err
	}
	user, err := readPages[measurement](filepath.Join(dir, "api"), "measurements*.json")
	if err != nil {
		return r, err
	}
	byID := map[string]measurement{}
	for _, m := range append(glob, user...) {
		if m.MeasurementType != "" && m.MeasurementType != "EXERCISE" {
			continue
		}
		byID[m.ID] = m
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := byID[id]
		e, media := convertExercise(m)
		if e.Name == "" {
			r.Skipped = append(r.Skipped, "exercise "+id+": no name")
			continue
		}
		if opt.MediaDir != "" {
			for _, md := range media {
				src := filepath.Join(dir, "media", md.File)
				if _, err := os.Stat(src); err != nil {
					r.MediaMissing++
					continue
				}
				if err := copyFile(src, filepath.Join(opt.MediaDir, md.File)); err != nil {
					return r, err
				}
				r.MediaCopied++
				e.Media = append(e.Media, md)
			}
		} else {
			e.Media = media
		}
		if err := s.PutExercise(ctx, e); err != nil {
			return r, fmt.Errorf("exercise %s: %w", e.Name, err)
		}
		r.Exercises++
	}
	logf("exercises: %d (media copied %d, missing %d)", r.Exercises, r.MediaCopied, r.MediaMissing)

	// 2. Folders.
	folders, err := readPages[folder](filepath.Join(dir, "api"), "folders.json")
	if err != nil {
		return r, err
	}
	for _, f := range folders {
		origin := "local"
		if f.IsGlobal {
			origin = "strong"
		}
		pos := f.Index
		if pos < 0 {
			pos = 1000 + pos // Strong puts examples at -1: after the user's
		}
		if err := s.PutFolder(ctx, store.Folder{ID: f.ID, Name: f.Name.String(), Position: pos, Origin: origin}); err != nil {
			return r, err
		}
		r.Folders++
	}

	// 3. Templates.
	tpls, err := readPages[log](filepath.Join(dir, "api"), "templates.json")
	if err != nil {
		return r, err
	}
	for _, t := range tpls {
		tpl := store.Template{ID: t.ID, Name: t.Name.String(), Position: t.Index, Origin: "strong"}
		if fid := lastSeg(t.Links.one("folder")); fid != "" && fid != "." {
			tpl.FolderID = &fid
		}
		for _, g := range t.Embedded.Groups {
			te, note, skip := convertGroup(g, byID, false)
			if skip != "" {
				r.Skipped = append(r.Skipped, "template "+tpl.Name+": "+skip)
				continue
			}
			tpl.Exercises = append(tpl.Exercises, store.TemplateExercise{ID: te.ID, ExerciseID: te.ExerciseID, Note: note, RestS: te.RestS, Sets: te.Sets})
		}
		if err := s.PutTemplate(ctx, tpl); err != nil {
			return r, fmt.Errorf("template %s: %w", tpl.Name, err)
		}
		r.Templates++
	}
	logf("folders: %d, templates: %d", r.Folders, r.Templates)

	// 4. Workouts.
	logs, err := readPages[log](filepath.Join(dir, "api"), "logs.json")
	if err != nil {
		return r, err
	}
	for _, l := range logs {
		if l.LogType != "" && l.LogType != "WORKOUT" {
			continue
		}
		start, err := time.Parse(time.RFC3339Nano, l.StartDate)
		if err != nil {
			r.Skipped = append(r.Skipped, "workout "+l.ID+": bad startDate")
			continue
		}
		w := store.Workout{ID: l.ID, Name: l.Name.String(), StartedAt: start, Timezone: l.Timezone, Origin: "strong"}
		if end, err := time.Parse(time.RFC3339Nano, l.EndDate); err == nil {
			w.EndedAt = &end
		} else {
			w.EndedAt = &start
		}
		if tid := lastSeg(l.Links.one("template")); tid != "" && tid != "." {
			w.TemplateID = &tid
		}
		for _, g := range l.Embedded.Groups {
			we, note, skip := convertGroup(g, byID, true)
			if skip != "" {
				r.Skipped = append(r.Skipped, "workout "+w.Name+" "+l.StartDate[:10]+": "+skip)
				continue
			}
			we.Note = note
			if len(we.Sets) == 0 {
				continue
			}
			w.Exercises = append(w.Exercises, we)
			r.Sets += len(we.Sets)
		}
		if w.TemplateID != nil {
			if _, err := s.GetTemplate(ctx, *w.TemplateID); err != nil {
				w.TemplateID = nil // deleted template
			}
		}
		if err := s.PutWorkout(ctx, w); err != nil {
			return r, fmt.Errorf("workout %s: %w", w.Name, err)
		}
		r.Workouts++
	}
	logf("workouts: %d, sets: %d, skipped: %d", r.Workouts, r.Sets, len(r.Skipped))
	return r, nil
}

func convertExercise(m measurement) (store.Exercise, []store.Media) {
	e := store.Exercise{ID: m.ID, Name: m.Name.String(), Origin: "local"}
	if m.IsGlobal {
		e.Origin = "strong"
	}
	e.BodyPart = lastSeg(m.Links.one("tag"))
	if e.BodyPart == "." || e.BodyPart == "" {
		e.BodyPart = "other"
	}
	for _, c := range m.CellTypeConfigs {
		if c.Mandatory || c.CellType == store.CellRPE {
			e.CellTypes = append(e.CellTypes, c.CellType)
		}
	}
	if len(e.CellTypes) == 0 {
		for _, c := range m.CellTypeConfigs {
			e.CellTypes = append(e.CellTypes, c.CellType)
		}
	}
	e.Category = store.CategoryFor(e.CellTypes)
	if e.BodyPart == "cardio" && e.Category != store.CatCardio && !e.HasWeight() {
		e.Category = store.CatCardio
	}
	if inst := strings.TrimSpace(m.Instructions["en"]); inst != "" {
		for _, step := range strings.Split(inst, "||") {
			if step = strings.TrimSpace(step); step != "" {
				e.Instructions = append(e.Instructions, step)
			}
		}
	}
	var media []store.Media
	for _, md := range m.Media {
		u, err := url.Parse(md.URL)
		if err != nil {
			continue
		}
		file := path.Base(u.Path)
		if dec, err := url.PathUnescape(file); err == nil {
			file = dec
		}
		kind := "image"
		switch {
		case strings.HasPrefix(md.ContentType, "video/"):
			kind = "video"
		case strings.HasPrefix(file, "THU"):
			kind = "thumb"
		}
		media = append(media, store.Media{Kind: kind, File: file, ContentType: md.ContentType})
	}
	return e, media
}

// convertGroup turns a cellSetGroup into a WorkoutExercise (sets with
// Completed) or, with completedOnly=false, template rows. The third
// return is a skip reason ("" = ok).
func convertGroup(g cellSetGroup, known map[string]measurement, completedOnly bool) (store.WorkoutExercise, string, string) {
	exID := lastSeg(g.Links.one("measurement"))
	we := store.WorkoutExercise{ID: g.ID, ExerciseID: exID}
	if _, ok := known[exID]; !ok {
		return we, "", "unknown exercise " + exID
	}
	var note string
	for _, cs := range g.CellSets {
		st := store.Set{ID: cs.ID, Kind: store.KindNormal, Completed: cs.IsCompleted}
		switch cs.CellSetTag {
		case "WARM_UP":
			st.Kind = store.KindWarmup
		case "DROP_SET":
			st.Kind = store.KindDrop
		case "FAILURE":
			st.Kind = store.KindFailure
		}
		isSet := false
		for _, c := range cs.Cells {
			v := strings.TrimSpace(c.Value)
			switch c.CellType {
			case "REST_TIMER":
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					n := int(f)
					we.RestS = &n
				}
			case "NOTE":
				if v != "" {
					if note != "" {
						note += "\n"
					}
					note += v
				}
			case store.CellReps:
				if n, err := strconv.Atoi(v); err == nil {
					st.Reps = &n
					isSet = true
				}
			case store.CellRPE:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					st.RPE = &f
				}
			case store.CellDuration:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					st.DurationS = &f
					isSet = true
				}
			case store.CellDistance:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					st.DistanceM = &f
					isSet = true
				}
			case store.CellBarbellWeight, store.CellDumbbellWeight, store.CellOtherWeight, store.CellWeightedBodyweight, store.CellAssistedBodyweight:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					st.WeightKg = &f
					isSet = true
				}
			}
		}
		if !isSet {
			continue
		}
		if completedOnly && !cs.IsCompleted {
			continue
		}
		we.Sets = append(we.Sets, st)
	}
	return we, note, ""
}

func copyFile(src, dst string) error {
	if fi, err := os.Stat(dst); err == nil {
		if sfi, err := os.Stat(src); err == nil && sfi.Size() == fi.Size() {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
