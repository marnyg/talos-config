//go:build iroh && darwin

package main

import "log"

// dnsNote: on darwin the zone is static — nix-darwin writes
// /etc/resolver/mesh.internal → 198.18.0.2 — so there is nothing to
// do at startup, only to say where it lives.
func dnsNote(string) {
	log.Printf("split DNS: /etc/resolver/mesh.internal (static, nix-darwin)")
}
