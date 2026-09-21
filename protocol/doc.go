// Package protocol is the sovereign-actor protocol: actors as keypair +
// wallet, authority as delegation certs, negotiation as signed
// proposals (decision talos-config-5w1: the protocol is this repo's
// center; talos-config is its first consumer).
//
// Layout (ADR-0020):
//
//	cert/      the one primitive {iss, aud, can, cav, iat, exp, sig},
//	           signing/verification, attenuation and authorize() — spec:
//	           docs/desired-state/domain-model.md glossary +
//	           ADR-0017/0018/0019; oracle: verification/quint/authorize.qnt
//	clock/     the verifier's low-water mark over cert iat (ADR-0019) —
//	           oracle: verification/quint/clock.qnt
//	envelope/  the messaging record: a self-authenticating Envelope (one
//	           capability invocation) and the Reply bound to it — spec:
//	           protocol/docs ADR-0001 § Envelope +
//	           docs/desired-state/domain-model.md § Messaging; no quint
//	           oracle
//	actor/     the runtime binding envelope messaging to the chain
//	           verifier: serial mailbox, facets, the #renew beat — spec:
//	           protocol/docs ADR-0001 § Decision Outcome; no quint
//	           oracle. Reference transport: actor.MemoryNetwork
//	           (in-process), for tests and examples
//	postage/   the stranger's stamp: cav.postage vocabulary (pow:<bits>)
//	           and the pluggable Scheme{Solve, Check} — spec: ADR-0007
//	lighthouse/ the rendezvous actor: #publish (verb publish) and
//	           #lookup over a volatile directory of published
//	           {reach-me-at, frontdoor} records — spec: ADR-0007
//	docs/      this scope's desired-state (goals, invariants, domain
//	           model) and the protocol sketch
//
// iroh-transport/ is the out-of-module QUIC adapter for actor.Endpoint:
// a separate Go module, so protocol/ never imports it (or iroh-go) and
// stays transport-independent.
//
// Separate Go module from config-server on purpose: the protocol must
// have no dependency on the hub, Talos or nebula. config-server imports
// it with a replace directive when Mesh v3 Phase 1 wires it in.
package protocol
