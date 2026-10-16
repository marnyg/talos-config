//go:build iroh && linux && !android

package main

import (
	"log"
	"strings"

	"github.com/marnyg/talos-config/config-server/fakeip"
)

// dnsNote: on linux fakeip.Setup declared the zone on the link in
// systemd-resolved when resolvectl was on PATH; otherwise the tun
// forwards by IP only and the operator wires the zone.
func dnsNote(ifname string) {
	zone := strings.TrimSuffix(fakeip.Zone, ".")
	if fakeip.HasResolved() {
		log.Printf("split DNS: %s → %s on %s (systemd-resolved, per-link)", zone, fakeip.ResolverIP, ifname)
		return
	}
	log.Printf("split DNS: no resolvectl — point %s at %s yourself (names will not resolve until then; IPs work)", zone, fakeip.ResolverIP)
}

// trustNote says how the login user trusts the device root once. On
// NixOS the declarative form is security.pki.certificateFiles pointing
// at the path; elsewhere p11-kit's `trust anchor`.
func trustNote(rootPath string, logger *log.Logger) {
	logger.Printf("https: device root at %s — trust it once: NixOS security.pki.certificateFiles = [ %q ], or `sudo trust anchor %s`", rootPath, rootPath, rootPath)
}
