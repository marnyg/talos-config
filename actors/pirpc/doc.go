// Package pirpc drives `pi --mode rpc` as a subprocess. It is the child
// beat's view of the agent (ADR-0037, "How events get out of the
// sandbox"): the prompt goes down as a `prompt` command, every record
// pi writes comes back up, raw and in order, `steer`/`abort` go down
// while a run is live, and the deadline — #kill, lapse, or the parent
// losing interest — is "close stdin". It is the consumer of pi's
// documented RPC surface and nothing more: no transport, no cgo, no
// build tags, stdlib only; cmd/child binds it to the protocol.
//
// The authoritative contract is pi's own documentation, shipped with
// the binary (pi 1.0.3 at the time of writing):
//
//	docs/rpc.md            — mode, framing, shutdown, minimal client
//	docs/rpc-commands.md   — every stdin command and its response
//	docs/json.md           — the record stream and its framing rules
//	dist/modes/rpc/rpc-types.d.ts — the wire types
//
// # Framing (docs/rpc.md § Framing, docs/json.md § Framing)
//
// Both directions are strict JSONL: one JSON object per record,
// terminated by LF. Records are split only on LF; one optional CR
// before the LF is stripped. U+2028 and U+2029 are ordinary
// characters inside JSON strings and are never a boundary, which is
// why the reader is a bufio.Reader.ReadBytes('\n') with no line limit
// and not a bufio.Scanner or any Unicode-aware line splitter. Stdout
// carries protocol records only; pi's diagnostics go to stderr, which
// this package passes through to Options.Stderr and never parses.
//
// # Commands and responses
//
// Every command this package sends carries a unique `id`. pi answers
// each with one `response` record of the same id; responses are
// asynchronous and are correlated by id, never by order. A `response`
// is delivered twice: routed to the waiting call (Prompt, Steer,
// Abort, State) and also published on Events, because the consumer
// ships the whole stream. A malformed-input response has no id
// (`"command":"parse"`) and shows up on Events only.
//
// A successful Prompt means pi accepted or queued the message, not
// that the run is over. The run is over when the `agent_settled`
// record arrives on Events — unless the prompt's disposition was
// "handled", in which case no run started and there is nothing to
// wait for (docs/rpc.md § Prompting, docs/rpc-commands.md § prompt).
//
// # Backpressure
//
// pi honours stdout backpressure, so a client that stops reading
// stalls pi (docs/rpc.md § Framing). Events is buffered (Options.
// Buffer, default 1024 records) and the reader goroutine blocks on it
// when full: a consumer that stops draining Events stalls pi, by
// design. Drain Events continuously, on its own goroutine.
//
// # Shutdown
//
// Close closes pi's stdin, which is the documented request for an
// orderly shutdown (docs/rpc.md § Shutdown: pi disposes the active
// runtime and exits). Close then waits for the process; if it has
// not exited after Options.KillAfter (default 10 s) it is killed.
// Close cannot hang. Cancelling the Start context has the same
// effect. Done is closed once the process has exited; Events is
// closed once stdout reaches EOF, which in the normal case is
// moments after Done. Any call waiting on a response when stdout
// ends returns ErrExited rather than hanging.
package pirpc
