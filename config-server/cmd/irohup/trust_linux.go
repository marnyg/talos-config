//go:build iroh && linux && !android

package main

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// trustRoot puts the device root in the system trust store through
// p11-kit (`trust anchor`), which Firefox and Chrome read on most
// distributions. NixOS has no writable store: the declarative
// security.pki.certificateFiles line (trustNote) is the way there, and
// `trust` is usually absent, which is reported as an error so the note
// prints.
func trustRoot(rootPath string) error {
	if _, err := exec.LookPath("trust"); err != nil {
		return errors.New("no `trust` (p11-kit) on PATH")
	}
	out, err := exec.Command("trust", "anchor", "--store", rootPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("trust anchor: %v: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("https: device root trusted via p11-kit (%s)", rootPath)
	return nil
}

// trustNote is the by-hand form, for when trustRoot could not.
func trustNote(rootPath string, logger *log.Logger) {
	logger.Printf("https: device root at %s — trust it: NixOS security.pki.certificateFiles = [ %q ], or `sudo trust anchor --store %s`", rootPath, rootPath, rootPath)
}
