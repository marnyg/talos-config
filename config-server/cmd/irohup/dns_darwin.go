//go:build iroh && darwin

package main

import "log"

// dnsNote: on darwin the zone is static — nix-darwin writes
// /etc/resolver/mesh.internal → 198.18.0.2 — so there is nothing to
// do at startup, only to say where it lives.
func dnsNote(string) {
	log.Printf("split DNS: /etc/resolver/mesh.internal (static, nix-darwin)")
}

// trustNote says how the login user trusts the device root once: the
// System keychain, so Safari and Chrome (which reads it with name
// constraints enforced) both see it. Firefox follows the keychain via
// security.enterprise_roots.enabled.
func trustNote(rootPath string, logger *log.Logger) {
	logger.Printf("https: device root at %s — trust it once with:\n  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s", rootPath, rootPath)
}
