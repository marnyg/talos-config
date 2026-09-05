# Goals — sovereign-actor protocol

<!-- The protocol's OWN higher-order outcomes, independent of any
     deployment (talos, fly, nebula, Kubernetes). The talos
     consumer's goals live in ../../../docs/desired-state/goals.md. -->

The narrative source is
[`../sovereign-actor-protocol.md`](../sovereign-actor-protocol.md).
This file is the checkable goal set.

## What the protocol is

A network of **sovereign actors**: an actor whose identity is a
keypair, reachable by identity rather than by machine address, holding
and spending its own money natively, able to spawn children onto rented
compute by paying for them. Three existing things compose — actor
systems (Erlang/OTP), cryptographic keypairs as self-certifying
identity+wallet, and permissionless compute markets. Hierarchies
(companies, services, families of agents) are patterns actors build
*voluntarily*, never structures the protocol imposes.

## Current goals

- **One primitive.** Almost everything is a single **delegation
  certificate** `{iss, aud, can, cav, iat, exp, sig}`. It is used five
  ways — key rotation, network membership, messaging authority,
  location advertisement, economic offers. If you understand the cert,
  you understand the system. A second authority mechanism beside the
  cert would refute the protocol before it is built.

- **Offline, receiver-rooted authorization.** Checking a chain's
  authority is pure signature verification — no registry, no chain
  RPC, no phone-home. Every actor is the root of authority over
  itself: a message is a proof that the receiver (transitively)
  invited it. The proof chain *is* the registry, carried by the
  caller.

- **Revocation is expiry, everywhere.** No revocation lists. Every
  credential is re-signed on a per-relationship **renewal beat** (days,
  not months), so a stolen credential has a bounded lifetime. Taken
  knowingly for keys, capabilities, and location records alike.

- **Sovereign identity with a survivable compromise story.** A
  long-lived root is a **cold key** that never touches the wire;
  day-to-day operation uses **hot keys** carrying short-lived
  `speak-as` delegations. Spawned actors mint disposable keys at birth
  on their own compute — the private key never travels. Hot-key and
  leaf compromise are routine recoveries; root capture is the one
  unrecoverable event, and the protocol says so rather than pretending
  otherwise.

- **Open-world reachability without an open inbox.** Anyone can message
  anyone (a default, revocable **frontdoor** facet), but unsolicited
  delivery costs the sender more than it costs the receiver
  (postage — PoW now, micropayments as the goal). Sovereignty lives at
  admission time: tighten caveats or go dark.

- **Deployment-independent transport and settlement.** The protocol is
  defined at the signed-envelope layer; anything that moves signed
  bytes qualifies (QUIC, plain UDP). The core contains **zero economic
  enforcement** — money vocabulary exists only inside opaque caveats,
  so any settlement rail can sit behind it. Chain interactions live
  strictly on the money path and never block messaging, key rotation,
  or rendezvous.

- **Spawning as funded enrollment.** A parent spawns a child by paying
  for a lease, injecting a one-time intro nonce; the child mints its
  identity and exchanges the nonce for real certs. Supervision trees
  become funding trees; "let it crash" becomes "let the lease lapse".
  A child grows up by accumulating its own renewable edges, so its
  parent's death costs it one edge, not its existence.

## v0 scope

- **No per-message money.** Stranger contact is receiver-enforced PoW
  postage; invited children pay nothing. Per-relationship or slower
  flows settle with plain transfers; payment channels/tickets wait for
  a real per-message flow.
- **Not built in v0:** DHT, TEE remote attestation, in-protocol chain
  code, store-and-forward mailboxes. Each has a stated upgrade path
  that rides the existing envelope without changing the protocol's
  shape.
- The honest **open problems** (postage economics, provider
  impersonation at birth, interior-node compromise, detection,
  metadata privacy, market liveness, per-correspondent state scaling,
  unchosen parameters, intermittent-to-intermittent delivery) are part
  of the design's claims — see the sketch's closing section.

## Non-goals

- No certificate authority at the root of the system. Where CA-like
  things appear (network founders minting publish-caps), they are
  subordinate to identities, never above them.
- No global clock, no global namespace, no imposed hierarchy.
- No in-protocol remedy for root-key capture — a remedy would require
  an authority above the identity, the one thing the design refuses.
