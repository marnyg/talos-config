// Command smoke is the acceptance test for the in-house iroh Go binding
// (task talos-config-ow7). It uses nothing but the generated package.
//
// Two Endpoints in one process, zero n0 infrastructure (PresetMinimal: no
// DNS discovery, no pkarr, no default relays), connect A→B under ALPN
// mesh/smoke/v1, one bidirectional stream, echo, clean close.
//
//	smoke                       direct path: RelayMode::Disabled, dial B's
//	                            bound 127.0.0.1 socket
//	smoke -relay http://h:p     relay path: RelayMode::Custom(url), B is
//	                            dialed by id + relay URL only (no direct
//	                            addresses), so the first packets traverse
//	                            the relay
//
// Exit 0 on success.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/marnyg/talos-config/iroh-go/iroh"
)

const (
	alpn    = "mesh/smoke/v1"
	message = "hello from the sovereign actor smoke test"
)

// Options selects the path under test.
type Options struct {
	// RelayURL empty ⇒ RelayMode::Disabled and a direct dial.
	RelayURL string
	Timeout  time.Duration
	Log      io.Writer
}

// Run executes one smoke round and returns nil on success. main and the
// go test both call this.
func Run(o Options) error {
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	logf := func(f string, a ...any) { fmt.Fprintf(o.Log, f+"\n", a...) }

	var mode *iroh.RelayMode
	if o.RelayURL == "" {
		mode = iroh.RelayModeDisabled()
	} else {
		m, err := iroh.RelayModeCustomFromUrls([]string{o.RelayURL})
		if err != nil {
			return fmt.Errorf("relay mode: %w", err)
		}
		mode = m
	}
	logf("relay mode: %s", mode.String())

	bind := func(name string) (*iroh.Endpoint, error) {
		preset := iroh.PresetMinimal() // crypto provider only; no n0 endpoints
		bindAddr := "127.0.0.1:0"
		alpns := [][]byte{[]byte(alpn)}
		ep, err := iroh.EndpointBind(iroh.EndpointOptions{
			Preset:    &preset,
			BindAddr:  &bindAddr,
			Alpns:     &alpns,
			RelayMode: &mode,
		})
		if err != nil {
			return nil, fmt.Errorf("bind %s: %w", name, err)
		}
		logf("%s: id=%s sockets=%v", name, ep.Id().FmtShort(), ep.BoundSockets())
		return ep, nil
	}

	a, err := bind("A")
	if err != nil {
		return err
	}
	defer a.Destroy()
	b, err := bind("B")
	if err != nil {
		return err
	}
	defer b.Destroy()

	// Address of B as A will dial it.
	var addrB *iroh.EndpointAddr
	if o.RelayURL == "" {
		addrB = iroh.NewEndpointAddr(b.Id(), nil, b.BoundSockets())
	} else {
		// Both endpoints must have a home relay before a relay-only dial
		// can succeed. online() waits for exactly that.
		if err := await(o.Timeout, "online", func() error { a.Online(); b.Online(); return nil }); err != nil {
			return err
		}
		relay := o.RelayURL
		addrB = iroh.NewEndpointAddr(b.Id(), &relay, nil)
	}
	logf("dialing %s", addrB.String())

	// Server side: accept one connection, echo one bi-stream, report.
	type result struct {
		got []byte
		err error
	}
	srv := make(chan result, 1)
	go func() {
		got, err := serveOne(b)
		srv <- result{got, err}
	}()

	// Client side.
	var echoed []byte
	err = await(o.Timeout, "client", func() error {
		conn, err := a.Connect(addrB, []byte(alpn))
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer conn.Destroy()
		if got := string(conn.Alpn()); got != alpn {
			return fmt.Errorf("client alpn %q, want %q", got, alpn)
		}
		bi, err := conn.OpenBi()
		if err != nil {
			return fmt.Errorf("open_bi: %w", err)
		}
		defer bi.Destroy()
		if err := bi.Send().WriteAll([]byte(message)); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		if err := bi.Send().Finish(); err != nil {
			return fmt.Errorf("finish: %w", err)
		}
		echoed, err = bi.Recv().ReadToEnd(1 << 16)
		if err != nil {
			return fmt.Errorf("read_to_end: %w", err)
		}
		logf("client: rtt=%v paths=%s", derefU64(conn.Rtt()), describePaths(conn.Paths()))
		if err := conn.Close(0, []byte("done")); err != nil {
			return fmt.Errorf("close: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	var sr result
	select {
	case sr = <-srv:
	case <-time.After(o.Timeout):
		return errors.New("server side timed out")
	}
	if sr.err != nil {
		return fmt.Errorf("server: %w", sr.err)
	}
	if !bytes.Equal(sr.got, []byte(message)) {
		return fmt.Errorf("server received %q, want %q", sr.got, message)
	}
	if !bytes.Equal(echoed, []byte(message)) {
		return fmt.Errorf("client echoed %q, want %q", echoed, message)
	}

	if err := await(o.Timeout, "close", func() error {
		if err := a.Close(); err != nil {
			return fmt.Errorf("close A: %w", err)
		}
		if err := b.Close(); err != nil {
			return fmt.Errorf("close B: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	if !a.IsClosed() || !b.IsClosed() {
		return errors.New("endpoints report not closed")
	}
	logf("ok: echoed %d bytes over %s (%s)", len(echoed), alpn, pathName(o.RelayURL))
	return nil
}

// serveOne accepts the first incoming connection on ep, reads one bi-stream
// to end, echoes it back, and returns what it read.
func serveOne(ep *iroh.Endpoint) ([]byte, error) {
	inc := ep.AcceptNext()
	if inc == nil || *inc == nil {
		return nil, errors.New("accept_next returned none (endpoint closed?)")
	}
	defer (*inc).Destroy()
	accepting, err := (*inc).Accept()
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}
	defer accepting.Destroy()
	got, err := accepting.Alpn()
	if err != nil {
		return nil, fmt.Errorf("alpn: %w", err)
	}
	if string(got) != alpn {
		return nil, fmt.Errorf("server alpn %q, want %q", got, alpn)
	}
	conn, err := accepting.Connect()
	if err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	defer conn.Destroy()
	bi, err := conn.AcceptBi()
	if err != nil {
		return nil, fmt.Errorf("accept_bi: %w", err)
	}
	defer bi.Destroy()
	data, err := bi.Recv().ReadToEnd(1 << 16)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if err := bi.Send().WriteAll(data); err != nil {
		return nil, fmt.Errorf("echo write: %w", err)
	}
	if err := bi.Send().Finish(); err != nil {
		return nil, fmt.Errorf("echo finish: %w", err)
	}
	// Wait for the peer to close so the echo is flushed before we drop.
	reason := conn.Closed()
	if !strings.Contains(reason, "done") && !strings.Contains(reason, "ApplicationClosed") && !strings.Contains(reason, "closed") {
		return data, fmt.Errorf("unexpected close reason: %q", reason)
	}
	return data, nil
}

// await runs f on a goroutine (the binding's async calls block the calling
// goroutine) and fails if it does not return within d.
func await(d time.Duration, what string, f func() error) error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		return fmt.Errorf("%s: timed out after %s", what, d)
	}
}

func derefU64(p *uint64) any {
	if p == nil {
		return "n/a"
	}
	return time.Duration(*p) * time.Millisecond
}

// describePaths renders the connection's transport paths ("relay:<url>",
// "ip:<addr>", "*" marks the selected one) so the log shows which way the
// bytes went.
func describePaths(ps []iroh.PathSnapshot) string {
	var parts []string
	for _, p := range ps {
		kind := "other"
		switch {
		case p.IsRelay:
			kind = "relay"
		case p.IsIp:
			kind = "ip"
		}
		sel := ""
		if p.IsSelected {
			sel = "*"
		}
		parts = append(parts, fmt.Sprintf("%s%s:%s", sel, kind, p.RemoteAddr))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func pathName(relay string) string {
	if relay == "" {
		return "direct, RelayMode::Disabled"
	}
	return "via relay " + relay
}
