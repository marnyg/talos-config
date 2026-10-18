package parent_test

import (
	"bufio"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marnyg/talos-config/actors/parent"
)

// issuer is a fake siwe-oidc: discovery, an /authorize that redirects
// straight back with a code (the wallet page stands in for by a query
// param `as`), a /token that checks PKCE and mints an RS256 id token,
// and /jwks.
type issuer struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	mu    sync.Mutex
	codes map[string]struct {
		challenge, nonce, redirect, client, sub, user string
		groups                                        []string
	}
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	is := &issuer{key: key}
	is.codes = map[string]struct {
		challenge, nonce, redirect, client, sub, user string
		groups                                        []string
	}{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 is.srv.URL,
			"authorization_endpoint": is.srv.URL + "/authorize",
			"token_endpoint":         is.srv.URL + "/token",
			"jwks_uri":               is.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("response_type") != "code" {
			http.Error(w, "pkce required", 400)
			return
		}
		// Who logs in: the test sets a cookie on the issuer's origin.
		as, _ := r.Cookie("as")
		if as == nil {
			http.Error(w, "no wallet", 403)
			return
		}
		sub, user, groups := as.Value, "someone", []string{}
		if i := strings.Index(sub, "|"); i >= 0 {
			groups = strings.Split(sub[i+1:], ",")
			sub = sub[:i]
		}
		code := base64.RawURLEncoding.EncodeToString([]byte(time.Now().String() + sub))
		is.mu.Lock()
		is.codes[code] = struct {
			challenge, nonce, redirect, client, sub, user string
			groups                                        []string
		}{q.Get("code_challenge"), q.Get("nonce"), q.Get("redirect_uri"), q.Get("client_id"), sub, user, groups}
		is.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(q.Get("state")), http.StatusSeeOther)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		is.mu.Lock()
		c, ok := is.codes[r.FormValue("code")]
		delete(is.codes, r.FormValue("code"))
		is.mu.Unlock()
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		if !ok || c.redirect != r.FormValue("redirect_uri") || c.client != r.FormValue("client_id") ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		claims := map[string]any{
			"iss": is.srv.URL, "aud": c.client, "sub": c.sub, "preferred_username": c.user, "groups": c.groups,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": c.nonce,
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": is.jwt(t, claims), "access_token": "x", "token_type": "Bearer"})
	})
	is.srv = httptest.NewServer(mux)
	t.Cleanup(is.srv.Close)
	return is
}

func (is *issuer) jwt(t *testing.T, claims map[string]any) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, is.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// browser is one logged-in client: a cookie jar that follows the
// redirects through the issuer.
func browser(t *testing.T, is *issuer, ui *httptest.Server, as string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	iu, _ := url.Parse(is.srv.URL)
	jar.SetCookies(iu, []*http.Cookie{{Name: "as", Value: as}})
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	resp, err := c.Get(ui.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "sessions") {
		t.Fatalf("login as %s: %d %s", as, resp.StatusCode, b)
	}
	return c
}

func (r *rig) web(t *testing.T, is *issuer) *httptest.Server {
	t.Helper()
	login := &parent.Login{Issuer: is.srv.URL, ClientID: "agents", Key: []byte(strings.Repeat("k", 32))}
	w := &parent.Web{P: r.p, Store: r.store, Login: login}
	srv := httptest.NewServer(w.Handler())
	login.RedirectURL = srv.URL + "/callback"
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, c *http.Client, u string, v any) int {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil && resp.StatusCode/100 == 2 {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func postJSON(t *testing.T, c *http.Client, u string, body any, v any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", u[:strings.Index(u[8:], "/")+8])
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil && resp.StatusCode/100 == 2 {
		json.NewDecoder(resp.Body).Decode(v)
	}
	return resp.StatusCode
}

// TestWebEndToEnd: login through the issuer; submit from the owner's
// browser; the stream tails to `end`; a stranger gets 404, a viewer
// 200 on the session but 403 on steer, a steerer's steer lands with
// their wallet; admins see it in the list; unauthenticated is 401/
// redirect; a cross-site POST is refused.
func TestWebEndToEnd(t *testing.T) {
	r := setup(t, true)
	r.listen(t)
	is := newIssuer(t)
	ui := r.web(t, is)

	// Unauthenticated.
	noauth := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, _ := noauth.Get(ui.URL + "/"); resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/login?next=") {
		t.Fatalf("unauthenticated page: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if code := getJSON(t, noauth, ui.URL+"/api/sessions", nil); code != 401 {
		t.Fatalf("unauthenticated api: %d", code)
	}

	me := browser(t, is, ui, owner)
	viewer := browser(t, is, ui, "0x1111111111111111111111111111111111111111")
	steerer := browser(t, is, ui, "0x2222222222222222222222222222222222222222")
	stranger := browser(t, is, ui, "0x3333333333333333333333333333333333333333")
	admin := browser(t, is, ui, "0x4444444444444444444444444444444444444444|admins")

	var meP parent.Principal
	getJSON(t, me, ui.URL+"/api/me", &meP)
	if meP.Wallet != owner || meP.Username != "someone" {
		t.Fatalf("me %+v", meP)
	}

	var sess parent.Session
	if code := postJSON(t, me, ui.URL+"/api/sessions", map[string]string{"prompt": "hang around"}, &sess); code != 201 {
		t.Fatalf("submit: %d", code)
	}
	if sess.Owner != owner || sess.Status != parent.StatusPending {
		t.Fatalf("submitted %+v", sess)
	}
	su := ui.URL + "/api/sessions/" + sess.ID
	waitFor(t, "running", func() bool { return r.status(sess.ID) == parent.StatusRunning })

	// ACL before sharing.
	if code := getJSON(t, stranger, su, nil); code != 404 {
		t.Fatalf("stranger: %d", code)
	}
	if code := getJSON(t, viewer, su, nil); code != 404 {
		t.Fatalf("viewer before share: %d", code)
	}
	if code := getJSON(t, admin, su, nil); code != 200 {
		t.Fatalf("admin: %d", code)
	}
	if code := postJSON(t, admin, su+"/steer", map[string]string{"message": "x"}, nil); code != 403 {
		t.Fatalf("admin steer: %d", code)
	}
	// Share: only the owner may.
	if code := postJSON(t, viewer, su+"/shares", parent.Share{Principal: "0x1111111111111111111111111111111111111111", Right: "steer"}, nil); code != 404 {
		t.Fatalf("viewer shares to self: %d", code)
	}
	if code := postJSON(t, me, su+"/shares", parent.Share{Principal: "0x1111111111111111111111111111111111111111", Right: "view"}, nil); code != 200 {
		t.Fatalf("share view: %d", code)
	}
	if code := postJSON(t, me, su+"/shares", parent.Share{Principal: "0x2222222222222222222222222222222222222222", Right: "steer"}, nil); code != 200 {
		t.Fatalf("share steer: %d", code)
	}
	if code := getJSON(t, viewer, su, nil); code != 200 {
		t.Fatalf("viewer after share: %d", code)
	}
	if code := postJSON(t, viewer, su+"/steer", map[string]string{"message": "x"}, nil); code != 403 {
		t.Fatalf("viewer steer: %d", code)
	}
	var shares []parent.Share
	if code := getJSON(t, admin, su+"/shares", &shares); code != 200 || len(shares) != 2 {
		t.Fatalf("shares: %d %+v", code, shares)
	}
	// A cross-site POST is refused even with the cookie.
	b, _ := json.Marshal(map[string]string{"message": "evil"})
	req, _ := http.NewRequest(http.MethodPost, su+"/steer", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	if resp, _ := steerer.Do(req); resp.StatusCode != 403 {
		t.Fatalf("cross-site steer: %d", resp.StatusCode)
	}

	// The live tail from the viewer; the steerer steers; the owner aborts.
	waitFor(t, "message_start", func() bool {
		evs, _ := r.store.Events(sess.ID, 0, 0)
		return strings.HasSuffix(types(evs), "message_start")
	})
	resp, err := viewer.Get(su + "/stream?after=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream content-type %q", resp.Header.Get("Content-Type"))
	}
	lines := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	seen := map[string]int{}
	var from string
	collect := func(until string) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed before %s; seen %v", until, seen)
				}
				if strings.HasPrefix(l, "event: ") {
					seen[l[7:]]++
					if l[7:] == until {
						return
					}
				}
				if strings.HasPrefix(l, "data: ") && strings.Contains(l, `"type":"steer"`) {
					var ev parent.Event
					json.Unmarshal([]byte(l[6:]), &ev)
					from = ev.From
				}
			case <-deadline:
				t.Fatalf("no %s on the stream; seen %v", until, seen)
			}
		}
	}
	if code := postJSON(t, steerer, su+"/steer", map[string]string{"message": "go left"}, nil); code != 200 {
		t.Fatalf("steerer steer: %d", code)
	}
	waitFor(t, "steer stamped", func() bool {
		evs, _ := r.store.Events(sess.ID, 0, 0)
		return strings.Contains(types(evs), "steer")
	})
	if code := postJSON(t, me, su+"/abort", nil, nil); code != 200 {
		t.Fatalf("owner abort: %d", code)
	}
	collect("end")
	if from != "0x2222222222222222222222222222222222222222" {
		t.Fatalf("steer stamped from %q", from)
	}
	if seen["line"] < 8 {
		t.Fatalf("stream lines %v", seen)
	}
	waitFor(t, "aborted", func() bool { return r.status(sess.ID) == parent.StatusAborted })

	// The list: admin sees it, stranger does not; search finds the prompt.
	var list []parent.Session
	getJSON(t, admin, ui.URL+"/api/sessions", &list)
	if len(list) != 1 || list[0].ID != sess.ID {
		t.Fatalf("admin list %+v", list)
	}
	getJSON(t, stranger, ui.URL+"/api/sessions", &list)
	if len(list) != 0 {
		t.Fatalf("stranger list %+v", list)
	}
	var hits []parent.Hit
	getJSON(t, viewer, ui.URL+"/api/search?q=hang", &hits)
	if len(hits) == 0 {
		t.Fatal("viewer search found nothing")
	}
	// The session file, once shipped.
	waitFor(t, "file", func() bool { x, _ := r.store.Get(sess.ID); return x.File != "" })
	if resp, _ := viewer.Get(su + "/file"); resp.StatusCode != 200 {
		t.Fatalf("file: %d", resp.StatusCode)
	}
	// Pages render.
	for _, c := range []*http.Client{me, viewer, admin} {
		resp, _ := c.Get(ui.URL + "/s/" + sess.ID)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), "hang around") {
			t.Fatalf("session page: %d", resp.StatusCode)
		}
	}
	if resp, _ := stranger.Get(ui.URL + "/s/" + sess.ID); resp.StatusCode != 404 {
		t.Fatalf("stranger page: %d", resp.StatusCode)
	}
	// Logout.
	req, _ = http.NewRequest(http.MethodPost, ui.URL+"/logout", nil)
	req.Header.Set("Origin", ui.URL)
	// Do not follow the redirect home: the issuer's cookie would log
	// the browser straight back in.
	me.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if resp, _ := me.Do(req); resp.StatusCode != 303 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if code := getJSON(t, me, ui.URL+"/api/me", nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}
