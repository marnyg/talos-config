package irohtransport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
)

const testALPN = "talos-mesh/apid/v1"

// TestStreamFacet: the connection is the invocation. A node advertises
// one stream ALPN beside the actor ALPN; a caller connects under it,
// presents a preamble, is admitted, and every later bi-stream is a raw
// byte pipe spliced to a loopback TCP echo. A second caller is refused
// and learns why; the actor ALPN keeps working beside it.
func TestStreamFacet(t *testing.T) {
	ctx := testCtx(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	node, err := Bind(priv, Options{BindAddr: "127.0.0.1:0", StreamALPNs: []string{testALPN}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	caller, _ := bindLoopback(t, "")

	// Loopback echo standing in for apid.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()

	// The node: accept, read the preamble, admit "let-me-in", refuse the
	// rest, splice admitted forwards to the echo.
	go func() {
		for {
			c, err := node.AcceptConn(ctx)
			if err != nil {
				return
			}
			go func() {
				pre, err := c.Preamble(ctx)
				if err != nil {
					return
				}
				if string(pre) != "let-me-in" || c.ALPN() != testALPN || c.Peer() != caller.ID() {
					_ = c.Refuse(ctx, "not authorized")
					return
				}
				if err := c.Admit(ctx); err != nil {
					return
				}
				for {
					raw, err := c.Accept(ctx)
					if err != nil {
						return
					}
					go func() {
						defer raw.Close()
						tcp, err := net.Dial("tcp", ln.Addr().String())
						if err != nil {
							return
						}
						defer tcp.Close()
						done := make(chan struct{})
						go func() { _, _ = io.Copy(tcp, raw); _ = tcp.(*net.TCPConn).CloseWrite(); close(done) }()
						_, _ = io.Copy(raw, tcp)
						_ = raw.CloseWrite()
						<-done
					}()
				}
			}()
		}
	}()

	hints := node.Endpoints()

	// Refused: the reason arrives before any forward is opened.
	_, err = caller.DialConn(ctx, node.ID(), hints, testALPN, []byte("stranger"))
	var refused *ErrRefused
	if !errors.As(err, &refused) || refused.Reason != "not authorized" {
		t.Fatalf("stranger: %v", err)
	}

	// Admitted: two forwards on one connection, each echoed.
	conn, err := caller.DialConn(ctx, node.ID(), hints, testALPN, []byte("let-me-in"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i, msg := range [][]byte{[]byte("hello apid"), bytes.Repeat([]byte("x"), 300<<10)} {
		raw, err := conn.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Write(msg); err != nil {
			t.Fatal(err)
		}
		if err := raw.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(raw)
		if err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("forward %d: %d bytes, %v", i, len(got), err)
		}
		_ = raw.Close()
	}
	if !conn.Alive() {
		t.Fatal("connection died after forwards")
	}

	// The actor ALPN is unaffected: a Dial still yields an actor stream
	// on Accept.
	go func() {
		s, _, err := node.Accept(ctx)
		if err != nil {
			return
		}
		msg, _ := s.RecvMsg(ctx)
		_ = s.SendMsg(ctx, append([]byte("re:"), msg...))
		_ = s.Close()
	}()
	s, err := caller.Dial(ctx, node.ID(), hints)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendMsg(ctx, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if reply, err := s.RecvMsg(ctx); err != nil || string(reply) != "re:ping" {
		t.Fatalf("actor stream beside the facet: %q %v", reply, err)
	}
	_ = s.Close()

	// An ALPN the node does not advertise fails at the handshake.
	short, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := caller.DialConn(short, node.ID(), hints, "talos-mesh/nope/v1", nil); err == nil {
		t.Fatal("unadvertised ALPN connected")
	}

	// Closing the node's endpoint ends the caller's connection.
	_ = node.Close()
	if _, err := caller.DialConn(short, node.ID(), hints, testALPN, []byte("let-me-in")); err == nil {
		t.Fatal("dialled a closed endpoint")
	}
}

func TestStreamALPNOptions(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	for _, bad := range [][]string{{ALPN}, {""}, {"a", "a"}} {
		if _, err := Bind(priv, Options{BindAddr: "127.0.0.1:0", StreamALPNs: bad}); err == nil {
			t.Errorf("StreamALPNs %q accepted", bad)
		}
	}
	e, _ := bindLoopback(t, "")
	if _, err := e.DialConn(context.Background(), e.ID(), nil, ALPN, nil); err == nil {
		t.Error("DialConn under the actor ALPN accepted")
	}
	_ = actor.ErrClosed
}
