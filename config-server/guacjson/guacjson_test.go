package guacjson

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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

const host = "guac.gw.mesh.internal"

var (
	now = time.Unix(1_800_000_000, 0)
	key = []byte("0123456789abcdef")
)

func device(name string, groups ...string) cert.Identity {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return cert.Identity{Key: cert.NewEdSigner(priv).ActorID(), Name: name, Groups: groups}
}

// fakeWebapp records what reached it: the last token POST's form and
// headers, and everything else's path.
type fakeWebapp struct {
	mu      sync.Mutex
	forms   []url.Values
	headers []http.Header
	other   []string
}

func (f *fakeWebapp) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost && r.URL.Path == DefaultTokensPath {
			body, _ := io.ReadAll(r.Body)
			if int64(len(body)) != r.ContentLength {
				t.Errorf("Content-Length %d, body %d", r.ContentLength, len(body))
			}
			form, _ := url.ParseQuery(string(body))
			f.forms = append(f.forms, form)
			f.headers = append(f.headers, r.Header.Clone())
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"authToken":"t"}`)
			return
		}
		f.other = append(f.other, r.URL.Path)
		_, _ = io.WriteString(w, "ok")
	})
}

type rig struct {
	t      *testing.T
	fake   *fakeWebapp
	signer *meshtoken.Signer
	srv    *httptest.Server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fake := &fakeWebapp{}
	up := httptest.NewServer(fake.handler(t))
	t.Cleanup(up.Close)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := meshtoken.NewSigner(priv)
	v, err := meshtoken.NewVerifier(signer.ID())
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMinter(key, Connection{Name: "win2k25", Protocol: "rdp", Parameters: map[string]string{"hostname": "vm", "password": "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(up.URL)
	h := New(u, v, m, "admins", "")
	h.now = func() time.Time { return now }
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &rig{t: t, fake: fake, signer: signer, srv: srv}
}

// post sends a form POST through the proxy, as the client would, with
// the identity headers the gateway would have set for id (nil: none).
func (r *rig) post(path string, form url.Values, id *cert.Identity) *http.Response {
	r.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+path, strings.NewReader(form.Encode()))
	req.Host = host
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if id != nil {
		tok, err := r.signer.Mint(*id, host, now)
		if err != nil {
			r.t.Fatal(err)
		}
		req.Header.Set(meshtoken.Header, tok)
		req.Header.Set("X-Mesh-Name", id.Name)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (r *rig) lastForm() url.Values {
	r.fake.mu.Lock()
	defer r.fake.mu.Unlock()
	if len(r.fake.forms) == 0 {
		r.t.Fatal("nothing reached the webapp")
	}
	return r.fake.forms[len(r.fake.forms)-1]
}

func TestAdminDeviceGetsDataBlob(t *testing.T) {
	r := newRig(t)
	id := device("laptop", "admins", "media")
	resp := r.post(DefaultTokensPath, url.Values{}, &id)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	form := r.lastForm()
	if form.Has("username") || !form.Has("data") {
		t.Fatalf("form: %v", form)
	}
	js, err := Decode(key, form.Get("data"))
	if err != nil {
		t.Fatal(err)
	}
	var ud userData
	if err := json.Unmarshal(js, &ud); err != nil {
		t.Fatal(err)
	}
	if ud.Username != "laptop" || !ud.SingleUse || ud.Expires != now.Add(BlobTTL).UnixMilli() {
		t.Fatalf("user data: %+v", ud)
	}
	c, ok := ud.Connections["win2k25"]
	if !ok || c.Protocol != "rdp" || c.Parameters["password"] != "pw" {
		t.Fatalf("connection: %+v", ud.Connections)
	}
	// The webapp never sees the ambient identity.
	hdr := r.fake.headers[len(r.fake.headers)-1]
	for _, name := range identityHeaders {
		if hdr.Get(name) != "" {
			t.Errorf("%s leaked to the webapp", name)
		}
	}
}

func TestStaleTokenStillGetsDataAndKeepsToken(t *testing.T) {
	// The client re-authenticates with its current token on every
	// route; our data rides along so a dead session becomes a new login.
	r := newRig(t)
	id := device("phone", "admins")
	r.post(DefaultTokensPath, url.Values{"token": {"old"}}, &id)
	form := r.lastForm()
	if form.Get("token") != "old" || !form.Has("data") {
		t.Fatalf("form: %v", form)
	}
}

func TestLeftAloneWhenNotQualified(t *testing.T) {
	cases := map[string]struct {
		id   *cert.Identity
		form url.Values
	}{
		"no gateway token":          {nil, url.Values{}},
		"outside the group":         {ptr(device("tv", "media")), url.Values{}},
		"brought its own username":  {ptr(device("laptop", "admins")), url.Values{"username": {"x"}, "password": {"y"}}},
		"brought its own data blob": {ptr(device("laptop", "admins")), url.Values{"data": {"zzz"}}},
	}
	for name, c := range cases {
		r := newRig(t)
		r.post(DefaultTokensPath, c.form, c.id)
		form := r.lastForm()
		if c.form.Has("data") {
			if form.Get("data") != "zzz" {
				t.Errorf("%s: data replaced: %v", name, form)
			}
			continue
		}
		if form.Has("data") {
			t.Errorf("%s: data added: %v", name, form)
		}
		if form.Encode() != c.form.Encode() {
			t.Errorf("%s: body changed: %v != %v", name, form, c.form)
		}
	}
}

func TestForeignGatewayRefused(t *testing.T) {
	r := newRig(t)
	_, p, _ := ed25519.GenerateKey(rand.Reader)
	other := meshtoken.NewSigner(p)
	tok, _ := other.Mint(device("laptop", "admins"), host, now)
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+DefaultTokensPath, strings.NewReader(""))
	req.Host = host
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(meshtoken.Header, tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if r.lastForm().Has("data") {
		t.Fatal("a foreign gateway's token minted a login")
	}
}

func TestOtherRequestsForwardUntouched(t *testing.T) {
	r := newRig(t)
	id := device("laptop", "admins")
	// A JSON POST to the token path is not the client's form; a GET
	// elsewhere is the tunnel or static assets.
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+DefaultTokensPath, strings.NewReader(`{}`))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	tok, _ := r.signer.Mint(id, host, now)
	req.Header.Set(meshtoken.Header, tok)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if r.lastForm().Has("data") {
		t.Fatal("non-form POST got data")
	}
	resp, err := http.Get(r.srv.URL + "/guacamole/websocket-tunnel")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	r.fake.mu.Lock()
	defer r.fake.mu.Unlock()
	if len(r.fake.other) != 1 || r.fake.other[0] != "/guacamole/websocket-tunnel" {
		t.Fatalf("other: %v", r.fake.other)
	}
}

func TestEncodeDecodeRejectsTampering(t *testing.T) {
	blob, err := Encode(key, []byte(`{"username":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode([]byte("fedcba9876543210"), blob); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := NewMinter([]byte("short"), Connection{Name: "a", Protocol: "rdp"}); err == nil {
		t.Fatal("short key accepted")
	}
}

func ptr(id cert.Identity) *cert.Identity { return &id }
