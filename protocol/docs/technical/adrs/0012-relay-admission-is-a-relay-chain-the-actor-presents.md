# ADR-0012: Relay admission is a `relay` chain the actor presents; a newborn presents a wildcard link from its intro

- Status: Accepted 2026-11-28 (owner; spike `talos-config-qx5c`).
  Ruled with it: the reversal below, and the wildcard link as a bearer
  for the birth window (Consequences). The four "Not decided" items
  stay open. Built so far: the gate (`actors/relaygate`,
  `talos-config-bal3`) and the links in the intro and the kit
  (`protocol/spawn`, `talos-config-bpha`) — not the child's
  presentation, the parent's `-relay-id` wiring, or anything deployed. Measured: the three chain shapes fold
  in today's verifier (`TestRelayAdmissionShapes`, `protocol/cert`),
  and the gate's mechanics hold against the real relay binary
  (`TestRelayGate`, `iroh-transport`).
- Date: 2026-11-28
- Related: root ADR-0051 (the protocol network is its own public
  surface — this answers its open admission question), spike
  `talos-config-k7sd`, `talos-config-5gz` (the hub relay's member
  gate), ADR-0001 (one chain verifier; `relay` is a chain verb since
  `xwu`), ADR-0007 (lighthouse, postage), ADR-0008 (birth behind a
  per-spawn open consent — the shape reused here), ADR-0009 (leases).
- **Reverses, for the protocol's relay only,** the 2026-09-18 ruling
  on the hub relay ("relay is NOT a facet; no grant reaches the hook",
  `359.8.5`). That ruling stands for the hub: its members' grants are
  compiled by the hub, and nothing there presents one. Here the actor
  presents its own.

## Context and Problem Statement

A relay in front of sovereign actors has to decide who may hold a
connection on it. Open, it is free bandwidth for anyone. The hub's
gate ("members only") cannot serve: actors hold protocol keys, not
member certs, and that gate has been observe-only since the day it
went live for exactly that reason.

The stock relay offers one decision point: an HTTP hook called at
connect with the connecting key and nothing else. So whatever admits
an actor must already be known to the gate, by key, when the actor
connects.

The hard case is the newborn. A child mints its key on its own
compute, and the first thing it does is reach its parent — through the
relay. Nobody can vouch for a key that does not exist yet, and the
parent learns it only from the message the relay must first carry.

## Decision Drivers

- Invariant 1: no second authority mechanism beside the cert.
- Invariant 2: the relay verifies offline, rooted at itself.
- Invariant 7: no private key travels; invariant 13: the provisioner
  never learns about birth.
- Root invariant 1 ("the grant is the record"): the gate may cache
  what was shown to it; nothing may depend on state only it holds.
- Root invariant 5 as amended (ADR-0051): no call back to the hub.
- No change to the stock relay binary.

## Considered Options

- **A — open relay with a per-client rate limit.** No regression
  against today (the hub's gate only observes). Keys are free, so the
  limit bounds nothing in aggregate.
- **B — an allow-list of stable ids.** Covers the provisioner, the
  parent, a laptop. Does not cover children.
- **C — the parent vouches for the child's key** (`#admit`, the
  spike's first idea). Fails the newborn: the key is unknown until
  birth, and birth needs the relay.
- **D — the actor presents a `relay` chain; the newborn presents a
  wildcard link carried in its intro.** Below.
- **E — D, with the chain carried as the relay client's bearer
  token.** iroh 1.1.0 can send `Authorization: Bearer` on the upgrade,
  and a proxy in front of the relay can read it. Ruled out by
  measurement: the token is read once per endpoint and re-sent
  unchanged on every reconnect; replacing the relay-map entry on a
  live endpoint does not change it. Child certs live one birth window
  (15 min by default), so a credential in the token means a new
  endpoint every beat.

## Decision Outcome

**D.** The relay's gate is a receiver `R` (in the deployment: the
lighthouse's key — one founder key per network). Admission is
`VerifyChain(R, relay, chain, …, key, "#relay", now)`, and the three
callers are three chains the verifier already folds:

| Caller | Presents | Rooted by |
|---|---|---|
| member | nothing | `R`'s own consent naming it (`-member`, delegable) |
| child | `[P→C relay]`, from its kit, renewed on the beat | `R`'s consent to `P` |
| newborn | `[P→"*" relay + postage]`, from its intro, one birth window | `R`'s consent to `P` |

The newborn's link is the birth consent's shape (ADR-0008) on the
`relay` verb: a wildcard audience is legal only with postage, and the
stamp is over `(R, the newborn's key)`, so the bearer link admits one
key per proof of work, for the birth window only. The child swaps it
for the key-bound link the moment it holds its kit.

**Presentation is out of band, over HTTPS**: the actor posts
`{id, chain, stamp}` to the gate; the gate verifies and remembers
`id → exp`. It never needs to be authenticated: showing `X`'s chain
admits `X`. Members are entered from `R`'s own consents at start and
present nothing.

**The gate has two halves, because neither sees enough alone.** The
relay's hook names the key that proved itself and admits it only while
a presented credential for it is live. A proxy in front of the relay
owns the byte stream and ends a connection when its credential does;
it learns which id a connection is for from a stable claim in the
bearer token (the id — never a credential, see E).

No new verb, caveat or verifier rule. `protocol/spawn`'s intro gains
one cert; the kit's outfit gains one chain.

### Consequences

- Good: the newborn is admitted with no key travelling, no registry,
  and nothing the provisioner can read as other than opaque bytes.
- Good: revoking a member and everything under it is `R` dropping one
  consent it holds itself — immediate, no list.
- Good: the gate's table is a cache of presented grants. Losing it
  refuses everyone until they present again; measured, a refused
  endpoint is back ~1.5 s after presenting, on the same endpoint.
- Good: the hub's member gate can enforce once no actor homes there.
- Bad / to watch: the wildcard link is a bearer for the birth window.
  Whoever reads an intro (the compute provider — "birth trust equals
  compute trust") can admit keys at one proof of work each until it
  expires. _Accepted by the owner 2026-11-28 as the same trade._
- Bad / to watch: every non-member must present on its beat. A child
  that renews but does not present is cut at expiry.
- Not decided: the claim in the bearer is unsigned in this sketch.
  An actor that is itself admitted could claim another id and have its
  connection outlive its own credential, until it next reconnects.
  Closing it is a signature by the claimed key over `R`'s id; decide
  when building.
- Bad / to watch: a refused client knocks about three times a second.
  The refusing path must stay one table lookup.
- Not decided: a stranger door at the relay itself (`R→"*"` with
  postage). Off in this design; it is one consent to add.
- Not decided: whether a child may delegate onward (`delegable` on
  `P→C`), which is how a caller from another relay would be let in to
  reach it. No such caller exists yet.
- Not decided: a per-client byte limit (the relay has one built in).

### Confirmation

- `TestRelayAdmissionShapes`: member, child, newborn admitted; a
  stranger, another key holding the child's chain, an expired link, a
  wildcard link without postage, a stamp for another key, and a link
  under a non-delegable cert are refused; dropping `R`'s consent
  refuses the child.
- `TestRelayGate`, real `iroh-relay` 1.1.0 behind a Go reverse proxy:
  the hook refuses a key claiming a presented id; a refused endpoint
  comes online by itself after presentation; the proxy cuts at expiry
  and the client returns and is refused until renewed; a renewal in
  time keeps one uninterrupted connection. Loopback, not through fly's
  proxy.
- `TestGateInFrontOfRelay` (`actors/relaygate`, tagged): the built
  gate with real certs, the real relay and real endpoints — member,
  stranger, child, a key claiming the child's id, newborn on a stamped
  wildcard link, expiry and renewal.
- Found while building: the gate's clock floor (invariant 9) is its
  own consents' `iat`, not the last presentation — the verifier roots
  caller links only under an `invoke` consent, so `relay` (and
  `publish`) links do not feed the mark (thread `talos-config-w0ur`).
- Wrong if: admission needs anything the actor did not present — a
  lookup at the hub, a list in git, a second kind of credential.
