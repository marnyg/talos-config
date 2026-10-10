package facethttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
