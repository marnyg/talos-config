package pirpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures a pi subprocess. The zero value is usable.
type Options struct {
	// Dir is pi's working directory (the sandboxed workspace). Empty
	// means the caller's.
	Dir string
	// Args are appended after `--mode rpc`: `--no-session`,
	// `--session-dir DIR`, `--provider`, `--model`, … as documented in
	// docs/rpc.md § Starting.
	Args []string
	// Env is the subprocess environment; nil inherits the caller's.
	Env []string
	// Stderr receives pi's diagnostics verbatim; nil means os.Stderr.
	Stderr io.Writer
	// Log receives this package's own diagnostics; nil discards.
	Log *slog.Logger
	// Buffer is the Events channel capacity; <= 0 means 1024.
	Buffer int
	// KillAfter bounds Close: how long to wait after closing stdin
	// before killing the process; <= 0 means 10 s.
	KillAfter time.Duration
}

const (
	defaultBuffer    = 1024
	defaultKillAfter = 10 * time.Second
)

var (
	// ErrClosed is returned by a command issued after Close.
	ErrClosed = errors.New("pirpc: stdin closed")
	// ErrExited is returned by a command whose response can no longer
	// arrive because pi's stdout has ended.
	ErrExited = errors.New("pirpc: pi exited")
)

// Record is one line of pi's stdout. Type and ID are lifted from the
// object for routing; Raw is the whole line without its terminator,
// untouched, for shipping as-is. A line that is not a JSON object
// still arrives (Type empty) so that nothing pi says is lost.
type Record struct {
	Type string
	ID   string
	Raw  json.RawMessage
}

// Response is pi's reply to one command (docs/rpc-commands.md).
type Response struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// Disposition returns data.disposition for prompt/steer responses:
// "started", "queued", or "handled" (no run started — do not wait for
// agent_settled). Empty when absent.
func (r Response) Disposition() string {
	var d struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(r.Data, &d)
	return d.Disposition
}

// State is the subset of get_state's data the child beat needs
// (docs/rpc-commands.md § get_state).
type State struct {
	SessionFile         string `json:"sessionFile"`
	SessionID           string `json:"sessionId"`
	SessionName         string `json:"sessionName"`
	IsStreaming         bool   `json:"isStreaming"`
	IsCompacting        bool   `json:"isCompacting"`
	MessageCount        int    `json:"messageCount"`
	PendingMessageCount int    `json:"pendingMessageCount"`
}

// Client is one running pi process. Methods are safe for concurrent
// use.
type Client struct {
	cmd       *exec.Cmd
	log       *slog.Logger
	killAfter time.Duration

	// stdin: writes serialised by wmu; closed exactly once, without
	// taking wmu, so that Close never waits behind a blocked write
	// (closing the *os.File unblocks it).
	stdin     io.WriteCloser
	wmu       sync.Mutex
	stdinOnce sync.Once
	closed    atomic.Bool

	events chan Record

	// pending: in-flight commands by id. stdoutDone flips when the
	// reader reaches EOF; from then on every pending and every new
	// command fails with ErrExited.
	pmu        sync.Mutex
	pending    map[string]chan Response
	stdoutDone bool
	seq        atomic.Uint64

	done    chan struct{} // closed after Wait returns
	exitErr error         // valid after done

	closeOnce sync.Once
	closeErr  error
}

// Start runs `bin --mode rpc <o.Args...>`. ctx bounds the process's
// lifetime: cancelling it closes stdin (orderly shutdown) and, after
// o.KillAfter, kills the process — the same sequence as Close.
func Start(ctx context.Context, bin string, o Options) (*Client, error) {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if o.Buffer <= 0 {
		o.Buffer = defaultBuffer
	}
	if o.KillAfter <= 0 {
		o.KillAfter = defaultKillAfter
	}
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd := exec.CommandContext(ctx, bin, append([]string{"--mode", "rpc"}, o.Args...)...)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	cmd.Stderr = stderr
	cmd.WaitDelay = o.KillAfter

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pirpc: stdin pipe: %w", err)
	}
	// Stdout is a pipe we own rather than cmd.StdoutPipe so that Wait
	// (process exit) and the reader (stream EOF) are independent: the
	// reader drains whatever pi flushed before dying, and Wait never
	// waits on the reader.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pirpc: stdout pipe: %w", err)
	}
	cmd.Stdout = pw

	c := &Client{
		cmd:       cmd,
		log:       log,
		killAfter: o.KillAfter,
		stdin:     stdin,
		events:    make(chan Record, o.Buffer),
		pending:   make(map[string]chan Response),
		done:      make(chan struct{}),
	}
	cmd.Cancel = func() error {
		// Orderly shutdown is the point, so an exit 0 after cancel is
		// success: ErrProcessDone keeps Wait reporting the real exit
		// status instead of ctx.Err().
		c.closeStdin()
		return os.ErrProcessDone
	}

	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, fmt.Errorf("pirpc: start %s: %w", bin, err)
	}
	pw.Close() // the child holds the write end now
	log.Debug("pirpc: started", "bin", bin, "pid", cmd.Process.Pid, "args", cmd.Args[1:])

	go c.read(pr)
	go func() {
		c.exitErr = cmd.Wait()
		log.Debug("pirpc: exited", "pid", cmd.Process.Pid, "err", c.exitErr)
		close(c.done)
	}()
	return c, nil
}

// Events yields every stdout record in order and is closed when
// stdout ends. The reader blocks when the buffer is full: a consumer
// that stops draining stalls pi (see the package doc).
func (c *Client) Events() <-chan Record { return c.events }

// Done is closed once the process has exited; Close returns its
// exit error.
func (c *Client) Done() <-chan struct{} { return c.done }

// Prompt submits a user message (docs/rpc-commands.md § prompt).
// Success means accepted or queued; the run ends with agent_settled
// on Events, unless Response.Disposition is "handled".
func (c *Client) Prompt(ctx context.Context, message string) (Response, error) {
	return c.call(ctx, command{Type: "prompt", Message: message})
}

// Steer queues a message to interrupt the live run
// (docs/rpc-commands.md § steer).
func (c *Client) Steer(ctx context.Context, message string) (Response, error) {
	return c.call(ctx, command{Type: "steer", Message: message})
}

// Abort aborts the current operation; pi replies once the session is
// idle (docs/rpc-commands.md § abort).
func (c *Client) Abort(ctx context.Context) (Response, error) {
	return c.call(ctx, command{Type: "abort"})
}

// State asks pi for its session state (docs/rpc-commands.md
// § get_state); RPC mode emits no session header record, so this is
// the only way to learn the session file.
func (c *Client) State(ctx context.Context) (State, error) {
	r, err := c.call(ctx, command{Type: "get_state"})
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(r.Data, &s); err != nil {
		return State{}, fmt.Errorf("pirpc: get_state data: %w", err)
	}
	return s, nil
}

// Close requests an orderly shutdown by closing stdin, waits up to
// KillAfter for the process to exit, kills it otherwise, and returns
// the exit error (nil for exit 0). Idempotent.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeStdin()
		t := time.NewTimer(c.killAfter)
		defer t.Stop()
		select {
		case <-c.done:
		case <-t.C:
			c.log.Warn("pirpc: pi did not exit after stdin close; killing", "after", c.killAfter)
			_ = c.cmd.Process.Kill()
			<-c.done
		}
		c.closeErr = c.exitErr
	})
	<-c.done
	return c.closeErr
}

func (c *Client) closeStdin() {
	c.stdinOnce.Do(func() {
		c.closed.Store(true)
		_ = c.stdin.Close()
	})
}

type command struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
}

func (c *Client) call(ctx context.Context, cmd command) (Response, error) {
	if c.closed.Load() {
		return Response{}, ErrClosed
	}
	cmd.ID = fmt.Sprintf("%s-%d", cmd.Type, c.seq.Add(1))
	line, err := json.Marshal(cmd)
	if err != nil {
		return Response{}, fmt.Errorf("pirpc: encode %s: %w", cmd.Type, err)
	}
	line = append(line, '\n')

	ch := make(chan Response, 1)
	c.pmu.Lock()
	if c.stdoutDone {
		c.pmu.Unlock()
		return Response{}, ErrExited
	}
	c.pending[cmd.ID] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.pending, cmd.ID)
		c.pmu.Unlock()
	}()

	c.wmu.Lock()
	_, err = c.stdin.Write(line)
	c.wmu.Unlock()
	if err != nil {
		if c.closed.Load() {
			return Response{}, ErrClosed
		}
		return Response{}, fmt.Errorf("pirpc: write %s: %w", cmd.Type, err)
	}

	select {
	case r, ok := <-ch:
		if !ok {
			return Response{}, ErrExited
		}
		return r, nil
	case <-ctx.Done():
		return Response{}, fmt.Errorf("pirpc: %s: %w", cmd.Type, ctx.Err())
	}
}

// read is the stdout goroutine: split on LF only, strip one optional
// CR, route responses, publish everything. On EOF it closes Events
// and fails every pending command.
func (c *Client) read(r io.ReadCloser) {
	defer r.Close()
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimSuffix(line, []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) > 0 {
			c.deliver(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.log.Warn("pirpc: stdout read", "err", err)
			}
			break
		}
	}
	c.pmu.Lock()
	c.stdoutDone = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pmu.Unlock()
	close(c.events)
}

func (c *Client) deliver(line []byte) {
	var head struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		c.log.Warn("pirpc: stdout line is not a JSON object", "err", err, "bytes", len(line))
	}
	if head.Type == "response" {
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			c.log.Warn("pirpc: malformed response", "err", err)
		} else if resp.ID == "" {
			c.log.Warn("pirpc: response without id", "command", resp.Command, "error", resp.Error)
		} else {
			c.pmu.Lock()
			ch, ok := c.pending[resp.ID]
			delete(c.pending, resp.ID)
			c.pmu.Unlock()
			if ok {
				ch <- resp // buffered 1, never blocks
			} else {
				c.log.Warn("pirpc: response for unknown id", "id", resp.ID, "command", resp.Command)
			}
		}
	}
	c.events <- Record{Type: head.Type, ID: head.ID, Raw: json.RawMessage(line)}
}
