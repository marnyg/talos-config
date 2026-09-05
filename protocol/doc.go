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
//	docs/      this scope's desired-state (goals, invariants, domain
//	           model) and the protocol sketch
//
// Separate Go module from config-server on purpose: the protocol must
// have no dependency on the hub, Talos or nebula. config-server imports
// it with a replace directive when Mesh v3 Phase 1 wires it in.
package protocol
