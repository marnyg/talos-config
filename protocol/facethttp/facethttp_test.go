package facethttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// TestServerRoundTrip drives one request from an http.Transport over
// Dialed through a Listener fed by Push, and checks the handler sees
// the identity and peer Push attributed to the stream.
func TestServerRoundTrip(t *testing.T) {
	want := cert.Identity{Key: cert.ActorID("caller-key"), Name: "laptop", Groups: []string{"admins"}}
	wantPeer := cert.ActorID("caller-key")

	gotID := make(chan cert.Identity, 1)
	gotPeer := make(chan cert.ActorID, 1)
	srv, ln := Server("hub-http", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			t.Error("IdentityFrom: missing")
		}
		peer, ok := PeerFrom(r.Context())
		if !ok {
			t.Error("PeerFrom: missing")
		}
		gotID <- id
		gotPeer <- peer
		if ra := r.RemoteAddr; ra != string(wantPeer) {
			t.Errorf("RemoteAddr = %q, want %q", ra, wantPeer)
		}
		io.WriteString(w, "ok")
	}))
	go srv.Serve(ln)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			client, server := net.Pipe()
			if !ln.Push(ctx, server, want, wantPeer) {
				server.Close()
				return nil, errors.New("push refused")
			}
			return Dialed(client, "laptop", cert.ActorID("hub-key")), nil
		},
	}
	defer tr.CloseIdleConnections()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://hub-http/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}

	id := <-gotID
	if id.Key != want.Key || id.Name != want.Name || len(id.Groups) != 1 || id.Groups[0] != "admins" {
		t.Errorf("identity = %+v, want %+v", id, want)
	}
	if peer := <-gotPeer; peer != wantPeer {
		t.Errorf("peer = %q, want %q", peer, wantPeer)
	}
}

// TestDialedAddrs: the dial side names itself and the receiver, no port.
func TestDialedAddrs(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	c := Dialed(client, "laptop", cert.ActorID("hub-key"))
	defer c.Close()
	if got := c.LocalAddr().String(); got != "laptop" {
		t.Errorf("LocalAddr = %q", got)
	}
	if got := c.RemoteAddr().String(); got != "hub-key" {
		t.Errorf("RemoteAddr = %q", got)
	}
	if got := c.LocalAddr().Network(); got != "talos-mesh" {
		t.Errorf("Network = %q", got)
	}
	if err := c.SetDeadline(time.Now()); err != nil {
		t.Errorf("SetDeadline: %v", err)
	}
}

// TestListenerCloseUnblocksAccept: Close makes a pending Accept return
// net.ErrClosed, and a later Push is refused.
func TestListenerCloseUnblocksAccept(t *testing.T) {
	ln := NewListener("hub-http")
	errc := make(chan error, 1)
	go func() {
		_, err := ln.Accept()
		errc <- err
	}()
	ln.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after Close: %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return after Close")
	}
	_, server := net.Pipe()
	defer server.Close()
	if ln.Push(context.Background(), server, cert.Identity{}, "") {
		t.Fatal("Push after Close: accepted, want refused")
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// blockingRWC hides a net.Pipe end behind io.ReadWriteCloser: no
// deadline methods, and a Read that returns only when the peer writes
// or closes — the shape of an iroh stream.
type blockingRWC struct{ net.Conn }

func (b blockingRWC) Read(p []byte) (int, error)  { return b.Conn.Read(p) }
func (b blockingRWC) Write(p []byte) (int, error) { return b.Conn.Write(p) }
func (b blockingRWC) Close() error                { return b.Conn.Close() }

// TestHijackUpgrade: a handler that hijacks for a 101 must get the
// connection back while the client is still waiting for the 101 —
// http.Server aborts its background read with a read deadline, and
// Conn has to honour it without the peer's help (talos-config-o8wd).
// Before the pump this hung until the client gave up.
func TestHijackUpgrade(t *testing.T) {
	srv, ln := Server("ingress-http", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "echo" {
			http.Error(w, "no upgrade", http.StatusBadRequest)
			return
		}
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		defer conn.Close()
		if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n"); err != nil {
			t.Errorf("write 101: %v", err)
			return
		}
		line, err := brw.ReadString('\n')
		if err != nil {
			t.Errorf("read after upgrade: %v", err)
			return
		}
		io.WriteString(conn, "echo "+line)
	}))
	go srv.Serve(ln)
	defer srv.Close()

	client, server := net.Pipe()
	defer client.Close()
	if !ln.Push(context.Background(), blockingRWC{server}, cert.Identity{Name: "laptop"}, "caller-key") {
		t.Fatal("push refused")
	}

	done := make(chan string, 1)
	go func() {
		client.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(client, "GET /ws HTTP/1.1\r\nHost: ingress-http\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n"); err != nil {
			done <- "write request: " + err.Error()
			return
		}
		buf := make([]byte, 4096)
		var got []byte
		for !bytesContains(got, "\r\n\r\n") {
			n, err := client.Read(buf)
			if err != nil {
				done <- "read 101: " + err.Error()
				return
			}
			got = append(got, buf[:n]...)
		}
		if !bytesContains(got, "101 Switching Protocols") {
			done <- "not a 101: " + string(got)
			return
		}
		if _, err := io.WriteString(client, "ping\n"); err != nil {
			done <- "write ping: " + err.Error()
			return
		}
		n, err := client.Read(buf)
		if err != nil {
			done <- "read echo: " + err.Error()
			return
		}
		done <- string(buf[:n])
	}()
	select {
	case got := <-done:
		if got != "echo ping\n" {
			t.Fatalf("got %q, want \"echo ping\\n\"", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("upgrade did not complete: Hijack is blocked on the background read")
	}
}

func bytesContains(b []byte, s string) bool { return strings.Contains(string(b), s) }

// TestConnReadDeadline: a Read in flight returns os.ErrDeadlineExceeded
// (a net.Error that reports Timeout) when the deadline passes, the
// stream is untouched, and a later Read still delivers the bytes.
func TestConnReadDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	c := newConn(blockingRWC{server}, cert.Identity{}, "peer", "f")
	defer c.Close()

	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("Read past deadline: %v, want a timeout net.Error", err)
	}
	c.SetReadDeadline(time.Time{})

	go io.WriteString(client, "late")
	buf := make([]byte, 8)
	n, err := c.Read(buf)
	if err != nil || string(buf[:n]) != "late" {
		t.Fatalf("Read after deadline cleared: %q, %v", buf[:n], err)
	}
}
