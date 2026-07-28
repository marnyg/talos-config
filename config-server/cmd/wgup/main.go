// Command wgup is the admin entrypoint to the WireGuard control
// channel — `tailscale up`, wallet edition. On first run it enrolls
// this device against the config server: the server issues a
// single-use challenge, the admin signs it with an allowlisted wallet
// (EIP-191 personal_sign — an ordinary auth message, NOT the fleet
// master message), and the server returns the derived wg-quick config,
// which is cached locally (0600) and brought up with wg-quick.
// Subsequent runs skip straight to wg-quick.
//
// Signing defaults to the browser: wgup serves a one-shot page on
// 127.0.0.1, opens it, and the in-page wallet (e.g. MetaMask) signs
// the challenge and posts the signature back — nothing to copy.
// -paste falls back to pasting a signature (e.g. cast wallet sign)
// for headless machines.
//
//	wgup                       # enroll if needed, then wg-quick up
//	wgup -down                 # tear the tunnel down
//	wgup -reenroll             # discard the cached config, enroll again
//	wgup -print                # enroll if needed, print config path, don't bring up
//	wgup -name phone -print    # enroll another declared device
//	wgup -paste                # headless: paste the signature instead
//
// Offline fallback (server unreachable, or you hold the master
// signature anyway): wgping -admin <name> -sig <sig> -wgquick.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	var (
		server   = flag.String("server", "https://marnyg-talos-config.fly.dev", "config server base URL")
		name     = flag.String("name", "laptop", "device name (must be declared in the server's WG_ADMIN_PEERS)")
		down     = flag.Bool("down", false, "tear the tunnel down")
		reenroll = flag.Bool("reenroll", false, "discard the cached config and enroll again")
		print    = flag.Bool("print", false, "enroll if needed and print the config path instead of bringing the tunnel up")
		paste    = flag.Bool("paste", false, "paste a signature instead of signing in the browser (headless)")
	)
	flag.Parse()

	dev := strings.ToLower(strings.TrimSpace(*name))
	path, err := confPath(dev)
	if err != nil {
		log.Fatal(err)
	}

	if *down {
		if _, err := os.Stat(path); err != nil {
			log.Fatalf("no cached config at %s — nothing to tear down", path)
		}
		if err := wgQuick("down", path); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *reenroll {
		_ = os.Remove(path)
	}
	if _, err := os.Stat(path); err != nil {
		cfg, err := enroll(strings.TrimRight(*server, "/"), dev, *paste)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
			log.Fatal(err)
		}
		log.Printf("enrolled %q — config cached at %s", dev, path)
	}

	if *print {
		fmt.Println(path)
		return
	}
	if err := wgQuick("up", path); err != nil {
		log.Fatal(err)
	}
	log.Printf("tunnel up — hub at hub.talos.wg (10.99.0.1); wgup -down to disconnect")
}

// confPath is where the device's wg-quick config is cached. The file
// name doubles as the interface name (wg-quick), so it is kept short.
func confPath(name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return filepath.Join(dir, "talos-wg", "talos-"+name+".conf"), nil
}

// enroll runs the challenge → wallet signature → config exchange.
func enroll(server, name string, paste bool) (string, error) {
	resp, err := http.Get(server + "/wg/enroll?name=" + url.QueryEscape(name))
	if err != nil {
		return "", fmt.Errorf("fetching enrollment challenge: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("enrollment challenge: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var ch struct {
		Name    string `json:"name"`
		Nonce   string `json:"nonce"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &ch); err != nil {
		return "", fmt.Errorf("parsing challenge: %w", err)
	}

	var sig string
	if paste {
		sig, err = pasteSignature(ch.Message)
	} else {
		sig, err = browserSignature(ch.Name, ch.Message)
	}
	if err != nil {
		return "", err
	}

	form := url.Values{"name": {ch.Name}, "nonce": {ch.Nonce}, "signature": {sig}}
	resp, err = http.PostForm(server+"/wg/enroll", form)
	if err != nil {
		return "", fmt.Errorf("submitting enrollment: %w", err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("enrollment rejected: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

// pasteSignature prints the challenge and reads a signature from
// stdin (headless flow).
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
// (random path, so no other local process can guess the URL), opens
// the browser, and waits for the in-page wallet to post the signature
// back. The timeout matches the server's 5-minute nonce TTL.
func browserSignature(name, message string) (string, error) {
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
	srv := &http.Server{Handler: signHandler(name, message, token, sigCh)}
	go func() { _ = srv.Serve(lis) }()
	defer srv.Close()

	pageURL := fmt.Sprintf("http://%s/%s", lis.Addr(), token)
	openBrowser(pageURL)
	fmt.Fprintf(os.Stderr, "Sign the enrollment challenge in your browser:\n\n  %s\n\n(no browser wallet? rerun with -paste)\n", pageURL)

	select {
	case sig := <-sigCh:
		return sig, nil
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("timed out waiting for the browser signature (the challenge nonce expires after 5 minutes — rerun wgup)")
	}
}

var signTemplate = template.Must(template.New("sign").Parse(`<!DOCTYPE html>
<html>
<head><title>wgup — enroll {{.Name}}</title>
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

// signHandler routes the one-shot signing page: GET /<token> renders
// it, POST /<token>/sig receives the signature. Anything else
// (including a missing or wrong token) is 404.
func signHandler(name, message, token string, sigCh chan<- string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+token, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = signTemplate.Execute(w, struct{ Name, Message string }{name, message})
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

// wgQuick runs wg-quick, via sudo when not already root.
func wgQuick(verb, path string) error {
	argv := []string{"wg-quick", verb, path}
	if os.Geteuid() != 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
