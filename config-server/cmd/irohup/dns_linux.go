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
