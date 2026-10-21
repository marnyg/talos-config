package parent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Forge lists the repos a task may name (talos-config-rpxe): GET
// /api/v1/repos/search on Forgejo as the agent bot — everything the
// bot can see, each marked with whether it can push there (the
// `agents` org through the bots team, plus repos it was added to;
// public repos are visible but not pushable until then). The picker
// shows the pushable ones first and the rest under their own heading:
// a task on a read-only repo clones but cannot land its branch. The
// parent holds a read-only token for this (the write token stays
// provisioner policy, ADR-0037); the picker is a hint, the form still
// takes free text. Nil ⇒ no list.
type Forge struct {
	// URL is the forge's base (http://forgejo.forgejo.svc:3000); the
	// API is under URL/api/v1.
	URL string
	// Token is the bot's access token (read:repository suffices).
	Token string
	// TTL bounds how stale the list may be; 0 ⇒ a minute. A failed
	// refresh keeps serving the last good list (fail soft: the forms
	// must not depend on the forge being up).
	TTL time.Duration
	Log *slog.Logger

	// Client overrides the HTTP client (tests); nil ⇒ a 10 s one.
	Client *http.Client

	mu    sync.Mutex
	at    time.Time
	repos []Repo
}

// Repo is one entry of the picker.
type Repo struct {
	// Name is owner/name, what #task.repo carries.
	Name string `json:"full_name"`
	// Default is the default branch (the ref a task takes when unset).
	Default string `json:"default_branch"`
	// Push: the bot can push here, so a task's result branch and PR
	// can land. Off for repos the bot merely sees.
	Push bool `json:"push"`
}

// searchRepo is Forgejo's repository shape, the fields read.
type searchRepo struct {
	Name        string `json:"full_name"`
	Default     string `json:"default_branch"`
	Permissions struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

// repoName is `owner/name` as the child accepts it (child.repoRe);
// anything else the forge returns is not offered.
var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// forgePage is Forgejo's page size cap (MAX_RESPONSE_ITEMS default);
// forgePages bounds the walk — a bot with more repos than that is a
// different problem.
const (
	forgePage  = 50
	forgePages = 20
)

// Repos is the current list, refreshed past TTL; never errors — a
// failed refresh logs and returns what it had (possibly nothing).
func (f *Forge) Repos(ctx context.Context) []Repo {
	if f == nil || f.URL == "" || f.Token == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ttl := f.TTL
	if ttl == 0 {
		ttl = time.Minute
	}
	if !f.at.IsZero() && time.Since(f.at) < ttl {
		return f.repos
	}
	repos, err := f.fetch(ctx)
	if err != nil {
		f.log().Warn("forge: repos", "err", err, "stale", len(f.repos))
		// Back off at the same cadence rather than hammering a down forge.
		f.at = time.Now()
		return f.repos
	}
	f.repos, f.at = repos, time.Now()
	return f.repos
}

func (f *Forge) fetch(ctx context.Context) ([]Repo, error) {
	c := f.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	var all []Repo
	for page := 1; page <= forgePages; page++ {
		u := fmt.Sprintf("%s/api/v1/repos/search?limit=%d&page=%d", strings.TrimSuffix(f.URL, "/"), forgePage, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "token "+f.Token)
		req.Header.Set("Accept", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: %d %s", u, resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		var got struct {
			OK   bool         `json:"ok"`
			Data []searchRepo `json:"data"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return nil, fmt.Errorf("%s: decode: %w", u, err)
		}
		if !got.OK {
			return nil, fmt.Errorf("%s: ok=false", u)
		}
		for _, r := range got.Data {
			if repoName.MatchString(r.Name) {
				all = append(all, Repo{Name: r.Name, Default: r.Default, Push: r.Permissions.Push})
			}
		}
		if len(got.Data) < forgePage {
			break
		}
	}
	// Pushable first, then by name.
	sort.Slice(all, func(i, j int) bool {
		if all[i].Push != all[j].Push {
			return all[i].Push
		}
		return all[i].Name < all[j].Name
	})
	return all, nil
}

func (f *Forge) log() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}
