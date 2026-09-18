//go:build iroh

package main

// The hub's own iroh endpoint (talos-config-e8d, ADR-0024): the hubkey
// IS the iroh EndpointId, so the Issuer's inbox is reachable by any
// member that can dial the hub's relay. cgo — libiroh_ffi.a — so it is
// behind the `iroh` build tag: the nix/fly build sets it, an everyday
// `go test ./...` in this module stays C-free (hubiroh_stub.go).

import (
	"crypto/ed25519"
	"fmt"

	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
)

// irohHubTransport binds the hubkey on iroh: homed on `home` (the relay
// child over loopback, or the public relay when there is no child) and
// advertised to peers as `advertise` — the hostname fly terminates TLS
// on. The relay forwards by EndpointId, so the two names meet at one
// server (iroh-transport TestAdvertiseRelay). No UDP is reachable on
// fly's shared address and QAD is off (ADR-0022), so this endpoint is
// relay-only by construction.
func irohHubTransport(home, advertise, bindAddr string) hubTransport {
	return func(priv ed25519.PrivateKey) (actor.Endpoint, error) {
		ep, err := irohtransport.Bind(priv, irohtransport.Options{
			BindAddr:       bindAddr,
			Relay:          home,
			AdvertiseRelay: advertise,
		})
		if err != nil {
			return nil, fmt.Errorf("iroh: %w", err)
		}
		return ep, nil
	}
}
