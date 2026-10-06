package jellyfinqc

import (
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/meshtoken"
	"github.com/marnyg/talos-config/protocol/cert"
)

const (
	host     = "jellyfin.gw.mesh.internal"
	adminPw  = "hunter2"
	adminTok = "admin-token"
)

var now = time.Unix(1_800_000_000, 0)

func device(name string, groups ...string) cert.Identity {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return cert.Identity{Key: cert.NewEdSigner(priv).ActorID(), Name: name, Groups: groups}
}

// fakeJellyfin is the slice of the Jellyfin API the proxy touches,
// with enough state to see what it did.
type fakeJellyfin struct {
	mu          sync.Mutex
	users       []map[string]any // UserDto
	policies    map[string]map[string]any
	authorized  []string // "code:userId"
	initiates   int
	config      map[string]any
	seenHeaders http.Header
	lastHost    string
}

func newFakeJellyfin() *fakeJellyfin {
	return &fakeJellyfin{
		users: []map[string]any{
			{"Name": "admin", "Id": "u-admin", "Policy": map[string]any{"IsAdministrator": true}},
			{"Name": "mar", "Id": "u-mar", "Policy": map[string]any{"IsAdministrator": true}},
			{"Name": "guest", "Id": "u-guest", "Policy": map[string]any{"IsAdministrator": false}},
		},
		policies: map[string]map[string]any{},
		config:   map[string]any{"QuickConnectAvailable": false, "ServerName": "x"},
	}
}

func (f *fakeJellyfin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == InitiatePath || r.URL.Path == "/other" {
		// a proxied client request, not one of the proxy's own admin calls
		f.seenHeaders = r.Header.Clone()
		f.lastHost = r.Host
	}
	write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	admin := r.Header.Get("Authorization") == `MediaBrowser Token="`+adminTok+`"`
	switch {
	case r.Method == "POST" && r.URL.Path == "/Users/AuthenticateByName":
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["Username"] != "admin" || in["Pw"] != adminPw {
			w.WriteHeader(401)
			return
		}
		write(map[string]any{"AccessToken": adminTok})
	case r.Method == "POST" && r.URL.Path == InitiatePath:
		f.initiates++
		res := map[string]any{"Code": fmt.Sprintf("%06d", f.initiates), "Secret": "s3cret", "Authenticated": false}
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			// Jellyfin (Kestrel) compresses when asked; the live bug of
			// 2026-10-06 was the proxy reading those bytes as JSON.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			json.NewEncoder(zw).Encode(res)
			zw.Close()
			return
		}
		write(res)
	case r.URL.Path == "/other":
		w.WriteHeader(418)
		io.WriteString(w, "teapot")
	case !admin:
		w.WriteHeader(401)
	case r.Method == "GET" && r.URL.Path == "/Users":
		write(f.users)
	case r.Method == "POST" && r.URL.Path == "/Users/New":
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		u := map[string]any{"Name": in["Name"], "Id": "u-" + in["Name"], "Policy": map[string]any{"IsAdministrator": false, "EnableAllFolders": false, "MaxActiveSessions": 0}}
		f.users = append(f.users, u)
		write(u)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/Users/") && strings.HasSuffix(r.URL.Path, "/Policy"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/Users/"), "/Policy")
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		f.policies[id] = p
		w.WriteHeader(204)
	case r.Method == "POST" && r.URL.Path == "/QuickConnect/Authorize":
		f.authorized = append(f.authorized, r.URL.Query().Get("code")+":"+r.URL.Query().Get("userId"))
		write(true)
	case r.Method == "GET" && r.URL.Path == "/System/Configuration":
		write(f.config)
	case r.Method == "POST" && r.URL.Path == "/System/Configuration":
		json.NewDecoder(r.Body).Decode(&f.config)
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
	}
}

type rig struct {
	jf     *fakeJellyfin
	signer *meshtoken.Signer
	front  *httptest.Server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	jf := newFakeJellyfin()
	back := httptest.NewServer(jf)
	t.Cleanup(back.Close)
	u, _ := url.Parse(back.URL)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := meshtoken.NewSigner(priv)
	v, err := meshtoken.NewVerifier(signer.ID())
	if err != nil {
		t.Fatal(err)
	}
	h := New(u, v, NewJellyfin(back.URL, "admin", adminPw), "media")
	h.now = func() time.Time { return now }
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)
	return &rig{jf: jf, signer: signer, front: front}
}

// initiate posts an Initiate through the proxy as id (no token when
// id.Name is empty), returning the status and the decoded body.
func (r *rig) initiate(t *testing.T, id cert.Identity, token string) (int, quickConnectResult) {
	t.Helper()
	req, _ := http.NewRequest("POST", r.front.URL+InitiatePath, nil)
	req.Host = host
	req.Header.Set("Authorization", `MediaBrowser Client="TV", Device="tv", DeviceId="d1", Version="1"`)
	if id.Name != "" && token == "" {
		var err error
		if token, err = r.signer.Mint(id, host, now); err != nil {
			t.Fatal(err)
		}
	}
	if token != "" {
		req.Header.Set(meshtoken.Header, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var qc quickConnectResult
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &qc)
	return resp.StatusCode, qc
}

func TestMediaDeviceIsLoggedInAsItself(t *testing.T) {
	r := newRig(t)
	status, qc := r.initiate(t, device("tv-parents", "media"), "")
	if status != 200 || qc.Code != "000001" || qc.Secret != "s3cret" {
		t.Fatalf("initiate: %d %+v; the response must pass through intact", status, qc)
	}
	if got := r.jf.authorized; len(got) != 1 || got[0] != "000001:u-tv-parents" {
		t.Fatalf("authorized = %v, want the code approved for the device's own user", got)
	}
	p := r.jf.policies["u-tv-parents"]
	if p == nil {
		t.Fatal("no policy written for the new user")
	}
	for k, want := range map[string]bool{"IsAdministrator": false, "IsHidden": true, "EnableAllFolders": true, "EnableRemoteAccess": true} {
		if got, _ := p[k].(bool); got != want {
			t.Errorf("policy %s = %v, want %v", k, p[k], want)
		}
	}
	if _, ok := p["MaxActiveSessions"]; !ok {
		t.Error("policy must be the server's own object with fields set, not a fresh one (Jellyfin replaces the whole policy)")
	}
	if r.jf.seenHeaders.Get(meshtoken.Header) != "" {
		t.Error("token forwarded to Jellyfin")
	}
	if r.jf.lastHost != host {
		t.Errorf("Host toward Jellyfin = %q, want %q", r.jf.lastHost, host)
	}

	// Second time: same user, no second create.
	r.initiate(t, device("tv-parents", "media"), "")
	if n := len(r.jf.users); n != 4 {
		t.Fatalf("users = %d, want 4 (no duplicate)", n)
	}
	if got := r.jf.authorized; len(got) != 2 || got[1] != "000002:u-tv-parents" {
		t.Fatalf("authorized = %v", got)
	}
}

func TestApprovesWhenTheClientAcceptsGzip(t *testing.T) {
	r := newRig(t)
	req, _ := http.NewRequest("POST", r.front.URL+InitiatePath, nil)
	req.Host = host
	req.Header.Set("Accept-Encoding", "gzip") // OkHttp's default on the TV app
	req.Header.Set("Authorization", `MediaBrowser Client="TV", Device="tv", DeviceId="d1", Version="1"`)
	tok, _ := r.signer.Mint(device("tv", "media"), host, now)
	req.Header.Set(meshtoken.Header, tok)
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var qc quickConnectResult
	if resp.StatusCode != 200 || json.Unmarshal(body, &qc) != nil || qc.Code != "000001" {
		t.Fatalf("client got %d %q; want the plain result", resp.StatusCode, body)
	}
	if got := r.jf.authorized; len(got) != 1 || got[0] != "000001:u-tv" {
		t.Fatalf("authorized = %v", got)
	}
}

func TestExistingNonAdminUserIsReused(t *testing.T) {
	r := newRig(t)
	r.initiate(t, device("guest", "media"), "")
	if got := r.jf.authorized; len(got) != 1 || got[0] != "000001:u-guest" {
		t.Fatalf("authorized = %v", got)
	}
	if len(r.jf.users) != 3 {
		t.Fatal("created a user that already existed")
	}
}

func TestNeverOntoAnAdministrator(t *testing.T) {
	r := newRig(t)
	for _, name := range []string{"mar", "Admin"} {
		status, qc := r.initiate(t, device(name, "media"), "")
		if status != 200 || qc.Code == "" {
			t.Fatalf("%s: initiate must still pass through: %d %+v", name, status, qc)
		}
	}
	if len(r.jf.authorized) != 0 {
		t.Fatalf("authorized = %v, want nothing: an appliance never becomes an admin", r.jf.authorized)
	}
}

func TestNoApprovalWithoutAGoodToken(t *testing.T) {
	r := newRig(t)
	other := meshtoken.NewSigner(func() ed25519.PrivateKey { _, p, _ := ed25519.GenerateKey(rand.Reader); return p }())
	forged, _ := other.Mint(device("tv-parents", "media"), host, now)
	wrongAud, _ := r.signer.Mint(device("tv-parents", "media"), "sonarr.gw.mesh.internal", now)
	stale, _ := r.signer.Mint(device("tv-parents", "media"), host, now.Add(-10*time.Minute))
	cases := map[string]string{
		"no token":  "",
		"forged":    forged,
		"wrong aud": wrongAud,
		"stale":     stale,
		"garbage":   "x.y.z",
	}
	for name, tok := range cases {
		id := cert.Identity{}
		if tok != "" {
			id = device("tv-parents", "media")
		}
		status, qc := r.initiate(t, id, tok)
		if status != 200 || qc.Code == "" {
			t.Errorf("%s: initiate must pass through: %d %+v", name, status, qc)
		}
	}
	if len(r.jf.authorized) != 0 || len(r.jf.users) != 3 {
		t.Fatalf("authorized = %v users = %d; nothing may be approved or created", r.jf.authorized, len(r.jf.users))
	}
}

func TestOwnerDevicesKeepManualApproval(t *testing.T) {
	r := newRig(t)
	status, qc := r.initiate(t, device("marius-mac", "admins"), "")
	if status != 200 || qc.Code == "" {
		t.Fatalf("initiate: %d %+v", status, qc)
	}
	if len(r.jf.authorized) != 0 {
		t.Fatalf("authorized = %v; an admins device approves by hand, as the person", r.jf.authorized)
	}
}

func TestOtherRequestsAreForwardedUntouched(t *testing.T) {
	r := newRig(t)
	req, _ := http.NewRequest("GET", r.front.URL+"/other", nil)
	tok, _ := r.signer.Mint(device("tv-parents", "media"), host, now)
	req.Header.Set(meshtoken.Header, tok)
	req.Header.Set("X-Mesh-Name", "tv-parents")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 418 || string(body) != "teapot" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if r.jf.seenHeaders.Get(meshtoken.Header) != "" || r.jf.seenHeaders.Get("X-Mesh-Name") != "tv-parents" {
		t.Errorf("headers toward Jellyfin: token must go, the rest stays: %v", r.jf.seenHeaders)
	}
	if len(r.jf.authorized) != 0 {
		t.Fatal("approved something off a non-Initiate request")
	}
}

func TestEnableQuickConnectIsIdempotent(t *testing.T) {
	r := newRig(t)
	jf := NewJellyfin(r.jf.base(t), "admin", adminPw)
	for i := 0; i < 2; i++ {
		if err := jf.EnableQuickConnect(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if on, _ := r.jf.config["QuickConnectAvailable"].(bool); !on {
		t.Fatal("not enabled")
	}
	if r.jf.config["ServerName"] != "x" {
		t.Fatal("the rest of the configuration must round-trip")
	}
}

func TestReauthenticatesOnceOn401(t *testing.T) {
	r := newRig(t)
	jf := NewJellyfin(r.jf.base(t), "admin", adminPw)
	jf.token = "expired"
	id, err := jf.EnsureUser(t.Context(), "guest")
	if err != nil || id != "u-guest" {
		t.Fatalf("EnsureUser = %q, %v", id, err)
	}
	if jf.token != adminTok {
		t.Fatalf("token = %q, want re-minted", jf.token)
	}
}

// base is the fake's URL; the rig only keeps the proxy's, so recover
// it from the handler under test.
func (f *fakeJellyfin) base(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	return s.URL
}
