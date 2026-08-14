// Package walletsign is the client half of the hub's wallet-signed
// enrollment flow: fetch a single-use challenge, get it signed by an
// allowlisted wallet, redeem it for the device's config.
//
// The signature is always over an ordinary auth message — never the
// fleet master message, which is signed only at /status or offline with
// `cast wallet sign`. The hub side of the exchange lives in
// nebenroll.go; this package knows only the shape, and keeps the part
// that is fiddly and security-relevant in one place: rendering a
// signing page no other local process can reach, and never letting a
// signature touch a file or an argv.
//
// Under ADR-0012 the caller has already generated an X25519 keypair
// locally; only the pubkey travels here, and the private key never
// leaves the caller's disk.
package walletsign

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// nonceTTL matches the hub's session nonce lifetime. A signature
// produced after it expires is refused, so waiting longer than this only
// wastes the user's time.
const nonceTTL = 5 * time.Minute

// Challenge is what /mesh/enroll/challenge returns: the canonical
// message the wallet signs, plus the single-use nonce that binds it
// and the fingerprint the hub computed from the submitted pubkey so
// the client can echo it in the signing UI.
type Challenge struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Nonce       string `json:"nonce"`
	Fingerprint string `json:"fingerprint"`
	Message     string `json:"message"`
}

// MeshEnroll runs the whole enrollment exchange against endpoint
// (typically "<hub>/mesh/enroll") for a device that has already
// generated its keypair locally. Returns the config body the hub
// rendered; the caller is responsible for splicing in its own private
// key before running nebula.
func MeshEnroll(endpoint, name, group, pubkeyHex, tool string, paste bool) ([]byte, error) {
	ch, err := FetchChallenge(endpoint, name, group, pubkeyHex)
	if err != nil {
		return nil, err
	}
	sig, err := Sign(ch, tool, paste)
	if err != nil {
		return nil, err
	}
	return Redeem(endpoint, ch, pubkeyHex, sig)
}

// FetchChallenge asks endpoint's /challenge subpath for a challenge
// binding (name, group, pubkey). The hub echoes back the canonical v1
// message it expects signed.
func FetchChallenge(endpoint, name, group, pubkeyHex string) (Challenge, error) {
	resp, err := http.PostForm(endpoint+"/challenge", url.Values{
		"name": {name}, "group": {group}, "pubkey": {pubkeyHex},
	})
	if err != nil {
		return Challenge{}, fmt.Errorf("fetching enrollment challenge: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Challenge{}, fmt.Errorf("enrollment challenge: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var ch Challenge
	if err := json.Unmarshal(body, &ch); err != nil {
		return Challenge{}, fmt.Errorf("parsing challenge: %w", err)
	}
	return ch, nil
}

// Sign gets ch signed: in the browser by default, from stdin when paste.
func Sign(ch Challenge, tool string, paste bool) (string, error) {
	if paste {
		return pasteSignature(ch.Message)
	}
	return browserSignature(ch.Name, ch.Message, tool)
}

// Redeem posts the signed challenge and returns the hub's response
// body — the rendered device config, minus its private key.
func Redeem(endpoint string, ch Challenge, pubkeyHex, sig string) ([]byte, error) {
	resp, err := http.PostForm(endpoint, url.Values{
		"name":      {ch.Name},
		"group":     {ch.Group},
		"pubkey":    {pubkeyHex},
		"nonce":     {ch.Nonce},
		"signature": {sig},
	})
	if err != nil {
		return nil, fmt.Errorf("submitting enrollment: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrollment rejected: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// pasteSignature prints the challenge and reads a signature from stdin
// (headless flow).
func pasteSignature(message string) (string, error) {
	fmt.Fprintf(os.Stderr, `Sign this message with an allowlisted admin wallet (EIP-191 personal_sign):

--------------------------------------------------
%s
--------------------------------------------------

e.g.  cast wallet sign '%s'

signature: `, message, message)

	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return "", fmt.Errorf("no signature provided")
	}
	if sig := strings.TrimSpace(sc.Text()); sig != "" {
		return sig, nil
	}
	return "", fmt.Errorf("no signature provided")
}

// browserSignature serves the challenge on a one-shot localhost page
// (random path, so no other local process can guess the URL), opens the
// browser, and waits for the in-page wallet to post the signature back.
func browserSignature(name, message, tool string) (string, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("local callback listener: %w", err)
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tok)

	sigCh := make(chan string, 1)
	srv := &http.Server{Handler: signHandler(name, message, tool, token, sigCh)}
	go func() { _ = srv.Serve(lis) }()
	defer srv.Close()

	pageURL := fmt.Sprintf("http://%s/%s", lis.Addr(), token)
	openBrowser(pageURL)
	fmt.Fprintf(os.Stderr, "Sign the enrollment challenge in your browser:\n\n  %s\n\n(no browser wallet? rerun with -paste)\n", pageURL)

	select {
	case sig := <-sigCh:
		return sig, nil
	case <-time.After(nonceTTL):
		return "", fmt.Errorf("timed out waiting for the browser signature (the challenge nonce expires after %s — rerun %s)", nonceTTL, tool)
	}
}

var signTemplate = template.Must(template.New("sign").Parse(`<!DOCTYPE html>
<html>
<head><title>{{.Tool}} — enroll {{.Name}}</title>
<style>
 body { font-family: monospace; max-width: 44rem; margin: 2rem auto; }
 pre { background: #f4f4f4; padding: .5rem; overflow-x: auto; }
 button { font: inherit; padding: .4rem 1rem; }
 .err { color: #c00; }
</style></head>
<body>
<h1>Enroll device “{{.Name}}”</h1>
<p>This signs a <strong>single-use enrollment challenge</strong> — an ordinary
auth message, not the master-key message.</p>
<pre>{{.Message}}</pre>
<button id="sign">Sign with wallet</button>
<p id="msg"></p>
<script>
document.getElementById('sign').addEventListener('click', async function () {
  var msg = document.getElementById('msg');
  if (!window.ethereum) { msg.textContent = 'no wallet found'; msg.className = 'err'; return; }
  try {
    var accounts = await ethereum.request({ method: 'eth_requestAccounts' });
    var sig = await ethereum.request({ method: 'personal_sign', params: [{{.Message}}, accounts[0]] });
    var resp = await fetch(window.location.pathname + '/sig', { method: 'POST', body: sig });
    if (!resp.ok) { throw new Error('callback failed: ' + resp.status); }
    document.body.innerHTML = '<h1>Signed ✓</h1><p>Return to the terminal — you can close this tab.</p>';
  } catch (e) { msg.textContent = 'signing failed: ' + (e.message || e); msg.className = 'err'; }
});
</script>
</body></html>`))

// signHandler routes the one-shot signing page: GET /<token> renders it,
// POST /<token>/sig receives the signature. Anything else (including a
// missing or wrong token) is 404.
func signHandler(name, message, tool, token string, sigCh chan<- string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+token, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = signTemplate.Execute(w, struct{ Name, Message, Tool string }{name, message, tool})
	})
	mux.HandleFunc("POST /"+token+"/sig", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		sig := strings.TrimSpace(string(body))
		if err != nil || sig == "" {
			http.Error(w, "empty signature", http.StatusBadRequest)
			return
		}
		select {
		case sigCh <- sig:
		default: // already got one; ignore repeats
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// openBrowser is best-effort; the URL is always printed as fallback.
// Package var so tests can stub it.
var openBrowser = func(url string) {
	bin := "xdg-open"
	if runtime.GOOS == "darwin" {
		bin = "open"
	}
	_ = exec.Command(bin, url).Start()
}
