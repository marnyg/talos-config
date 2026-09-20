package nodeagent

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A NetworkChanged must make the next hub request dial afresh: the
// pooled h2 connection belongs to the network that just went away.
// hubTransport installs x/net's h2 transport, which net/http's
// CloseIdleConnections only reaches through a reflection hook (issue
// 22891) — this pins that the hook still fires with our setup.
func TestHubTransportCloseIdleDropsH2Conn(t *testing.T) {
	var dials atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("proto %s, want HTTP/2", r.Proto)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			dials.Add(1)
		}
	}
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tr := hubTransport().(*http.Transport)
	// Mutate, don't replace: ConfigureTransports put "h2" in NextProtos.
	tr.TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	c := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	get := func() {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	get()
	get()
	if n := dials.Load(); n != 1 {
		t.Fatalf("after two requests: %d connections, want 1 (pooled)", n)
	}
	c.CloseIdleConnections()
	get()
	if n := dials.Load(); n != 2 {
		t.Fatalf("after CloseIdleConnections: %d connections, want 2 (redialed)", n)
	}
}
