// Package guacjson logs a device into Apache Guacamole from its mesh
// identity (talos-config-5is2, ADR-0045). It is a reverse proxy in
// front of the Guacamole webapp — the backend of the guac.gw Ingress —
// that does one thing beyond forwarding: when the browser asks for a
// Guacamole auth token without credentials, it supplies them.
//
// Why this shape. Guacamole's own client POSTs `api/tokens` on every
// route it resolves (form-encoded: the current `token` if it has one,
// plus the URL's query parameters) and shows its login form only when
// that answer is "insufficient credentials". The gateway terminates
// the identity stream and signs who the caller is into X-Mesh-Token
// per request (ADR-0032); this proxy verifies the token the way the
// bridge and jellyfinqc do — against the gateway ids pinned in git —
// and, on a token POST that carries neither `username` nor `data`,
// adds `data`: the guacamole-auth-json extension's blob naming the
// device as the Guacamole user and carrying the one RDP connection.
// The bundled extension (enabled by JSON_SECRET_KEY on the webapp)
// authenticates it; the client stores the token and no form appears.
// With a still-valid `token` in the body the webapp keeps the existing
// session and ignores the fresh `data`; with a stale one it is a new
// login — so the same hook also re-logs the device in silently after
// Guacamole's idle timeout.
//
// What it refuses, by doing nothing: a request without a valid token,
// or from a device outside the configured group, is forwarded
// untouched. The webapp then shows its login form, which — with no
// other authentication extension configured — authenticates nobody.
// Fail closed, nothing to brute-force.
//
// The blob format is the extension's (UserDataService/CryptoService):
// base64( AES-128-CBC/PKCS5, zero IV, key, HMAC-SHA256(key, json) ‖ json ).
// Each blob is single-use and expires in BlobTTL, so a captured one
// is worthless. Everything else — the WebSocket tunnel included — is
// forwarded as-is with the identity headers dropped.
package guacjson

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/meshtoken"
)

// identityHeaders are the gateway's ambient identity headers
// (config-server/gateway.Headers, named here rather than imported so
// this package stays as small as jellyfinqc): dropped on the way to
// the webapp, which must never see a name it could be tempted to
// trust.
var identityHeaders = []string{"X-Mesh-Node", "X-Mesh-Name", "X-Mesh-Groups", meshtoken.Header}

// DefaultTokensPath is the webapp's token endpoint under its default
// context (WEBAPP_CONTEXT=guacamole).
const DefaultTokensPath = "/guacamole/api/tokens"

// BlobTTL bounds a minted blob: it is consumed by the request that
// carries it, so a minute of clock skew is all it needs to survive.
const BlobTTL = 2 * time.Minute

// maxForm bounds the token POST body the proxy is willing to parse.
const maxForm = 1 << 16

// Connection is the one connection the device gets: a protocol and
// its parameters, under the name the connection list shows.
type Connection struct {
	Name       string
	Protocol   string
	Parameters map[string]string
}

// userData is the extension's UserData, the JSON inside the blob.
type userData struct {
	Username    string                    `json:"username"`
	Expires     int64                     `json:"expires"` // ms since epoch
	SingleUse   bool                      `json:"singleUse"`
	Connections map[string]userConnection `json:"connections"`
}

type userConnection struct {
	Protocol   string            `json:"protocol"`
	Parameters map[string]string `json:"parameters"`
}

// Minter turns a device name into a blob for the extension.
type Minter struct {
	key  []byte
	conn Connection
}

// NewMinter takes the 16-byte AES key the webapp holds as
// json-secret-key and the connection to grant.
func NewMinter(key []byte, conn Connection) (*Minter, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("guacjson: key is %d bytes, want 16", len(key))
	}
	if conn.Name == "" || conn.Protocol == "" {
		return nil, errors.New("guacjson: connection needs a name and a protocol")
	}
	return &Minter{key: key, conn: conn}, nil
}

// Mint encodes a single-use blob naming username, valid until
// now+BlobTTL.
func (m *Minter) Mint(username string, now time.Time) (string, error) {
	ud := userData{
		Username:  username,
		Expires:   now.Add(BlobTTL).UnixMilli(),
		SingleUse: true,
		Connections: map[string]userConnection{
			m.conn.Name: {Protocol: m.conn.Protocol, Parameters: m.conn.Parameters},
		},
	}
	js, err := json.Marshal(ud)
	if err != nil {
		return "", err
	}
	return Encode(m.key, js)
}

// Encode is the extension's wire format for an arbitrary JSON
// document: sign, prepend the signature, encrypt the whole, base64.
func Encode(key, js []byte) (string, error) {
	mac := hmac.New(sha256.New, key)
	mac.Write(js)
	plain := append(mac.Sum(nil), js...)
	// PKCS#5 padding to the AES block.
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decode reverses Encode and verifies the signature. The webapp is the
// real consumer; this exists so the proxy's output is testable
// byte-for-byte against the format.
func Decode(key []byte, blob string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, errors.New("guacjson: ciphertext is not whole blocks")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(plain, raw)
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(plain) {
		return nil, errors.New("guacjson: bad padding")
	}
	plain = plain[:len(plain)-pad]
	if len(plain) <= sha256.Size {
		return nil, errors.New("guacjson: too short for signature and data")
	}
	sig, js := plain[:sha256.Size], plain[sha256.Size:]
	mac := hmac.New(sha256.New, key)
	mac.Write(js)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("guacjson: signature mismatch")
	}
	return js, nil
}

// Handler is the proxy. Zero value is not usable; use New.
type Handler struct {
	proxy      *httputil.ReverseProxy
	verifier   *meshtoken.Verifier
	minter     *Minter
	group      string
	tokensPath string
	now        func() time.Time
}

// New builds the proxy to upstream (the webapp's listener). verifier
// pins the gateway keys; group is the member-cert group whose devices
// are logged in; tokensPath is the webapp's token endpoint ("" for
// DefaultTokensPath).
func New(upstream *url.URL, verifier *meshtoken.Verifier, minter *Minter, group, tokensPath string) *Handler {
	if upstream == nil || verifier == nil || minter == nil || group == "" {
		panic("guacjson.New: upstream, verifier, minter and group are required")
	}
	if tokensPath == "" {
		tokensPath = DefaultTokensPath
	}
	h := &Handler{verifier: verifier, minter: minter, group: group, tokensPath: tokensPath, now: time.Now}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.Host = pr.In.Host // nginx's view, kept for the webapp's redirects
			for _, name := range identityHeaders {
				pr.Out.Header.Del(name)
			}
		},
		FlushInterval: -1, // the tunnel is a stream; never buffer
	}
	return h
}

// ServeHTTP adds credentials to a credential-less token POST from a
// verified device; everything else is a plain forward.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == h.tokensPath {
		h.login(r)
	}
	h.proxy.ServeHTTP(w, r)
}

// login rewrites r's body in place when the device qualifies. Any
// reason not to is logged and the request goes on as it came.
func (h *Handler) login(r *http.Request) {
	ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if strings.TrimSpace(ct) != "application/x-www-form-urlencoded" {
		return // not the client's own POST; leave it alone
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxForm+1))
	r.Body.Close()
	if err != nil || len(body) > maxForm {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	// Whatever happens below, the upstream must see the original body
	// unless we deliberately replace it.
	r.Body = io.NopCloser(bytes.NewReader(body))

	form, err := url.ParseQuery(string(body))
	if err != nil {
		return
	}
	if form.Has("username") || form.Has("data") {
		return // the caller brought credentials of its own
	}
	tok := r.Header.Get(meshtoken.Header)
	if tok == "" {
		log.Printf("guacjson: token request from %s without a gateway token; left to the login form", r.RemoteAddr)
		return
	}
	c, err := h.verifier.Verify(tok, r.Host, h.now())
	if err != nil {
		log.Printf("guacjson: token request for %s: gateway token refused: %v", r.Host, err)
		return
	}
	if !c.HasGroup(h.group) {
		log.Printf("guacjson: token request by %s (%s) with %v, not %s; left to the login form", c.Subject, c.Name, c.Groups, h.group)
		return
	}
	blob, err := h.minter.Mint(c.Name, h.now())
	if err != nil {
		log.Printf("guacjson: mint for %s (%s): %v", c.Subject, c.Name, err)
		return
	}
	form.Set("data", blob)
	nb := []byte(form.Encode())
	r.Body = io.NopCloser(bytes.NewReader(nb))
	r.ContentLength = int64(len(nb))
	r.Header.Set("Content-Length", fmt.Sprint(len(nb)))
	log.Printf("guacjson: device %s (%s) logged in as guacamole user %q", c.Subject, c.Name, c.Name)
}
