package mobile

// The tunnel runner: nebula over the file descriptor Android's
// VpnService.Builder.establish() hands the app. Kotlin owns the fd's
// lifecycle (and the VpnService routes/DNS, from ConfigInfo); Go owns
// the nebula instance. overlay.NewFdDeviceFromConfig is upstream
// nebula's own hook for exactly this embedding — no fork, no netstack:
// unlike the hub (TUN-less by necessity, see nebstack), the app has a
// real tun and the whole point is routing other apps' packets
// (Jellyfin) through it.

import (
	"fmt"
	"io"
	"os"

	"github.com/slackhq/nebula"
	nebconfig "github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"
)

// tunnelBuildVersion is what the app reports to peers in handshakes.
const tunnelBuildVersion = "talos-config-mobile"

// Tunnel is a running nebula instance bound to a VpnService tun fd.
// gomobile binds this as a Java object; the VpnService holds it for
// the life of the session and calls Stop on revocation/teardown.
type Tunnel struct {
	control *nebula.Control
}

// NewTunnel starts nebula from a completed (key-spliced) config on the
// given tun file descriptor. The fd must already be configured by the
// VpnService builder with the values from ConfigInfo.
//
// logSink: "" logs to stderr (logcat swallows it); a path appends to
// that file instead, so the app can show a debug log screen.
func NewTunnel(cfgYAML string, tunFd int, logSink string) (*Tunnel, error) {
	var c nebconfig.C
	if err := c.LoadString(cfgYAML); err != nil {
		return nil, fmt.Errorf("loading nebula config: %w", err)
	}

	var sink io.Writer = os.Stderr
	if logSink != "" {
		f, err := os.OpenFile(logSink, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening log sink: %w", err)
		}
		sink = f
	}
	logger := logging.NewLogger(sink)
	// nebula.Main deliberately does not apply logging config itself, so
	// an embedder that wants logging.level honored has to ask (same as
	// the hub's startMeshNebula).
	if err := logging.ApplyConfig(logger, &c); err != nil {
		return nil, fmt.Errorf("applying nebula logging config: %w", err)
	}

	fd := tunFd
	control, err := nebula.Main(&c, false, tunnelBuildVersion, logger, overlay.NewFdDeviceFromConfig(&fd))
	if err != nil {
		return nil, fmt.Errorf("nebula: %w", err)
	}
	if err := control.Start(); err != nil {
		control.Stop()
		return nil, fmt.Errorf("starting nebula: %w", err)
	}
	return &Tunnel{control: control}, nil
}

// Stop tears the tunnel down. Idempotent enough for a VpnService
// onDestroy: nebula's Stop signals shutdown and waits for the goroutines.
func (t *Tunnel) Stop() {
	if t.control != nil {
		t.control.Stop()
		t.control = nil
	}
}
