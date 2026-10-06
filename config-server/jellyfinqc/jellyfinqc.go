// Package jellyfinqc logs an appliance into Jellyfin from its mesh
// identity (talos-config-95la). It is a reverse proxy in front of the
// Jellyfin container — the backend of the jellyfin.gw Ingress — that
// does one thing beyond forwarding: when a device starts a Quick
// Connect, it authorizes that Quick Connect as the device's own
// Jellyfin user before the response goes back.
//
// Why this shape. Jellyfin clients cannot be logged in by a header:
// the TV app needs a Jellyfin access token, and the only ways to one
// are a password (AuthenticateByName) or Quick Connect, where a code
// shown on the device is approved by someone already signed in. That
// approval is the hook. The gateway terminates the identity stream
// and signs who the caller is into X-Mesh-Token per request (ADR-0032);
// this proxy verifies the token the way the bridge does — against the
// gateway ids pinned in git — and, on `POST /QuickConnect/Initiate`
// from a device in the configured group, calls
// `POST /QuickConnect/Authorize?code=…&userId=<that device's user>`
// with the admin credential the pod already holds. The device never
// sees a password; the user it becomes is named after the device
// (`name` in the member cert, so a re-keyed device keeps its watch
// state), created on first sight as a non-administrator hidden from
// the login screen, with every library enabled.
//
// What it refuses: a request without a valid token is proxied but not
// approved (Quick Connect then waits for a human, Jellyfin's own
// behaviour — the token is a shortcut, never a lockout); a device
// outside the group likewise (owner devices keep approving by hand,
// as the person); a device whose name is an existing *administrator*
// user is never approved onto it — an appliance never holds an admin
// token.
//
// Everything else is forwarded as-is, streams included. Jellyfin's
// own auth decides the rest; the X-Mesh-Token header is dropped on
// the way through.
package jellyfinqc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/config-server/meshtoken"
)

// InitiatePath is the Quick Connect start the proxy watches.
const InitiatePath = "/QuickConnect/Initiate"

// approveTimeout bounds the admin calls made inside one Initiate
// response: ensure-user plus authorize, each a local round trip.
const approveTimeout = 10 * time.Second

// Handler is the proxy. Zero value is not usable; use New.
type Handler struct {
	proxy    *httputil.ReverseProxy
	verifier *meshtoken.Verifier
	jf       *Jellyfin
	group    string
	now      func() time.Time
}

type identityKey struct{}

// New builds the proxy to upstream (the Jellyfin listener). verifier
// pins the gateway keys; group is the member-cert group whose devices
// are logged in automatically (the shared-appliance group).
func New(upstream *url.URL, verifier *meshtoken.Verifier, jf *Jellyfin, group string) *Handler {
	if verifier == nil || jf == nil || group == "" {
		panic("jellyfinqc.New: verifier, jellyfin client and group are required")
	}
	h := &Handler{verifier: verifier, jf: jf, group: group, now: time.Now}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.Host = pr.In.Host // what Jellyfin saw when nginx dialed it directly
			pr.Out.Header.Del(meshtoken.Header)
		},
		ModifyResponse: h.modifyResponse,
		FlushInterval:  -1, // media streams: never buffer
	}
	return h
}

// ServeHTTP verifies the token on an Initiate and carries the verified
// claims to the response hook; everything else is a plain forward.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == InitiatePath {
		if c, ok := h.identity(r); ok {
			r = r.WithContext(context.WithValue(r.Context(), identityKey{}, c))
		}
	}
	h.proxy.ServeHTTP(w, r)
}

// identity resolves the request's device, or says why not (log only:
// the request proceeds either way).
func (h *Handler) identity(r *http.Request) (meshtoken.Claims, bool) {
	tok := r.Header.Get(meshtoken.Header)
	if tok == "" {
		log.Printf("quickconnect: initiate from %s without a gateway token; left to manual approval", r.RemoteAddr)
		return meshtoken.Claims{}, false
	}
	c, err := h.verifier.Verify(tok, r.Host, h.now())
	if err != nil {
		log.Printf("quickconnect: initiate for %s: token refused: %v", r.Host, err)
		return meshtoken.Claims{}, false
	}
	if !c.HasGroup(h.group) {
		log.Printf("quickconnect: initiate by %s (%s) with %v, not %s; left to manual approval", c.Subject, c.Name, c.Groups, h.group)
		return meshtoken.Claims{}, false
	}
	return c, true
}

// quickConnectResult is the slice of Jellyfin's QuickConnectResult the
// approval needs.
type quickConnectResult struct {
	Code   string `json:"Code"`
	Secret string `json:"Secret"`
}

// modifyResponse approves a successful Initiate for the device the
// request context carries. The body is read and put back unchanged;
// an approval failure is logged and the response still returns, so
// the client falls back to waiting for a human.
func (h *Handler) modifyResponse(resp *http.Response) error {
	req := resp.Request
	if req == nil || req.Method != http.MethodPost || req.URL.Path != InitiatePath || resp.StatusCode != http.StatusOK {
		return nil
	}
	c, ok := req.Context().Value(identityKey{}).(meshtoken.Claims)
	if !ok {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprint(len(body)))

	var qc quickConnectResult
	if err := json.Unmarshal(body, &qc); err != nil || qc.Code == "" {
		log.Printf("quickconnect: initiate by %s (%s): unreadable result, not approved", c.Subject, c.Name)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), approveTimeout)
	defer cancel()
	userID, err := h.jf.EnsureUser(ctx, c.Name)
	if err != nil {
		log.Printf("quickconnect: initiate by %s (%s): user: %v; not approved", c.Subject, c.Name, err)
		return nil
	}
	if err := h.jf.Authorize(ctx, qc.Code, userID); err != nil {
		log.Printf("quickconnect: initiate by %s (%s): authorize: %v; not approved", c.Subject, c.Name, err)
		return nil
	}
	log.Printf("quickconnect: device %s (%s) logged in as jellyfin user %q", c.Subject, c.Name, c.Name)
	return nil
}

// Jellyfin is the admin-side client: the handful of calls the approval
// needs, authenticated as the local admin. The access token is minted
// lazily and re-minted once on a 401.
type Jellyfin struct {
	base     string
	http     *http.Client
	user     string
	password string

	mu    sync.Mutex
	token string
}

// NewJellyfin targets base (e.g. http://127.0.0.1:8096) as user with
// password. The password is only ever sent to base.
func NewJellyfin(base, user, password string) *Jellyfin {
	return &Jellyfin{
		base:     strings.TrimRight(base, "/"),
		http:     &http.Client{Timeout: 15 * time.Second},
		user:     user,
		password: password,
	}
}

// ErrAdminUser is returned when the device's name is an administrator.
var ErrAdminUser = errors.New("jellyfin: user is an administrator; refusing to log an appliance in as it")

// jfUser is the slice of UserDto the approval reads and rewrites.
type jfUser struct {
	Name   string         `json:"Name"`
	ID     string         `json:"Id"`
	Policy map[string]any `json:"Policy"`
}

// EnsureUser returns the id of the Jellyfin user named name, creating
// it if absent: non-administrator, hidden from the login screen, all
// libraries. Names compare case-insensitively like Jellyfin's own
// uniqueness rule. An existing administrator is ErrAdminUser.
func (j *Jellyfin) EnsureUser(ctx context.Context, name string) (string, error) {
	var users []jfUser
	if _, err := j.do(ctx, http.MethodGet, "/Users", nil, &users); err != nil {
		return "", err
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, name) {
			if admin, _ := u.Policy["IsAdministrator"].(bool); admin {
				return "", ErrAdminUser
			}
			return u.ID, nil
		}
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return "", err
	}
	var created jfUser
	if _, err := j.do(ctx, http.MethodPost, "/Users/New", map[string]string{"Name": name, "Password": hex.EncodeToString(pw)}, &created); err != nil {
		return "", fmt.Errorf("create %q: %w", name, err)
	}
	if created.Policy == nil {
		created.Policy = map[string]any{}
	}
	created.Policy["IsAdministrator"] = false
	created.Policy["IsHidden"] = true
	created.Policy["EnableAllFolders"] = true
	created.Policy["EnableRemoteAccess"] = true
	if _, err := j.do(ctx, http.MethodPost, "/Users/"+created.ID+"/Policy", created.Policy, nil); err != nil {
		return "", fmt.Errorf("policy for %q: %w", name, err)
	}
	log.Printf("jellyfin: created user %q (%s) for a device", name, created.ID)
	return created.ID, nil
}

// Authorize approves the Quick Connect code for userID.
func (j *Jellyfin) Authorize(ctx context.Context, code, userID string) error {
	q := url.Values{"code": {code}, "userId": {userID}}
	var ok bool
	if _, err := j.do(ctx, http.MethodPost, "/QuickConnect/Authorize?"+q.Encode(), nil, &ok); err != nil {
		return err
	}
	if !ok {
		return errors.New("jellyfin: authorize returned false (unknown or expired code)")
	}
	return nil
}

// EnableQuickConnect sets QuickConnectAvailable in the server
// configuration if it is off. Declared here rather than left to the
// web UI: the setting lives on the app volume and a wipe forgets it.
func (j *Jellyfin) EnableQuickConnect(ctx context.Context) error {
	var cfg map[string]any
	if _, err := j.do(ctx, http.MethodGet, "/System/Configuration", nil, &cfg); err != nil {
		return err
	}
	if on, _ := cfg["QuickConnectAvailable"].(bool); on {
		return nil
	}
	cfg["QuickConnectAvailable"] = true
	if _, err := j.do(ctx, http.MethodPost, "/System/Configuration", cfg, nil); err != nil {
		return err
	}
	log.Printf("jellyfin: enabled Quick Connect")
	return nil
}

// Ready reports whether the server answers /health.
func (j *Jellyfin) Ready(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.base+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// do performs one authenticated call, decoding a JSON body into out
// when out is non-nil. A 401 re-authenticates once and retries.
func (j *Jellyfin) do(ctx context.Context, method, path string, in, out any) (int, error) {
	for attempt := 0; ; attempt++ {
		tok, err := j.accessToken(ctx, attempt > 0)
		if err != nil {
			return 0, err
		}
		status, body, err := j.call(ctx, method, path, in, `MediaBrowser Token="`+tok+`"`)
		if err != nil {
			return 0, err
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if status < 200 || status > 299 {
			return status, fmt.Errorf("jellyfin: %s %s: %d %s", method, path, status, strings.TrimSpace(string(body)))
		}
		if out != nil {
			if err := json.Unmarshal(body, out); err != nil {
				return status, fmt.Errorf("jellyfin: %s %s: %w", method, path, err)
			}
		}
		return status, nil
	}
}

// accessToken returns the admin token, minting it on first use or
// when fresh is set.
func (j *Jellyfin) accessToken(ctx context.Context, fresh bool) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.token != "" && !fresh {
		return j.token, nil
	}
	auth := `MediaBrowser Client="jellyfinqc", Device="k8s", DeviceId="jellyfinqc", Version="1"`
	status, body, err := j.call(ctx, http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": j.user, "Pw": j.password}, auth)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("jellyfin: authenticate as %q: %d", j.user, status)
	}
	var res struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := json.Unmarshal(body, &res); err != nil || res.AccessToken == "" {
		return "", errors.New("jellyfin: authenticate: no access token in reply")
	}
	j.token = res.AccessToken
	return j.token, nil
}

func (j *Jellyfin) call(ctx context.Context, method, path string, in any, authorization string) (int, []byte, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, j.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", authorization)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
}
