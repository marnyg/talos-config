// Command sap is the terminal client of the parent (talos-config-hp79):
// list, watch, steer and move from a shell — the same JSON API the web
// UI uses (actors/parent/web.go, webtasks.go), nothing else. Pure
// net/http: no iroh tag, so `go build ./...` compiles it.
//
//	sap login [-server URL]          browser approves, token lands here
//	sap me
//	sap tasks [-project P]            the board
//	sap task ID                       state, exits, log, sessions
//	sap move TASK STATE [-note TEXT]  one transition
//	sap sessions [-limit N]
//	sap session ID
//	sap watch SESSION [-after N] [-raw]   SSE tail; exits at `end`
//	sap file SESSION [-o PATH]        the stored session file
//	sap steer SESSION MESSAGE
//	sap abort SESSION
//
// -json on any read prints the API's body verbatim (the agent use
// case: another process reading a run post-mortem).
//
// The gate: /api/* takes `Authorization: Bearer <token>` where the
// token is the parent's own login cookie carried by hand. `sap login`
// gets one the gh-auth way — open the browser at /cli/authorize with
// an ephemeral 127.0.0.1 port and a nonce, the logged-in person
// approves, the parent redirects the token to the port. Stored 0600
// under $XDG_CONFIG_HOME/sap/config.json (SAP_SERVER / SAP_TOKEN
// override). It expires with the login (7 d); re-run login.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "sap", "config.json")
}

func loadConfig() config {
	var c config
	if b, err := os.ReadFile(configPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	if v := os.Getenv("SAP_SERVER"); v != "" {
		c.Server = v
	}
	if v := os.Getenv("SAP_TOKEN"); v != "" {
		c.Token = v
	}
	return c
}

func saveConfig(c config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

// client is one parent and one token.
type client struct {
	server string
	token  string
	http   *http.Client
}

func (c *client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.server, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, errors.New("401: not logged in or token expired — run `sap login`")
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%d: %s", resp.StatusCode, e.Error)
		}
		return nil, fmt.Errorf("%d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// get fetches path; with raw the body is written to stdout verbatim,
// otherwise decoded into v.
func (c *client) get(ctx context.Context, path string, raw bool, v any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if raw {
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (c *client) post(ctx context.Context, path string, body any, raw bool, v any) error {
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if raw {
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ---- the API's shapes, only what the printers need ------------------------

type principal struct {
	Wallet   string   `json:"wallet"`
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
	Exp      int64    `json:"exp"`
}

type session struct {
	ID      string `json:"id"`
	Owner   string `json:"owner"`
	Prompt  string `json:"prompt"`
	Repo    string `json:"repo"`
	Ref     string `json:"ref"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Task    string `json:"task"`
	Created int64  `json:"created"`
	Settled int64  `json:"settled"`
	PR      string `json:"pr"`
	Branch  string `json:"branch"`
	Reason  string `json:"reason"`
	File    string `json:"file"`
}

type task struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Owner   string `json:"owner"`
	Title   string `json:"title"`
	Goal    string `json:"goal"`
	Why     string `json:"why"`
	Repo    string `json:"repo"`
	Ref     string `json:"ref"`
	Flow    string `json:"flow"`
	State   string `json:"state"`
	Retries int    `json:"retries"`
	Session string `json:"session"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
}

type taskView struct {
	task
	GoalText string     `json:"goal_text"`
	Driver   string     `json:"driver"`
	Doc      string     `json:"doc"`
	Next     []string   `json:"next"`
	Terminal bool       `json:"terminal"`
	CanMove  bool       `json:"can_move"`
	Log      []logEntry `json:"log"`
	Children []task     `json:"children"`
	Sessions []session  `json:"sessions"`
}

type logEntry struct {
	At      int64  `json:"at"`
	From    string `json:"from"`
	To      string `json:"to"`
	By      string `json:"by"`
	Session string `json:"session"`
	Note    string `json:"note"`
}

type event struct {
	Seq  int64           `json:"seq"`
	At   int64           `json:"at"`
	Type string          `json:"type"`
	From string          `json:"from"`
	Raw  json.RawMessage `json:"raw"`
}

// ---- main ------------------------------------------------------------------

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "help" || os.Args[1] == "--help" {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	ctx := context.Background()
	cfg := loadConfig()

	if cmd == "login" {
		if err := login(ctx, cfg, args); err != nil {
			fail(err)
		}
		return
	}
	if cfg.Server == "" || cfg.Token == "" {
		fail(errors.New("not logged in — run `sap login -server http://agents.gw.mesh.internal`"))
	}
	c := &client{server: cfg.Server, token: cfg.Token, http: &http.Client{Timeout: 30 * time.Second}}

	var err error
	switch cmd {
	case "me":
		err = me(ctx, c, args)
	case "tasks":
		err = tasks(ctx, c, args)
	case "task":
		err = taskCmd(ctx, c, args)
	case "move":
		err = move(ctx, c, args)
	case "sessions":
		err = sessions(ctx, c, args)
	case "session":
		err = sessionCmd(ctx, c, args)
	case "watch":
		err = watch(ctx, c, args)
	case "file":
		err = file(ctx, c, args)
	case "steer":
		err = steer(ctx, c, args)
	case "abort":
		err = abort(ctx, c, args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `sap — the parent from a shell

  sap login [-server URL]
  sap me [-json]
  sap tasks [-project P] [-json]
  sap task ID [-json]
  sap move TASK STATE [-note TEXT] [-json]
  sap sessions [-limit N] [-json]
  sap session ID [-json]
  sap watch SESSION [-after N] [-raw]
  sap file SESSION [-o PATH]
  sap steer SESSION MESSAGE
  sap abort SESSION

Config: `+configPath()+` (SAP_SERVER, SAP_TOKEN override)
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sap:", err)
	os.Exit(1)
}

// flags parses fs over args and returns the positionals.
func flags(fs *flag.FlagSet, args []string, want int, names string) []string {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: sap %s %s [flags]\n", fs.Name(), names)
		fs.PrintDefaults()
	}
	// Positionals before flags are allowed: `sap task ID -json`.
	var pos []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	pos = append(pos, fs.Args()...)
	if len(pos) < want {
		fs.Usage()
		os.Exit(2)
	}
	return pos
}

// ---- login -----------------------------------------------------------------

func login(ctx context.Context, cfg config, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", cfg.Server, "the parent's URL as the browser reaches it")
	noOpen := fs.Bool("no-browser", false, "print the URL instead of opening it")
	flags(fs, args, 0, "")
	if *server == "" {
		return errors.New("-server is required the first time")
	}
	base, err := url.Parse(*server)
	if err != nil || base.Host == "" {
		return fmt.Errorf("-server %q: want a URL", *server)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	nonce := make([]byte, 18)
	rand.Read(nonce)
	state := base64.RawURLEncoding.EncodeToString(nonce)

	got := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state || q.Get("token") == "" {
			http.Error(w, "sap: state mismatch — run `sap login` again", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><title>sap</title><p>sap is logged in. You can close this tab.</p>")
		select {
		case got <- q.Get("token"):
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	u := *base
	u.Path = "/cli/authorize"
	u.RawQuery = url.Values{"port": {strconv.Itoa(port)}, "state": {state}}.Encode()
	fmt.Fprintln(os.Stderr, "sap: approve in the browser:", u.String())
	if !*noOpen {
		browse(u.String())
	}

	var token string
	select {
	case token = <-got:
	case <-time.After(5 * time.Minute):
		return errors.New("timed out waiting for the browser")
	}
	cfg.Server = strings.TrimRight(base.String(), "/")
	cfg.Token = token
	if err := saveConfig(cfg); err != nil {
		return err
	}
	c := &client{server: cfg.Server, token: cfg.Token, http: &http.Client{Timeout: 30 * time.Second}}
	var p principal
	if err := c.get(ctx, "/api/me", false, &p); err != nil {
		return err
	}
	fmt.Printf("logged in as %s (%s) until %s\n", p.Username, short(p.Wallet), time.Unix(p.Exp, 0).Format(time.RFC3339))
	return nil
}

// browse opens the system browser; a test swaps it for a client.
var browse = openBrowser

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "sap: could not open a browser:", err)
	}
}

// ---- reads -----------------------------------------------------------------

func me(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("me", flag.ContinueOnError)
	raw := fs.Bool("json", false, "print the API's body")
	flags(fs, args, 0, "")
	var p principal
	if err := c.get(ctx, "/api/me", *raw, &p); err != nil || *raw {
		return err
	}
	fmt.Printf("%s %s groups=%s exp=%s\n", p.Username, p.Wallet, strings.Join(p.Groups, ","), time.Unix(p.Exp, 0).Format(time.RFC3339))
	return nil
}

func tasks(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	raw := fs.Bool("json", false, "print the API's body")
	project := fs.String("project", "", "only this project")
	flags(fs, args, 0, "")
	path := "/api/tasks"
	if *project != "" {
		path += "?project=" + url.QueryEscape(*project)
	}
	var ts []task
	if err := c.get(ctx, path, *raw, &ts); err != nil || *raw {
		return err
	}
	for _, t := range ts {
		live := ""
		if t.Session != "" {
			live = " live=" + t.Session
		}
		fmt.Printf("%s  %-12s %-10s %s%s\n", t.ID, t.State, t.Project, t.Title, live)
	}
	return nil
}

func taskCmd(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("task", flag.ContinueOnError)
	raw := fs.Bool("json", false, "print the API's body")
	pos := flags(fs, args, 1, "ID")
	var t taskView
	if err := c.get(ctx, "/api/tasks/"+url.PathEscape(pos[0]), *raw, &t); err != nil || *raw {
		return err
	}
	fmt.Printf("%s  %s  [%s/%s driver=%s]\n", t.ID, t.Title, t.Flow, t.State, t.Driver)
	fmt.Printf("project=%s owner=%s created=%s", t.Project, short(t.Owner), ago(t.Created))
	if t.Repo != "" {
		fmt.Printf(" repo=%s", t.Repo)
		if t.Ref != "" {
			fmt.Printf("@%s", t.Ref)
		}
	}
	if t.Retries > 0 {
		fmt.Printf(" retries=%d", t.Retries)
	}
	fmt.Println()
	if t.GoalText != "" {
		fmt.Printf("goal: %s (%s)\n", t.GoalText, t.Goal)
	}
	if t.Why != "" {
		fmt.Printf("why:  %s\n", t.Why)
	}
	if t.Doc != "" {
		fmt.Printf("doc:  %s\n", t.Doc)
	}
	if len(t.Next) > 0 {
		fmt.Printf("next: %s", strings.Join(t.Next, " "))
		if !t.CanMove {
			fmt.Print(" (not yours to move)")
		}
		fmt.Println()
	}
	if t.Session != "" {
		fmt.Printf("live: %s\n", t.Session)
	}
	if len(t.Log) > 0 {
		fmt.Println("log:")
		for _, e := range t.Log {
			line := fmt.Sprintf("  %s  %s → %s  by %s", time.Unix(e.At, 0).Format("01-02 15:04:05"), e.From, e.To, short(e.By))
			if e.Session != "" {
				line += "  session=" + e.Session
			}
			fmt.Println(line)
			if e.Note != "" {
				fmt.Println("      " + strings.ReplaceAll(e.Note, "\n", "\n      "))
			}
		}
	}
	if len(t.Sessions) > 0 {
		fmt.Println("sessions:")
		for _, s := range t.Sessions {
			fmt.Printf("  %s  %-9s %s\n", s.ID, s.Status, sessionTail(s))
		}
	}
	if len(t.Children) > 0 {
		fmt.Println("children:")
		for _, ch := range t.Children {
			fmt.Printf("  %s  %-12s %s\n", ch.ID, ch.State, ch.Title)
		}
	}
	return nil
}

func sessions(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	raw := fs.Bool("json", false, "print the API's body")
	limit := fs.Int("limit", 0, "at most N")
	flags(fs, args, 0, "")
	path := "/api/sessions"
	if *limit > 0 {
		path += "?limit=" + strconv.Itoa(*limit)
	}
	var ss []session
	if err := c.get(ctx, path, *raw, &ss); err != nil || *raw {
		return err
	}
	for _, s := range ss {
		title := s.Title
		if title == "" {
			title = firstLine(s.Prompt)
		}
		fmt.Printf("%s  %-9s %-9s %s%s\n", s.ID, s.Status, ago(s.Created), title, sessionTail(s))
	}
	return nil
}

func sessionCmd(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	raw := fs.Bool("json", false, "print the API's body")
	pos := flags(fs, args, 1, "ID")
	var s session
	if err := c.get(ctx, "/api/sessions/"+url.PathEscape(pos[0]), *raw, &s); err != nil || *raw {
		return err
	}
	fmt.Printf("%s  %s  owner=%s created=%s", s.ID, s.Status, short(s.Owner), ago(s.Created))
	if s.Task != "" {
		fmt.Printf(" task=%s", s.Task)
	}
	if s.Repo != "" {
		fmt.Printf(" repo=%s@%s", s.Repo, s.Ref)
	}
	fmt.Println()
	if s.Title != "" {
		fmt.Println("title:", s.Title)
	}
	if s.PR != "" {
		fmt.Println("pr:", s.PR)
	} else if s.Branch != "" {
		fmt.Println("branch:", s.Branch)
	}
	if s.Reason != "" {
		fmt.Println("reason:", s.Reason)
	}
	if s.File != "" {
		fmt.Println("file: stored (sap file", s.ID+")")
	}
	fmt.Println("prompt:")
	fmt.Println("  " + strings.ReplaceAll(strings.TrimSpace(s.Prompt), "\n", "\n  "))
	return nil
}

func file(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("file", flag.ContinueOnError)
	out := fs.String("o", "-", "write here (- is stdout)")
	pos := flags(fs, args, 1, "SESSION")
	resp, err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(pos[0])+"/file", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	w := io.Writer(os.Stdout)
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	n, err := io.Copy(w, resp.Body)
	if err == nil && *out != "-" {
		fmt.Fprintf(os.Stderr, "sap: %d bytes → %s\n", n, *out)
	}
	return err
}

// ---- watch -----------------------------------------------------------------

// watch tails /api/sessions/{id}/stream. Without -raw it renders the
// way the web UI does (parent/ui/app.js): user and assistant messages,
// tool calls with their result's first line, steer/abort stamps, the
// settled line. -raw prints every event as `event SEQ TYPE RAW` — the
// cmd/spawn printer, one line per event, deltas included.
func watch(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	after := fs.Int64("after", 0, "start after this seq (0: from the beginning)")
	raw := fs.Bool("raw", false, "one line per event, unrendered")
	pos := flags(fs, args, 1, "SESSION")
	id := pos[0]

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			strings.TrimRight(c.server, "/")+"/api/sessions/"+url.PathEscape(id)+"/stream?after="+strconv.FormatInt(*after, 10), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := (&http.Client{}).Do(req) // no timeout: it is a tail
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			_, err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(id), nil) // a readable error
			if err != nil {
				return err
			}
			return fmt.Errorf("stream: %d", resp.StatusCode)
		}
		ended, err := sse(resp.Body, func(name string, data []byte) {
			if name == "end" {
				return
			}
			var ev event
			if json.Unmarshal(data, &ev) != nil {
				return
			}
			*after = ev.Seq
			if *raw {
				fmt.Printf("event %d %s %s\n", ev.Seq, ev.Type, bytes.TrimSpace(ev.Raw))
				return
			}
			render(ev)
		})
		resp.Body.Close()
		if ended {
			return nil
		}
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "sap: stream dropped, reconnecting:", err)
			time.Sleep(2 * time.Second)
			continue
		}
		return err
	}
}

// sse reads one event stream, calling on for each event; returns
// ended=true when the server sent `event: end`.
func sse(r io.Reader, on func(name string, data []byte)) (ended bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var name string
	var data bytes.Buffer
	flush := func() {
		if data.Len() > 0 || name != "" {
			on(name, data.Bytes())
		}
		name, data = "", bytes.Buffer{}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if name == "end" {
				return true, nil
			}
			flush()
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if name == "end" {
		return true, nil
	}
	return false, sc.Err()
}

// part is one piece of a pi message's content.
type part struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Content   json.RawMessage `json:"content"`
}

func parts(raw json.RawMessage) []part {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []part{{Type: "text", Text: s}}
	}
	var ps []part
	json.Unmarshal(raw, &ps)
	return ps
}

func render(ev event) {
	stamp := time.Unix(ev.At, 0).Format("15:04:05")
	switch ev.Type {
	case "message_end":
		var r struct {
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		json.Unmarshal(ev.Raw, &r)
		switch r.Message.Role {
		case "user":
			fmt.Printf("%s user:\n%s\n", stamp, indent(textOf(r.Message.Content)))
		case "assistant":
			fmt.Printf("%s assistant:\n", stamp)
			for _, p := range parts(r.Message.Content) {
				switch p.Type {
				case "text":
					fmt.Println(indent(p.Text))
				case "thinking":
					fmt.Println(indent("(thinking) " + firstLine(p.Thinking)))
				case "toolCall":
					fmt.Printf("  ▸ %s %s\n", p.Name, clip(string(p.Arguments), 200))
				}
			}
		}
	case "tool_execution_end":
		var r struct {
			ToolName string `json:"toolName"`
			IsError  bool   `json:"isError"`
			Result   struct {
				Content json.RawMessage `json:"content"`
			} `json:"result"`
		}
		json.Unmarshal(ev.Raw, &r)
		mark := "✓"
		if r.IsError {
			mark = "✗"
		}
		fmt.Printf("%s   %s %s — %s\n", stamp, mark, r.ToolName, clip(firstLine(textOf(r.Result.Content)), 160))
	case "steer":
		var r struct {
			Message string `json:"message"`
		}
		json.Unmarshal(ev.Raw, &r)
		fmt.Printf("%s steer by %s: %s\n", stamp, short(ev.From), r.Message)
	case "abort":
		fmt.Printf("%s abort by %s\n", stamp, short(ev.From))
	case "child_settled":
		var r struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
			Events int    `json:"events"`
			PR     string `json:"pr"`
			Branch string `json:"branch"`
		}
		json.Unmarshal(ev.Raw, &r)
		line := fmt.Sprintf("%s settled: %s events=%d", stamp, r.Status, r.Events)
		if r.Reason != "" {
			line += " — " + r.Reason
		}
		if r.PR != "" {
			line += " pr=" + r.PR
		} else if r.Branch != "" {
			line += " branch=" + r.Branch
		}
		fmt.Println(line)
	}
}

func textOf(content json.RawMessage) string {
	var sb strings.Builder
	for _, p := range parts(content) {
		if p.Type == "text" || p.Type == "" {
			sb.WriteString(p.Text)
		} else if p.Type == "toolResult" {
			sb.WriteString(textOf(p.Content))
		}
	}
	return sb.String()
}

// ---- writes ----------------------------------------------------------------

func move(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	raw := fs.Bool("json", false, "print the API's body")
	note := fs.String("note", "", "why")
	pos := flags(fs, args, 2, "TASK STATE")
	body := map[string]any{"to": pos[1]}
	if *note != "" {
		body["note"] = *note
	}
	var t task
	if err := c.post(ctx, "/api/tasks/"+url.PathEscape(pos[0])+"/transition", body, *raw, &t); err != nil || *raw {
		return err
	}
	fmt.Printf("%s → %s\n", t.ID, t.State)
	return nil
}

func steer(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("steer", flag.ContinueOnError)
	pos := flags(fs, args, 2, "SESSION MESSAGE")
	msg := strings.Join(pos[1:], " ")
	if err := c.post(ctx, "/api/sessions/"+url.PathEscape(pos[0])+"/steer", map[string]string{"message": msg}, false, nil); err != nil {
		return err
	}
	fmt.Println("steered", pos[0])
	return nil
}

func abort(ctx context.Context, c *client, args []string) error {
	fs := flag.NewFlagSet("abort", flag.ContinueOnError)
	pos := flags(fs, args, 1, "SESSION")
	if err := c.post(ctx, "/api/sessions/"+url.PathEscape(pos[0])+"/abort", map[string]string{}, false, nil); err != nil {
		return err
	}
	fmt.Println("aborted", pos[0])
	return nil
}

// ---- small -----------------------------------------------------------------

func sessionTail(s session) string {
	switch {
	case s.PR != "":
		return " pr=" + s.PR
	case s.Branch != "":
		return " branch=" + s.Branch
	case s.Reason != "":
		return " — " + clip(firstLine(s.Reason), 100)
	}
	return ""
}

func short(w string) string {
	if len(w) > 12 {
		return w[:6] + "…" + w[len(w)-4:]
	}
	if w == "" {
		return "?"
	}
	return w
}

func ago(at int64) string {
	if at == 0 {
		return "-"
	}
	d := time.Since(time.Unix(at, 0)).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
