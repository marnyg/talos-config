package pirpc_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/pirpc"
)

// The fake pi is this test binary re-executed with PIRPC_FAKE_PI=1.
// It speaks the documented RPC surface (docs/rpc.md, rpc-commands.md):
// strict JSONL on both sides, one response per command correlated by
// id, a canned run after a prompt, get_state with a session file, and
// exit 0 on stdin EOF. Knobs via env (see fakeMain).
func TestMain(m *testing.M) {
	if os.Getenv("PIRPC_FAKE_PI") == "1" {
		fakeMain()
		return
	}
	os.Exit(m.Run())
}

const fakeSessionFile = "/tmp/fake-pi/session.jsonl"

func fakeMain() {
	out := bufio.NewWriter(os.Stdout)
	emit := func(s string) {
		out.WriteString(s)
		out.WriteByte('\n')
	}
	flush := func() { out.Flush() }
	fmt.Fprintln(os.Stderr, "fake pi: hello")

	flood, _ := strconv.Atoi(os.Getenv("PIRPC_FAKE_FLOOD"))
	dieOn := os.Getenv("PIRPC_FAKE_DIE_ON")

	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(line) > 0 {
			var cmd struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if jerr := json.Unmarshal(line, &cmd); jerr != nil {
				emit(`{"type":"response","command":"parse","success":false,"error":"bad json"}`)
				flush()
				continue
			}
			if cmd.Type == dieOn {
				flush()
				os.Exit(3)
			}
			echo, _ := json.Marshal(string(line))
			emit(`{"type":"fake_received","line":` + string(echo) + `}`)
			switch cmd.Type {
			case "prompt":
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"prompt","success":true,"data":{"disposition":"started"}}`)
				emit(`{"type":"agent_start"}`)
				emit(`{"type":"turn_start"}`)
				emit(`{"type":"message_start"}`)
				if os.Getenv("PIRPC_FAKE_U2028") == "1" {
					// Literal U+2028 (E2 80 A8) inside the string, as
					// pi may emit it; NOT escaped.
					emit("{\"type\":\"message_update\",\"assistantMessageEvent\":{\"type\":\"text_delta\",\"delta\":\"a\u2028b\u2029c\"}}")
				} else if flood > 0 {
					for i := range flood {
						emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"chunk ` + strconv.Itoa(i) + `"}}`)
					}
				} else {
					emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`)
				}
				emit(`{"type":"message_end"}`)
				emit(`{"type":"turn_end"}`)
				emit(`{"type":"agent_end","messages":[],"willRetry":false}`)
				emit(`{"type":"agent_settled"}`)
			case "steer":
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"steer","success":true,"data":{"disposition":"queued"}}`)
			case "abort":
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"abort","success":true}`)
			case "get_state":
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"get_state","success":true,"data":{"model":{"id":"fake"},"thinkingLevel":"medium","isStreaming":false,"isCompacting":false,"sessionFile":"` + fakeSessionFile + `","sessionId":"abc123","sessionName":"","messageCount":0,"pendingMessageCount":0}}`)
			default:
				emit(`{"id":"` + cmd.ID + `","type":"response","command":"` + cmd.Type + `","success":false,"error":"unknown command"}`)
			}
			flush()
		}
		if err != nil {
			break
		}
	}
	flush()
	fmt.Fprintln(os.Stderr, "fake pi: bye")
	if os.Getenv("PIRPC_FAKE_IGNORE_EOF") == "1" {
		time.Sleep(time.Hour) // a pi that will not shut down
	}
	os.Exit(0)
}

// fakeEnv is the fake's environment. GORACE=atexit_sleep_ms=0: under
// -race the re-exec'd test binary otherwise sleeps 1 s at exit, which
// would make every Close look slow.
func fakeEnv(extra ...string) []string {
	env := append(os.Environ(), "PIRPC_FAKE_PI=1", "GORACE=atexit_sleep_ms=0")
	return append(env, extra...)
}

func start(t *testing.T, env ...string) (*pirpc.Client, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	c, err := pirpc.Start(context.Background(), os.Args[0], pirpc.Options{
		Env:       fakeEnv(env...),
		Stderr:    &stderr,
		Buffer:    1024,
		KillAfter: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, &stderr
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// untilSettled drains Events up to and including agent_settled.
func untilSettled(t *testing.T, c *pirpc.Client) []pirpc.Record {
	t.Helper()
	var recs []pirpc.Record
	timeout := time.After(5 * time.Second)
	for {
		select {
		case r, ok := <-c.Events():
			if !ok {
				t.Fatalf("events closed before agent_settled; got %d records", len(recs))
			}
			recs = append(recs, r)
			if r.Type == "agent_settled" {
				return recs
			}
		case <-timeout:
			t.Fatalf("no agent_settled after %d records", len(recs))
		}
	}
}

func types(recs []pirpc.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Type
	}
	return out
}

func TestPromptOrderedEventsSettled(t *testing.T) {
	c, _ := start(t)
	resp, err := c.Prompt(ctxT(t), "Hello")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Command != "prompt" || resp.Disposition() != "started" {
		t.Fatalf("prompt response: %+v", resp)
	}
	recs := untilSettled(t, c)
	want := "fake_received response agent_start turn_start message_start message_update message_end turn_end agent_end agent_settled"
	if got := strings.Join(types(recs), " "); got != want {
		t.Fatalf("event order\n got: %s\nwant: %s", got, want)
	}
	// Raw is the whole line, untouched; the response is on Events too.
	if recs[1].ID != resp.ID || !bytes.Contains(recs[1].Raw, []byte(`"disposition":"started"`)) {
		t.Fatalf("response record: %s", recs[1].Raw)
	}
	for _, r := range recs {
		if bytes.ContainsAny(r.Raw, "\n\r") || !json.Valid(r.Raw) {
			t.Fatalf("raw record not one clean JSON line: %q", r.Raw)
		}
	}
}

func TestSteerAbortWire(t *testing.T) {
	c, _ := start(t)
	ctx := ctxT(t)
	steer, err := c.Steer(ctx, "look left")
	if err != nil || !steer.Success || steer.Disposition() != "queued" {
		t.Fatalf("steer: %+v %v", steer, err)
	}
	abort, err := c.Abort(ctx)
	if err != nil || !abort.Success || abort.Command != "abort" {
		t.Fatalf("abort: %+v %v", abort, err)
	}
	var lines []string
	for range 4 {
		r := <-c.Events()
		if r.Type == "fake_received" {
			var e struct {
				Line string `json:"line"`
			}
			if err := json.Unmarshal(r.Raw, &e); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, e.Line)
		}
	}
	want := []string{
		`{"id":"` + steer.ID + `","type":"steer","message":"look left"}`,
		`{"id":"` + abort.ID + `","type":"abort"}`,
	}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("stdin lines\n got: %q\nwant: %q", lines, want)
	}
}

func TestState(t *testing.T) {
	c, _ := start(t)
	s, err := c.State(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionFile != fakeSessionFile || s.SessionID != "abc123" || s.IsStreaming {
		t.Fatalf("state: %+v", s)
	}
}

func TestCloseStdinDone(t *testing.T) {
	c, stderr := start(t)
	if _, err := c.Prompt(ctxT(t), "x"); err != nil {
		t.Fatal(err)
	}
	untilSettled(t, c)
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done not closed after Close")
	}
	// Events drains to closed; stderr passed through; commands refused.
	for range c.Events() {
	}
	if !strings.Contains(stderr.String(), "fake pi: bye") {
		t.Fatalf("stderr passthrough: %q", stderr.String())
	}
	if _, err := c.Prompt(ctxT(t), "y"); !errors.Is(err, pirpc.ErrClosed) && !errors.Is(err, pirpc.ErrExited) {
		t.Fatalf("prompt after close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestContextCancelShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, err := pirpc.Start(ctx, os.Args[0], pirpc.Options{
		Env:       fakeEnv(),
		Stderr:    io.Discard,
		KillAfter: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit on ctx cancel")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close after cancel: %v", err)
	}
}

func TestCloseKillsStuckPi(t *testing.T) {
	var stderr bytes.Buffer
	c, err := pirpc.Start(context.Background(), os.Args[0], pirpc.Options{
		Env:       fakeEnv("PIRPC_FAKE_IGNORE_EOF=1"),
		Stderr:    &stderr,
		KillAfter: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Close must take ~KillAfter: the fake sees EOF (stderr "bye"),
	// then refuses to exit until killed.
	t0 := time.Now()
	err = c.Close()
	if d := time.Since(t0); d < 300*time.Millisecond || d > 3*time.Second {
		t.Fatalf("close took %v, want ~KillAfter", d)
	}
	if !strings.Contains(stderr.String(), "fake pi: bye") {
		t.Fatalf("fake never saw EOF: %q", stderr.String())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("close of killed pi: %v", err)
	}
}

func TestU2028IsOneRecord(t *testing.T) {
	c, _ := start(t, "PIRPC_FAKE_U2028=1")
	if _, err := c.Prompt(ctxT(t), "x"); err != nil {
		t.Fatal(err)
	}
	recs := untilSettled(t, c)
	if len(recs) != 10 {
		t.Fatalf("got %d records, want 10: %v", len(recs), types(recs))
	}
	var upd struct {
		Ev struct {
			Delta string `json:"delta"`
		} `json:"assistantMessageEvent"`
	}
	if err := json.Unmarshal(recs[5].Raw, &upd); err != nil {
		t.Fatalf("message_update not one record: %v: %q", err, recs[5].Raw)
	}
	if upd.Ev.Delta != "a\u2028b\u2029c" {
		t.Fatalf("delta %q", upd.Ev.Delta)
	}
}

func TestFloodWithSleepingConsumer(t *testing.T) {
	const n = 2000
	c, _ := start(t, "PIRPC_FAKE_FLOOD="+strconv.Itoa(n))
	if _, err := c.Prompt(ctxT(t), "x"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	recs := untilSettled(t, c)
	updates := 0
	last := -1
	for _, r := range recs {
		if r.Type != "message_update" {
			continue
		}
		var upd struct {
			Ev struct {
				Delta string `json:"delta"`
			} `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(r.Raw, &upd); err != nil {
			t.Fatal(err)
		}
		i, _ := strconv.Atoi(strings.TrimPrefix(upd.Ev.Delta, "chunk "))
		if i != last+1 {
			t.Fatalf("out of order: %d after %d", i, last)
		}
		last = i
		updates++
	}
	if updates != n {
		t.Fatalf("got %d message_update, want %d", updates, n)
	}
}

func TestProcessDiesMidCommand(t *testing.T) {
	c, _ := start(t, "PIRPC_FAKE_DIE_ON=prompt")
	_, err := c.Prompt(ctxT(t), "x")
	if !errors.Is(err, pirpc.ErrExited) {
		t.Fatalf("prompt on dying pi: %v", err)
	}
	for range c.Events() {
	}
	var ee *exec.ExitError
	if err := c.Close(); !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("close: %v", err)
	}
}

func TestStartMissingBinary(t *testing.T) {
	_, err := pirpc.Start(context.Background(), "/nonexistent/pi", pirpc.Options{Stderr: io.Discard})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRealPi drives the real binary; needs a provider key, so it is
// opt-in: PIRPC_REAL=1 go test ./pirpc/ -run TestRealPi -v
func TestRealPi(t *testing.T) {
	if os.Getenv("PIRPC_REAL") != "1" {
		t.Skip("set PIRPC_REAL=1 to run against the real pi")
	}
	bin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi not on PATH")
	}
	dir := t.TempDir()
	c, err := pirpc.Start(context.Background(), bin, pirpc.Options{
		Dir:  dir,
		Args: []string{"--session-dir", dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	settled := make(chan struct{})
	go func() {
		for r := range c.Events() {
			t.Logf("%s %s", r.Type, truncate(r.Raw, 120))
			if r.Type == "agent_settled" {
				close(settled)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := c.Prompt(ctx, "Reply with the single word: pong")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("prompt: disposition=%s", resp.Disposition())
	if resp.Disposition() != "handled" {
		select {
		case <-settled:
		case <-ctx.Done():
			t.Fatal("no agent_settled")
		}
	}
	// --session-dir: the session file exists only once something was
	// written to it, so ask after the run.
	s, err := c.State(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("state: %+v", s)
	if s.SessionFile == "" {
		t.Fatal("no sessionFile with --session-dir")
	}
	if _, err := os.Stat(s.SessionFile); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
