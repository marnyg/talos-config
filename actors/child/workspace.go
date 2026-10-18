package child

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The git half of a task (ADR-0037, talos-config-e6pu.3): a #task that
// names a repo runs pi in a clone of it, and its result is a branch
// and a PR on the forge, not the transcript. Settled carries the
// pointer. Everything here is `git` as a subprocess plus the forge's
// REST API; the credential is the outfit's (provisioner policy, never
// the wire) and reaches git as an HTTP header through the subprocess
// environment only — no argv, no file on disk, and not in pi's
// environment (cmd/child scrubs it before pi inherits anything).

// Forge is the git host a workspace is cloned from and a result is
// pushed to: Forgejo's API shape (`/api/v1`), token auth. Zero ⇒ a
// #task naming a repo is refused.
type Forge struct {
	// URL is the forge's base (e.g. http://forgejo.forgejo.svc:3000):
	// clones go to URL/<owner>/<name>.git, the API under URL/api/v1.
	URL string
	// Token is the bot user's access token (write:repository +
	// write:issue).
	Token string
	// User is the bot user; "" ⇒ "agent". Names the commit author and
	// the basic-auth user on push.
	User string
	// CloneBase overrides where clones come from ("" ⇒ URL); tests
	// point it at a directory of bare repos.
	CloneBase string
	// Git is the git binary; "" ⇒ "git".
	Git string
}

func (f Forge) set() bool { return f.URL != "" && f.Token != "" }

func (f Forge) user() string {
	if f.User == "" {
		return "agent"
	}
	return f.User
}

func (f Forge) git() string {
	if f.Git == "" {
		return "git"
	}
	return f.Git
}

func (f Forge) cloneURL(repo string) string {
	base := f.CloneBase
	if base == "" {
		base = f.URL
	}
	return strings.TrimSuffix(base, "/") + "/" + repo + ".git"
}

// repoRe is `owner/name`: what Forgejo accepts in a URL path, nothing
// that could escape it (no dots-only segments, no slashes beyond one).
var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// resultTimeout bounds the commit/push/PR after pi is done — the
// deadline may already have passed (an aborted run still pushes what
// it has, as the shell entrypoint did on SIGTERM).
const resultTimeout = 60 * time.Second

// workspace is one task's clone.
type workspace struct {
	f      Forge
	repo   string // owner/name
	dir    string
	base   string // the branch cloned (PR base)
	branch string // agent/<task-id>
	log    *slog.Logger
}

// clone checks the request and clones repo@ref into root/<name>.
func clone(ctx context.Context, f Forge, root, repo, ref, id string, log *slog.Logger) (*workspace, error) {
	if !repoRe.MatchString(repo) || strings.Contains(repo, "..") {
		return nil, fmt.Errorf("repo %q: want owner/name", repo)
	}
	if !f.set() {
		return nil, errors.New("no forge: the outfit did not dress this child for git")
	}
	if root == "" {
		root = "."
	}
	dir := filepath.Join(root, path.Base(repo))
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%s exists", dir)
	}
	w := &workspace{f: f, repo: repo, dir: dir, branch: "agent/" + id, log: log}
	args := []string{"clone", "--quiet"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, f.cloneURL(repo), dir)
	if _, err := w.git(ctx, "", args...); err != nil {
		return nil, fmt.Errorf("clone: %w", err)
	}
	base, err := w.git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("clone: %w", err)
	}
	w.base = strings.TrimSpace(base)
	head, _ := w.git(ctx, dir, "rev-parse", "--short", "HEAD")
	log.Info("child: workspace", "dir", dir, "repo", repo, "base", w.base, "head", strings.TrimSpace(head), "branch", w.branch)
	return w, nil
}

// git runs one git command with the forge credential in its
// environment only: http.<URL>.extraHeader carries basic auth for
// pushes (and clones) to the forge; the committer identity is the bot.
func (w *workspace) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, w.f.git(), args...)
	cmd.Dir = dir
	auth := base64.StdEncoding.EncodeToString([]byte(w.f.user() + ":" + w.f.Token))
	cmd.Env = append(gitEnv(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=http."+strings.TrimSuffix(w.f.URL, "/")+"/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+auth,
		"GIT_CONFIG_KEY_1=user.name",
		"GIT_CONFIG_VALUE_1="+w.f.user(),
		"GIT_CONFIG_KEY_2=user.email",
		"GIT_CONFIG_VALUE_2="+w.f.user()+"@mesh.internal",
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
}

// gitEnv is the process environment without any GIT_CONFIG_* (ours
// replace them) and without forge secrets a caller may still hold.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_CONFIG_") || strings.HasPrefix(k, "FORGEJO_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// Pointer is where a task's result lives (ADR-0037): the branch and
// PR on the forge. Empty Branch ⇒ the run changed nothing and nothing
// was pushed.
type Pointer struct {
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	PR     string `json:"pr,omitempty"`
}

// result commits everything, pushes the branch (plain, never forced:
// a retry colliding on agent/<id> fails loudly) and opens the PR with
// body as its description. No diff ⇒ nothing pushed, zero Pointer.
func (w *workspace) result(ctx context.Context, title, body string) (Pointer, error) {
	p := Pointer{Repo: w.repo}
	if _, err := w.git(ctx, w.dir, "add", "-A"); err != nil {
		return p, err
	}
	if _, err := w.git(ctx, w.dir, "diff", "--cached", "--quiet"); err == nil {
		w.log.Info("child: result: no changes; nothing pushed", "dir", w.dir)
		return p, nil
	}
	if _, err := w.git(ctx, w.dir, "commit", "--quiet", "-m", "agent: "+w.branch[len("agent/"):], "-m", title); err != nil {
		return p, err
	}
	head, err := w.git(ctx, w.dir, "rev-parse", "HEAD")
	if err != nil {
		return p, err
	}
	p.Head = strings.TrimSpace(head)
	if _, err := w.git(ctx, w.dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+w.branch); err != nil {
		return p, err
	}
	p.Branch = w.branch
	w.log.Info("child: result: pushed", "branch", w.branch, "head", p.Head[:12])
	pr, err := w.openPR(ctx, title, body)
	if err != nil {
		return p, err
	}
	p.PR = pr
	w.log.Info("child: result: PR", "url", pr)
	return p, nil
}

// openPR is POST /api/v1/repos/<repo>/pulls; a 409 (the branch already
// has one) resolves to that PR's URL.
func (w *workspace) openPR(ctx context.Context, title, body string) (string, error) {
	api := strings.TrimSuffix(w.f.URL, "/") + "/api/v1/repos/" + w.repo + "/pulls"
	reqBody, _ := json.Marshal(map[string]string{"title": title, "head": w.branch, "base": w.base, "body": body})
	var created struct {
		HTMLURL string `json:"html_url"`
	}
	code, err := w.api(ctx, http.MethodPost, api, reqBody, &created)
	switch {
	case err != nil:
		return "", fmt.Errorf("pr: %w", err)
	case code == http.StatusCreated:
		return created.HTMLURL, nil
	case code == http.StatusConflict:
		var prs []struct {
			HTMLURL string `json:"html_url"`
			Head    struct {
				Ref string `json:"ref"`
			} `json:"head"`
		}
		if code, err := w.api(ctx, http.MethodGet, api+"?state=all", nil, &prs); err != nil || code != http.StatusOK {
			return "", fmt.Errorf("pr exists; listing: %v (HTTP %d)", err, code)
		}
		for _, pr := range prs {
			if pr.Head.Ref == w.branch {
				return pr.HTMLURL, nil
			}
		}
		return "", errors.New("pr exists but was not found in the list")
	default:
		return "", fmt.Errorf("pr: HTTP %d", code)
	}
}

func (w *workspace) api(ctx context.Context, method, url string, body []byte, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "token "+w.f.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := forgeClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 == 2 && out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// forgeClient bounds each API call on its own, under resultTimeout.
var forgeClient = &http.Client{Timeout: 15 * time.Second}

// taskTitle is the task's title when it has one, else the prompt's
// first non-empty line, bounded (a long first line makes a long PR
// title: name the task).
func taskTitle(title, prompt string) string {
	if t := strings.TrimSpace(title); t != "" {
		if len(t) > 200 {
			t = t[:200]
		}
		return "agent: " + t
	}
	for _, l := range strings.Split(prompt, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return "agent: " + l
		}
	}
	return "agent task"
}

// assistantText is the text of a message_end record whose message is
// the assistant's; ok=false for anything else. The last one seen is
// pi's final message — the PR body.
func assistantText(raw []byte) (string, bool) {
	var rec struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &rec) != nil || rec.Type != "message_end" || rec.Message.Role != "assistant" {
		return "", false
	}
	var sb strings.Builder
	for _, c := range rec.Message.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), true
}

// prBody is the report: pi's final message, or a note that there was
// none, with the task's trailer.
func prBody(final, id, status string) string {
	if final == "" {
		final = "No final message from pi."
	}
	return fmt.Sprintf("%s\n\n---\nTask %s, %s, %s\n", final, id, status, time.Now().UTC().Format(time.RFC3339))
}
