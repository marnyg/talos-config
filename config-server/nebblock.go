package main

// Mesh cert revocation (decision closing thread uuid dc04e3e8): a
// git-managed blocklist of certificate fingerprints, compiled into
// pki.blocklist of every config this server composes — the hub's own,
// every node's, and every enrolled device's. Nebula refuses handshakes
// from a blocklisted cert regardless of its validity window.
//
// Git stays the single source of truth (invariant 2): revoking is a
// commit, and enforcement is the already-routine propagation — redeploy
// the hub, re-apply the nodes. No CRL endpoint, no runtime state. The
// lag that propagation implies is bounded by what a rogue cert can
// reach at all: a media cert sees one NodePort, and the 90-day device
// validity is the backstop.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// nebBlocklistFile lives beside machines/ under the talos tree, so the
// hub image and any checkout agree on where revocations are declared.
const nebBlocklistFile = "mesh-blocklist.txt"

// loadMeshBlocklist reads the blocklist: one lowercase hex SHA-256
// fingerprint per line, '#' comments and blank lines ignored. A missing
// file is an empty list (nothing revoked yet); a malformed entry is an
// error, because silently skipping a typoed fingerprint would leave a
// revoked cert accepted — the failure mode the file exists to prevent.
func loadMeshBlocklist(root string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, nebBlocklistFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", nebBlocklistFile, err)
	}
	var out []string
	for i, line := range strings.Split(string(raw), "\n") {
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		fp := strings.ToLower(strings.TrimSpace(line))
		if fp == "" {
			continue
		}
		if len(fp) != 64 || strings.IndexFunc(fp, func(r rune) bool {
			return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
		}) >= 0 {
			return nil, fmt.Errorf("%s:%d: %q is not a hex sha256 cert fingerprint", nebBlocklistFile, i+1, fp)
		}
		out = append(out, fp)
	}
	return out, nil
}
