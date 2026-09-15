// Command p0agent is the Mesh v3 Phase 0 check P0.3 probe (bead
// talos-config-359.1.3): the minimal node agent that runs as a Talos
// system extension. Zero n0 infrastructure (PresetMinimal), one
// self-hosted relay, one persistent NodeId, ALPN-gated stream forwarding
// to loopback targets — the shape of the production agent, without
// certs or authorize() (P0 proves the platform path, not the protocol).
//
//	p0agent serve -relay URL -key PATH -forward ALPN=host:port [-forward ...]
//	    loads (or mints and persists, 0600) the 32-byte secret key at PATH,
//	    prints its endpoint id, dials the relay outbound, and pipes every
//	    accepted bi-stream to the target registered for the connection's
//	    ALPN. Connections on any other ALPN are refused at the TLS
//	    handshake (only registered ALPNs are advertised) and, belt and
//	    braces, closed if they get through. ALPN routes; it never
//	    authorizes — the receiver-side authorize() is Phase 1.
//	p0agent bridge -relay URL -id HEX -alpn ALPN -listen host:port
//	    the desktop side (irohup's ancestor): a local TCP listener; each
//	    accepted TCP connection becomes one bi-stream on a connection to
//	    the peer under ALPN. Redials the peer when the connection is gone
//	    (the peer rebooted), so `talosctl reboot` can be observed end to
//	    end from one bridge.
//
// Both take -bind ip:0 to advertise only one interface as a direct
// candidate.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/iroh-go/iroh"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "bridge":
		bridge(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: p0agent serve -relay URL -key PATH -forward ALPN=host:port ... | p0agent bridge -relay URL -id HEX -alpn ALPN -listen host:port")
	os.Exit(2)
}

type forwards map[string]string // alpn -> host:port

func (f forwards) String() string {
	var parts []string
	for k, v := range f {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (f forwards) Set(s string) error {
	alpn, target, ok := strings.Cut(s, "=")
	if !ok || alpn == "" || target == "" {
		return fmt.Errorf("want ALPN=host:port, got %q", s)
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return err
	}
	f[alpn] = target
	return nil
}

// bind builds the endpoint: minimal preset (no discovery, no default
// relays), the given relay pinned as the only relay, the given ALPNs
// advertised, optional persistent secret key.
func bind(relay, bindAddr string, alpns []string, key *[]byte) *iroh.Endpoint {
	if lvl := os.Getenv("P0_LOG"); lvl != "" { // trace|debug|info|warn
		iroh.SetLogLevel(map[string]iroh.LogLevel{"trace": iroh.LogLevelTrace, "debug": iroh.LogLevelDebug, "info": iroh.LogLevelInfo, "warn": iroh.LogLevelWarn}[lvl])
	}
	if relay == "" {
		fatal("-relay is required")
	}
	mode, err := iroh.RelayModeCustomFromUrls([]string{relay})
	if err != nil {
		fatal("relay mode: %v", err)
	}
	preset := iroh.PresetMinimal()
	opts := iroh.EndpointOptions{Preset: &preset, RelayMode: &mode, SecretKey: key}
	if len(alpns) > 0 {
		var bs [][]byte
		for _, a := range alpns {
			bs = append(bs, []byte(a))
		}
		opts.Alpns = &bs
	}
	if bindAddr != "" {
		opts.BindAddr = &bindAddr
	}
	ep, err := iroh.EndpointBind(opts)
	if err != nil {
		fatal("bind: %v", err)
	}
	logf("id=%s sockets=%v", ep.Id().String(), ep.BoundSockets())
	t0 := time.Now()
	ep.Online()
	logf("online (home relay %s reachable) after %s", relay, time.Since(t0).Round(time.Millisecond))
	return ep
}

// loadKey returns the 32-byte secret at path, minting and persisting one
// (0600, parent dir created) if absent. The key IS the node's identity
// across reboots: this is the one durable thing the agent owns
// (invariants.md 2, actor-owned state: possession is the credential).
func loadKey(path string) []byte {
	if b, err := os.ReadFile(path); err == nil {
		if len(b) != 32 {
			fatal("key %s: want 32 bytes, got %d", path, len(b))
		}
		logf("key loaded from %s", path)
		return b
	} else if !errors.Is(err, os.ErrNotExist) {
		fatal("key %s: %v", path, err)
	}
	sk := iroh.SecretKeyGenerate()
	defer sk.Destroy()
	b := sk.ToBytes()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fatal("mkdir for key: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		fatal("write key: %v", err)
	}
	logf("key minted and persisted at %s", path)
	return b
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	relay := fs.String("relay", "", "relay URL")
	keyPath := fs.String("key", "", "secret key file (32 bytes; minted if absent)")
	bindAddr := fs.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
	fwd := forwards{}
	fs.Var(fwd, "forward", "ALPN=host:port (repeatable)")
	_ = fs.Parse(args)
	if len(fwd) == 0 {
		fatal("at least one -forward is required")
	}
	if *keyPath == "" {
		fatal("-key is required")
	}
	// Boot-order evidence for the ADR-0019 NTP-gate question: wall clock
	// vs. uptime at the moment the extension service actually started.
	logf("start wall=%s uptime=%s", time.Now().UTC().Format(time.RFC3339), uptime())
	key := loadKey(*keyPath)
	var alpns []string
	for a := range fwd {
		alpns = append(alpns, a)
	}
	ep := bind(*relay, *bindAddr, alpns, &key)
	logf("serving %s", fwd)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	var stopping atomic.Bool
	go func() {
		s := <-sig
		stopping.Store(true)
		// Close() sends CONNECTION_CLOSE to peers (the bridge redials at
		// once instead of waiting out the QUIC idle timeout); exit after.
		logf("%v: closing endpoint", s)
		_ = ep.Close()
		logf("closed")
		os.Exit(0)
	}()

	for {
		inc := ep.AcceptNext()
		if inc == nil || *inc == nil {
			if stopping.Load() {
				select {} // shutdown in flight; the handler above exits
			}
			fatal("accept_next returned none (endpoint closed?)")
		}
		accepting, err := (*inc).Accept()
		(*inc).Destroy()
		if err != nil {
			logf("incoming accept: %v", err)
			continue
		}
		conn, err := accepting.Connect()
		accepting.Destroy()
		if err != nil {
			logf("handshake: %v", err)
			continue
		}
		go serveConn(conn, fwd)
	}
}

func serveConn(conn *iroh.Connection, fwd forwards) {
	defer conn.Destroy()
	alpn := string(conn.Alpn())
	target, ok := fwd[alpn]
	if !ok {
		logf("refused %s: alpn %q not served", short(conn.RemoteId()), alpn)
		_ = conn.Close(1, []byte("alpn not served"))
		return
	}
	logf("accepted %s alpn=%s -> %s paths=%s", short(conn.RemoteId()), alpn, target, paths(conn))
	for {
		bi, err := conn.AcceptBi()
		if err != nil {
			logf("peer %s gone: %v (last paths=%s)", short(conn.RemoteId()), err, paths(conn))
			return
		}
		go func() {
			defer bi.Destroy()
			tcp, err := net.DialTimeout("tcp", target, 5*time.Second)
			if err != nil {
				logf("dial %s: %v", target, err)
				_ = bi.Send().Reset(1)
				return
			}
			t0 := time.Now()
			in, out := pipe(bi, tcp.(*net.TCPConn))
			logf("stream done %s: %dB in, %dB out, %s, paths=%s", target, in, out, time.Since(t0).Round(time.Millisecond), paths(conn))
		}()
	}
}

func bridge(args []string) {
	fs := flag.NewFlagSet("bridge", flag.ExitOnError)
	relay := fs.String("relay", "", "relay URL")
	id := fs.String("id", "", "peer endpoint id (hex)")
	alpn := fs.String("alpn", "mesh/apid/v1", "ALPN to dial under")
	listen := fs.String("listen", "127.0.0.1:50000", "local TCP listen address")
	bindAddr := fs.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
	_ = fs.Parse(args)
	if *id == "" {
		fatal("-id is required")
	}
	peer, err := iroh.EndpointIdFromString(*id)
	if err != nil {
		fatal("peer id: %v", err)
	}
	ep := bind(*relay, *bindAddr, nil, nil)
	defer ep.Destroy()

	var mu sync.Mutex
	var conn *iroh.Connection
	getConn := func() (*iroh.Connection, error) {
		mu.Lock()
		defer mu.Unlock()
		if conn != nil && conn.CloseReason() == nil {
			return conn, nil
		}
		if conn != nil {
			logf("connection gone: %s — redialing", *conn.CloseReason())
			conn.Destroy()
			conn = nil
		}
		// Id + relay URL only: no direct addresses, so the first packets
		// traverse the relay; anything direct afterwards is hole-punching.
		t0 := time.Now()
		c, err := ep.Connect(iroh.NewEndpointAddr(peer, relay, nil), []byte(*alpn))
		if err != nil {
			return nil, err
		}
		logf("connected to %s in %s paths=%s", short(peer), time.Since(t0).Round(time.Millisecond), paths(c))
		conn = c
		return conn, nil
	}
	if _, err := getConn(); err != nil {
		fatal("connect: %v", err)
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fatal("listen: %v", err)
	}
	logf("bridging %s -> %s alpn=%s", *listen, short(peer), *alpn)
	for {
		tcp, err := ln.Accept()
		if err != nil {
			fatal("accept: %v", err)
		}
		go func() {
			c, err := getConn()
			if err != nil {
				logf("connect: %v", err)
				tcp.Close()
				return
			}
			bi, err := c.OpenBi()
			if err != nil {
				// Stale connection (peer rebooted, idle timeout): drop it
				// so the next accept redials, and give this one a second try.
				logf("open_bi: %v — retrying once", err)
				mu.Lock()
				if conn == c {
					conn.Destroy()
					conn = nil
				}
				mu.Unlock()
				if c, err = getConn(); err == nil {
					bi, err = c.OpenBi()
				}
				if err != nil {
					logf("open_bi: %v", err)
					tcp.Close()
					return
				}
			}
			defer bi.Destroy()
			t0 := time.Now()
			in, out := pipe(bi, tcp.(*net.TCPConn))
			logf("stream done: %dB from peer, %dB to peer, %s, paths=%s", in, out, time.Since(t0).Round(time.Millisecond), paths(c))
		}()
	}
}

// pipe copies tcp→send and recv→tcp until both directions are done.
// Half-closes propagate: TCP FIN → QUIC FIN, QUIC FIN → TCP CloseWrite.
// Returns bytes received from the stream and bytes sent into it.
func pipe(bi *iroh.BiStream, tcp *net.TCPConn) (fromStream, toStream int64) {
	send, recv := bi.Send(), bi.Recv()
	defer send.Destroy()
	defer recv.Destroy()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // stream -> tcp
		defer wg.Done()
		for {
			chunk, err := recv.Read(64 << 10)
			if err != nil {
				_ = tcp.Close() // reset from peer: tear down both ways
				return
			}
			if len(chunk) == 0 { // FIN
				_ = tcp.CloseWrite()
				return
			}
			n, err := tcp.Write(chunk)
			fromStream += int64(n)
			if err != nil {
				_ = recv.Stop(0)
				return
			}
		}
	}()
	go func() { // tcp -> stream
		defer wg.Done()
		buf := make([]byte, 64<<10)
		for {
			n, err := tcp.Read(buf)
			if n > 0 {
				if werr := send.WriteAll(buf[:n]); werr != nil {
					_ = tcp.Close()
					return
				}
				toStream += int64(n)
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					_ = send.Finish()
				} else {
					_ = send.Reset(0)
				}
				return
			}
		}
	}()
	wg.Wait()
	_ = tcp.Close()
	return
}

func uptime() string {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "n/a"
	}
	f, _, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	return f + "s"
}

func paths(conn *iroh.Connection) string {
	var parts []string
	for _, p := range conn.Paths() {
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

func short(id *iroh.EndpointId) string {
	if id == nil {
		return "?"
	}
	s := id.String()
	if len(s) > 10 {
		s = s[:10]
	}
	return s
}

func logf(f string, a ...any) {
	fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05.000 ")+f+"\n", a...)
}

func fatal(f string, a ...any) {
	logf("fatal: "+f, a...)
	os.Exit(1)
}
