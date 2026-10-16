//go:build iroh && darwin

package main

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

const systemKeychain = "/Library/Keychains/System.keychain"

// trustRoot puts the device root in the System keychain as a trusted
// root (admin trust settings), where Safari and Chrome read it — Chrome
// with the name constraint enforced, Safari too (trustd). Idempotent:
// a root that already verifies is left alone. Needs root, which
// privilegedSetup has — but under launchd that is not enough: writing
// admin trust settings takes an interactive authorization, and with
// no session the call fails "authorization was denied since no user
// interaction was possible" (seen 2026-10-16). So this succeeds for
// an interactive `sudo irohup -tun` and otherwise falls back to
// trustNote; the daemon serves https either way, trusted or not.
// Firefox reads the keychain at startup: restart it after trusting.
func trustRoot(rootPath string) error {
	if out, err := exec.Command("security", "verify-cert", "-c", rootPath, "-L", "-p", "basic").CombinedOutput(); err == nil {
		log.Printf("https: device root already trusted (%s)", rootPath)
		return nil
	} else if !strings.Contains(string(out), "CSSMERR_TP_NOT_TRUSTED") && !strings.Contains(string(out), "failed") {
		log.Printf("https: verify-cert: %s", strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", systemKeychain, rootPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("security add-trusted-cert: %v: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("https: device root trusted in %s (%s)", systemKeychain, rootPath)
	return nil
}

// trustNote is the by-hand form, for when trustRoot could not.
func trustNote(rootPath string, logger *log.Logger) {
	logger.Printf("https: device root at %s — trust it with:\n  sudo security add-trusted-cert -d -r trustRoot -k %s %s", rootPath, systemKeychain, rootPath)
}
