//go:build iroh && !darwin && !(linux && !android)

package main

import (
	"context"
	"errors"
	"log"

	"github.com/marnyg/talos-config/config-server/meshtun"
	"github.com/marnyg/talos-config/config-server/nodeagent"
)

// The desktop presentation exists on macOS (359.9.6) and linux
// (fakeip/tun_linux.go); other hosts get it when one needs it.
type tunSetup struct{}

func privilegedSetup(string, string) (*tunSetup, error) {
	return nil, errors.New("-tun: desktop presentation is not implemented on this OS (darwin and linux only)")
}

func serveTun(context.Context, *tunSetup, *nodeagent.Agent, *meshtun.Pool, *log.Logger) error {
	return errors.New("unreachable")
}
