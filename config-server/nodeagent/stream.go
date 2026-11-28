package nodeagent

import (
	"context"
	"encoding/binary"
	"errors"
	"io"

	"github.com/marnyg/sap/protocol/cert"
)

// StreamHandler terminates one admitted forward stream in-process
// (Options.Serve): the caller's identity is what Authorize attributed
// to the connection the stream arrived on. The handler owns stream
// and closes it. Untagged so a facet's server (config-server/gateway)
// builds and tests without iroh.
type StreamHandler func(ctx context.Context, stream io.ReadWriteCloser, id cert.Identity, peer cert.ActorID)

// A facet that takes any port (ADR-0050, AnyPort) carries no port in
// its name, its ALPN or its grant: the caller names one at the head of
// each stream — two bytes, big-endian, before the first payload byte —
// and the receiver splices the rest to <host>:<port>. Per stream, not
// per connection: one admitted connection to (member, facet) carries
// flows to many ports, and Authorize has already run for all of them.
// Zero is not a port.

// WritePort writes the port header that opens a stream on an any-port
// facet.
func WritePort(w io.Writer, port uint16) error {
	if port == 0 {
		return errors.New("nodeagent: port 0 names no service")
	}
	_, err := w.Write(binary.BigEndian.AppendUint16(nil, port))
	return err
}

// ReadPort reads the port header WritePort wrote.
func ReadPort(r io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	port := binary.BigEndian.Uint16(b[:])
	if port == 0 {
		return 0, errors.New("nodeagent: port 0 names no service")
	}
	return port, nil
}
