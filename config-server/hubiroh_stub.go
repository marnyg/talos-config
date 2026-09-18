//go:build !iroh

package main

import (
	"crypto/ed25519"
	"errors"

	"github.com/marnyg/talos-config/protocol/actor"
)

// irohHubTransport without the `iroh` tag: --iroh-relay is refused at
// startup rather than silently ignored. See hubiroh.go.
func irohHubTransport(_, _, _ string) hubTransport {
	return func(ed25519.PrivateKey) (actor.Endpoint, error) {
		return nil, errors.New("config-server built without iroh (go build -tags iroh, cgo + libiroh_ffi)")
	}
}
