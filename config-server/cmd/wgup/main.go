// Command wgup is the admin entrypoint to the WireGuard control
// channel — `tailscale up`, wallet edition. On first run it enrolls
// this device against the config server: the server issues a
// single-use challenge, the admin signs it with an allowlisted wallet
// (EIP-191 personal_sign — an ordinary auth message, NOT the fleet
// master message), and the server returns the derived wg-quick config,
// which is cached locally (0600) and brought up with wg-quick.
// Subsequent runs skip straight to wg-quick.
//
//	wgup                       # enroll if needed, then wg-quick up
//	wgup -down                 # tear the tunnel down
//	wgup -reenroll             # discard the cached config, enroll again
//	wgup -print                # enroll if needed, print config path, don't bring up
//	wgup -name phone -print    # enroll another declared device
//
// Offline fallback (server unreachable, or you hold the master
// signature anyway): wgping -admin <name> -sig <sig> -wgquick.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	log.SetFlags(0)
	var (
		server   = flag.String("server", "https://marnyg-talos-config.fly.dev", "config server base URL")
		name     = flag.String("name", "laptop", "device name (must be declared in the server's WG_ADMIN_PEERS)")
		down     = flag.Bool("down", false, "tear the tunnel down")
		reenroll = flag.Bool("reenroll", false, "discard the cached config and enroll again")
		print    = flag.Bool("print", false, "enroll if needed and print the config path instead of bringing the tunnel up")
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
		cfg, err := enroll(strings.TrimRight(*server, "/"), dev)
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
func enroll(server, name string) (string, error) {
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

	fmt.Fprintf(os.Stderr, `Sign this message with an allowlisted admin wallet (EIP-191 personal_sign):

--------------------------------------------------
%s
--------------------------------------------------

e.g.  cast wallet sign '%s'

signature: `, ch.Message, ch.Message)

	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return "", fmt.Errorf("no signature provided")
	}
	sig := strings.TrimSpace(sc.Text())

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
