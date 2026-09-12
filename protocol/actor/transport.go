package actor

import (
	"context"
	"errors"

	"github.com/marnyg/talos-config/protocol/cert"
)

// Transport is what the actor runtime needs from the wire: open a
// bidirectional stream to an actor, or accept one. It knows nothing
// about envelopes, chains, or facets — it moves opaque byte strings
// between authenticated peers.
//
// Stream framing (ADR-0001, ruling §6): one invocation = one
// bidirectional stream. The request is the WHOLE send side until FIN;
// the reply is the WHOLE return side until FIN. No length prefixes, no
// multiplexing inside a stream. A Stream therefore carries at most one
// message per direction.
type Transport interface {
	// Dial opens a stream to actor id. hints are transport-tagged
	// endpoint strings (e.g. "mem:b", "iroh:...") taken from the
	// caller's location cache; a transport ignores tags it does not
	// own and may fall back to its own discovery when none apply.
	Dial(ctx context.Context, id cert.ActorID, hints []string) (Stream, error)
	// Accept blocks until a peer opens a stream and returns it together
	// with the peer's transport-authenticated actor id. The peer id is
	// informational for the runtime: the envelope's signer is the
	// authority, and signer ≠ peer is allowed (ADR-0001).
	Accept(ctx context.Context) (Stream, cert.ActorID, error)
}

// Stream is one bidirectional, single-message-per-direction channel.
type Stream interface {
	// RecvMsg reads the peer's whole send side until FIN. A second call
	// after a message was delivered, or a FIN with no data, returns
	// io.EOF.
	RecvMsg(ctx context.Context) ([]byte, error)
	// SendMsg writes msg as the whole send side and FINs it. A second
	// call returns ErrStreamFinished.
	SendMsg(ctx context.Context, msg []byte) error
	// Close releases the stream. If the send side was never finished
	// it is FINed empty, so the peer's RecvMsg observes io.EOF.
	Close() error
}

// Endpoint is a Transport bound to a local identity that can also say
// how it is reachable. The runtime uses Endpoints() to mint the actor's
// reach-me-at record; the tags are opaque to the protocol.
type Endpoint interface {
	Transport
	// ID is the local actor id this endpoint authenticates as.
	ID() cert.ActorID
	// Endpoints returns transport-tagged strings peers can Dial with.
	Endpoints() []string
	// Close unbinds the endpoint; pending Accepts return ErrClosed.
	Close() error
}

var (
	// ErrClosed marks use of a closed stream or endpoint.
	ErrClosed = errors.New("actor: transport closed")
	// ErrStreamFinished marks a second SendMsg on a stream (one message
	// per direction, ruling §6).
	ErrStreamFinished = errors.New("actor: stream send side already finished")
	// ErrUnreachable marks a Dial that found no endpoint for the actor.
	ErrUnreachable = errors.New("actor: no reachable endpoint for actor")
	// ErrPeerMismatch marks a hint that resolved to an endpoint bound to
	// a different actor than the one being dialled.
	ErrPeerMismatch = errors.New("actor: endpoint is bound to another actor")
)
