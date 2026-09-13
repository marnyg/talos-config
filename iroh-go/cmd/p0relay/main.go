// Command p0relay is the two-peer probe for Mesh v3 Phase 0 check P0.1
// (bead talos-config-359.1.1): two endpoints on different machines,
// zero n0 infrastructure (PresetMinimal), one self-hosted relay.
//
//	p0relay listen -relay https://relay.example
//	    prints its endpoint id, accepts connections, echoes every
//	    bi-stream and logs the connection's paths as they change
//	p0relay dial -relay https://relay.example -id <hex> [-n 20]
//
// Both take -bind ip:0 to advertise only one interface as a direct
// candidate (e.g. the LAN address on a host that also runs tailscale).
//
//	connects by id + relay URL only, sends n pings one second apart
//	and logs paths after each — watch the selected path move from
//	relay:… to ip:… when the peers can hole-punch
//
// Exit 0 when every ping was echoed.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/marnyg/talos-config/iroh-go/iroh"
)

const alpn = "mesh/p0relay/v1"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "listen":
		listen(os.Args[2:])
	case "dial":
		dial(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: p0relay listen -relay URL | p0relay dial -relay URL -id HEX [-n N]")
	os.Exit(2)
}

func bind(relay, bindAddr string) *iroh.Endpoint {
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
	alpns := [][]byte{[]byte(alpn)}
	opts := iroh.EndpointOptions{Preset: &preset, Alpns: &alpns, RelayMode: &mode}
	if bindAddr != "" { // e.g. 10.0.0.11:0 — advertise only that interface as a candidate
		opts.BindAddr = &bindAddr
	}
	ep, err := iroh.EndpointBind(opts)
	if err != nil {
		fatal("bind: %v", err)
	}
	logf("id=%s sockets=%v", ep.Id().String(), ep.BoundSockets())
	ep.Online()
	logf("online (home relay reachable)")
	return ep
}

func listen(args []string) {
	fs := flag.NewFlagSet("listen", flag.ExitOnError)
	relay := fs.String("relay", "", "relay URL")
	bindAddr := fs.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
	_ = fs.Parse(args)
	ep := bind(*relay, *bindAddr)
	defer ep.Destroy()
	for {
		inc := ep.AcceptNext()
		if inc == nil || *inc == nil {
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
		go serve(conn)
	}
}

func serve(conn *iroh.Connection) {
	defer conn.Destroy()
	logf("accepted from %s paths=%s", short(conn.RemoteId()), paths(conn))
	for {
		bi, err := conn.AcceptBi()
		if err != nil {
			logf("peer %s gone: %v (last paths=%s)", short(conn.RemoteId()), err, paths(conn))
			return
		}
		data, err := bi.Recv().ReadToEnd(1 << 16)
		if err == nil {
			err = bi.Send().WriteAll(data)
		}
		if err == nil {
			err = bi.Send().Finish()
		}
		bi.Destroy()
		if err != nil {
			logf("echo: %v", err)
			return
		}
		logf("echoed %d bytes rtt=%s paths=%s", len(data), rtt(conn), paths(conn))
	}
}

func dial(args []string) {
	fs := flag.NewFlagSet("dial", flag.ExitOnError)
	relay := fs.String("relay", "", "relay URL")
	id := fs.String("id", "", "peer endpoint id (hex)")
	n := fs.Int("n", 20, "pings to send, one per second")
	bindAddr := fs.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)")
	_ = fs.Parse(args)
	if *id == "" {
		fatal("-id is required")
	}
	ep := bind(*relay, *bindAddr)
	defer ep.Destroy()
	peer, err := iroh.EndpointIdFromString(*id)
	if err != nil {
		fatal("peer id: %v", err)
	}
	// Id + relay URL only: no direct addresses, so the first packets must
	// traverse the relay; anything direct afterwards is hole-punching.
	addr := iroh.NewEndpointAddr(peer, relay, nil)
	t0 := time.Now()
	conn, err := ep.Connect(addr, []byte(alpn))
	if err != nil {
		fatal("connect: %v", err)
	}
	defer conn.Destroy()
	logf("connected in %s paths=%s", time.Since(t0).Round(time.Millisecond), paths(conn))
	ok := 0
	for i := 0; i < *n; i++ {
		msg := fmt.Sprintf("ping %d", i)
		bi, err := conn.OpenBi()
		if err != nil {
			fatal("open_bi: %v", err)
		}
		t := time.Now()
		if err := bi.Send().WriteAll([]byte(msg)); err != nil {
			fatal("write: %v", err)
		}
		if err := bi.Send().Finish(); err != nil {
			fatal("finish: %v", err)
		}
		got, err := bi.Recv().ReadToEnd(1 << 16)
		bi.Destroy()
		if err != nil {
			fatal("read: %v", err)
		}
		if string(got) != msg {
			fatal("echo mismatch: %q", got)
		}
		ok++
		logf("%s echoed in %s rtt=%s paths=%s", msg, time.Since(t).Round(time.Millisecond), rtt(conn), paths(conn))
		time.Sleep(time.Second)
	}
	_ = conn.Close(0, []byte("done"))
	if ok != *n {
		os.Exit(1)
	}
	logf("PASS %d/%d", ok, *n)
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

func rtt(conn *iroh.Connection) string {
	if r := conn.Rtt(); r != nil {
		return (time.Duration(*r) * time.Millisecond).String()
	}
	return "n/a"
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
