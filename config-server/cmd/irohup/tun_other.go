//go:build iroh && !darwin

package main

import (
	"context"
	"errors"
	"log"

	"github.com/marnyg/talos-config/config-server/meshtun"
	"github.com/marnyg/talos-config/config-server/nodeagent"
)

// The desktop presentation is macOS first (359.9.6). Linux gets it when
// a linux desktop needs it — fakeip itself is portable; what is missing
// is the privileged setup (ip tuntap / ip addr / ip route) and a
// systemd unit instead of launchd.
type tunSetup struct{}

func privilegedSetup(string, string) (*tunSetup, error) {
	return nil, errors.New("-tun: desktop presentation is not implemented on this OS yet (macOS first)")
}

func serveTun(context.Context, *tunSetup, *nodeagent.Agent, *meshtun.Pool, string, *log.Logger) error {
	return errors.New("unreachable")
}
