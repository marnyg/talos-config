// Package irohtransport adapts an iroh Endpoint (github.com/marnyg/
// talos-config/iroh-go) to the sovereign-actor protocol's actor.Endpoint
// (ADR-0001 §Transport & Facets; task talos-config-0bc.2.6).
//
// Dependency direction: this module imports protocol/actor, protocol/cert
// and iroh-go; protocol/ imports neither iroh-go nor this module (the
// protocol is transport-independent). Actor code that wants iroh does
//
//	ep, _ := irohtransport.Bind(priv, irohtransport.Options{})
//	a := actor.New(cert.NewEdSigner(priv), ep)
//
// # Identity
//
// An iroh EndpointId IS a 32-byte Ed25519 public key, and an "ed:<hex>"
// cert.ActorID IS the hex of that same key, so the mapping is a byte
// identity in both directions (nodeid.go). The iroh endpoint is bound
// with the actor's own Ed25519 seed, so the TLS-authenticated peer id
// iroh reports on Accept is the caller's actor id with no extra proof.
// Only ed: actors can be reached over iroh; eth: ids are unreachable
// here (an eth principal speaks through an ed hot key, ADR-0001).
//
// # One ALPN
//
// All actor traffic uses one ALPN, ALPN ("sovereign-actor/v1"). The
// facet is inside the encrypted envelope, not in the ClientHello.
//
// # Framing
//
// Ruling §6: one invocation = one QUIC bidirectional stream; the request
// is the whole send side until FIN, the reply the whole return side
// until FIN. SendMsg is WriteAll+Finish, RecvMsg is ReadToEnd. Streams
// of one dial→peer pair share a QUIC connection (pooled per peer on the
// dial side; the accept side loops AcceptBi per connection).
//
// # Endpoint tags
//
// Endpoints() returns "iroh:udp=<ip:port>" for every dialable bound or
// discovered socket and "iroh:relay=<url>" for the home relay, if any.
// Dial reads the same tags from the hints and ignores every other tag.
// With no iroh tag among the hints it falls back to iroh's own remote
// cache (a peer it has talked to before) and otherwise fails with
// actor.ErrUnreachable — PresetMinimal has no DNS/pkarr discovery by
// design; piggybacked location records are the discovery layer.
package irohtransport
