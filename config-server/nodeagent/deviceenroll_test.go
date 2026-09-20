package nodeagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/cert"
)

// fakeHub is the device-flow surface of the hub as a headless member
// sees it: start, poll (pending twice, then a token), redeem.
type fakeHub struct {
	t      *testing.T
	iss    *issuer.Issuer
	node   cert.ActorID
	polls  atomic.Int32
	deny   bool
	sealed bool
	kit    issuer.Kit
}

func (h *fakeHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+DeviceEnrollPath, func(w http.ResponseWriter, r *http.Request) {
		if h.sealed {
			http.Error(w, "sealed", http.StatusServiceUnavailable)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("node") != string(h.node) || len(r.FormValue("pubkey")) != 64 || r.FormValue("proposed_name") != "gw" {
			http.Error(w, "bad start: "+r.Form.Encode(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc", "user_code": "ABCD-1234", "verification_uri_complete": "http://hub/status?user_code=ABCD-1234",
			"expires_in": 600, "interval": 1,
		})
	})
	mux.HandleFunc("POST "+DeviceTokenPath, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") != deviceGrantType || r.FormValue("device_code") != "dc" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		n := h.polls.Add(1)
		switch {
		case h.deny:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
		case n == 1:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer"})
		}
	})
	mux.HandleFunc("GET "+DeviceConfigPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		kit, err := issuer.EncodeKit(h.kit)
		if err != nil {
			h.t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"config": "pki: {}\n", "kit": json.RawMessage(kit)})
	})
	return mux
}

// newFakeHub is an unsealed Issuer that has minted node's Kit as `gw`.
func newFakeHub(t *testing.T, node cert.ActorID) *fakeHub {
	t.Helper()
	iss, err := issuer.New([]string{"admins", "media", "machines"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wpriv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	ws := cert.NewEthSigner(wpriv)
	_, msg, err := iss.Proposal(ws.ActorID())
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ws.Sign([]byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Unseal("0x"+hex.EncodeToString(sig), []cert.ActorID{ws.ActorID()}); err != nil {
		t.Fatal(err)
	}
	kit, err := iss.Mint(node, "gw", []string{"media"})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeHub{t: t, iss: iss, node: node, kit: kit}
}

func TestEnrollDevice(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	node := cert.NewEdSigner(priv).ActorID()
	h := newFakeHub(t, node)
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var shown DeviceFlow
	kit, err := EnrollDevice(ctx, srv.Client(), srv.URL, node, "gw", "media", func(f DeviceFlow) { shown = f })
	if err != nil {
		t.Fatal(err)
	}
	if shown.UserCode != "ABCD-1234" || shown.ApproveURL == "" {
		t.Errorf("show: %+v", shown)
	}
	if kit.Member.Cav.Name != "gw" || kit.Member.Aud != string(node) {
		t.Errorf("kit: %+v", kit.Member)
	}
	if h.polls.Load() < 2 {
		t.Errorf("polled %d times; pending should have cost a round", h.polls.Load())
	}

	// A Kit for another key is refused at CheckKit, whatever the hub says.
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	other := cert.NewEdSigner(otherPriv).ActorID()
	h2 := newFakeHub(t, other)
	h2.node = node // the hub accepts the start for node but hands back other's Kit
	srv2 := httptest.NewServer(h2.handler())
	defer srv2.Close()
	if _, err := EnrollDevice(ctx, srv2.Client(), srv2.URL, node, "gw", "media", nil); err == nil {
		t.Error("a Kit minted to another key must be refused")
	}

	// Denied is terminal; sealed is a retry.
	h.deny = true
	h.polls.Store(0)
	if _, err := EnrollDevice(ctx, srv.Client(), srv.URL, node, "gw", "media", nil); !errors.Is(err, ErrEnrollDenied) {
		t.Errorf("denied: %v", err)
	}
	h.sealed = true
	if _, err := EnrollDevice(ctx, srv.Client(), srv.URL, node, "gw", "media", nil); !errors.Is(err, ErrHubSealed) {
		t.Errorf("sealed: %v", err)
	}
}
