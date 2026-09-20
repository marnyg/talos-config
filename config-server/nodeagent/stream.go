package nodeagent

import (
	"context"
	"io"

	"github.com/marnyg/talos-config/protocol/cert"
)

// StreamHandler terminates one admitted forward stream in-process
// (Options.Serve): the caller's identity is what Authorize attributed
// to the connection the stream arrived on. The handler owns stream
// and closes it. Untagged so a facet's server (config-server/gateway)
// builds and tests without iroh.
type StreamHandler func(ctx context.Context, stream io.ReadWriteCloser, id cert.Identity, peer cert.ActorID)
