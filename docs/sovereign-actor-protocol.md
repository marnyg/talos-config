# Sovereign Actors — a design sketch

*Status: design exploration, 2026-08-16. Nothing here is built. Shared
for critique. Version 4 — thrice revised after adversarial
cold-reader reviews; open problems are collected honestly at the end
and are part of the document's claims, not an appendix.*

## Motivation

Three existing things compose into something new:

1. **Actor systems** (Erlang/OTP): tiny processes with private state,
   communicating only by message, supervised in trees, cheap to spawn
   and to let crash.
2. **Cryptographic keypairs as identity**: an Ethereum-style keypair
   is self-certifying — you can prove who you are to anyone, offline,
   with no registrar. It is also, automatically, a wallet.
3. **Permissionless compute markets** (Akash, Golem, …): a wallet can
   rent a machine anywhere, paid per lease, no account signup.

Put together: an **actor whose PID is a keypair**. It is reachable by
identity rather than by machine address, it holds and spends money
natively, and it can *spawn children onto rented compute* by paying
for them. Supervision trees become funding trees. "Let it crash"
becomes "let the lease lapse."

The long-term picture is a flat, permissionless network of such
actors, where hierarchies (companies, services, families of agents)
are *patterns actors build voluntarily* — not structures the protocol
imposes. There is no certificate authority at the root of the system.
Where CA-like things appear, they are subordinate to identities,
never above them.

## The problem

A conventional VPN-mesh CA (e.g. Nebula's) quietly does five jobs:
binds names to keys, assigns addresses, grants group membership,
gates who may join, and expires/revokes all of the above. Remove the
CA and every one of those jobs needs a new answer. Add "actors hold
money" and "actors run on strangers' machines," and three more
problems appear: theft from compromised actors, spam against public
identities, and trusting a provider to run the code you paid for.

Concretely, the design must answer:

- **Identity**: what survives a key compromise? Self-certifying keys
  famously can't rotate — lose the key, lose the identity.
- **Authorization**: who may put a message in an actor's inbox? A
  public identity with an open inbox is a free DoS target.
- **Reachability**: how do you deliver to an *identity* when the
  machine under it changes address, or is re-rented weekly?
- **Spawning**: how does a parent learn its child's identity, when
  the child is born on untrusted hardware and mints its own key?
- **Economics**: what bounds the damage when an actor holding funds
  is compromised — without a central authority that could freeze it?

## Vocabulary and conventions

- **Identity** — the *address* of a keypair: a short hash of the
  public key (Ethereum-style). `iss` and `aud` fields always hold
  addresses; public keys travel alongside signatures when needed.
- **Facet** — a named entry point an actor exposes, written
  `P#report`. Purely a dispatch convention (like a method name); no
  isolation implied. Several capabilities may target the same facet
  with different caveats.
- **Renewal beat** — the short recurring cycle (days, not months) on
  which credentials are re-signed. It is **per-relationship**: every
  edge (parent↔child, member↔lighthouse, correspondent↔correspondent)
  renews with its own counterparty. There is no global clock tick.
- **Clocks** — expiry checks need *loosely* synchronized clocks
  (minutes of skew against windows of hours-to-days; the tightest
  pair — 1-hour location records — sets the real tolerance budget).
  A verifier always uses its **own** clock; a hostile host can lie to
  its *tenant* about time, but cannot make third parties accept the
  tenant's expired certs.
- **Machine addresses at the edges** — identity-addressing is the
  steady state. Bootstrap artifacts (the intro handed to a newborn,
  lighthouse endpoints in a network bundle) necessarily contain
  literal `ip:port`. The design rule: raw endpoints appear only in
  short-lived bootstrap artifacts, never in durable state.
- **Transport** — deliberately unspecified (QUIC and plain UDP are
  the candidates). The protocol is defined at the envelope layer;
  anything that moves signed bytes qualifies.

## One primitive

Almost everything below is a single data structure — a **delegation
certificate**:

```json
{
  "iss": "<granting identity>",
  "aud": "<receiving identity>",
  "can": "<verb, e.g. invoke:P#report, speak-as, reach-me-at>",
  "cav": { "<caveats: rate limits, spending caps, postage, ...>" },
  "exp": "<expiry>",
  "sig": "<iss's signature over all of the above>"
}
```

Certs chain: a link may re-delegate what it received, adding caveats,
never removing them. Effective authority is the intersection of the
chain. The caveat `delegable: false` means *no link may follow the
link that carries it* — issuers forbid re-delegation where it
matters, e.g. network membership caps. Caveats are a small,
versioned vocabulary; a verifier that meets a caveat it does not
understand rejects the proof (fail closed — an unknown restriction
might be the one that matters).

Checking a chain's *authority* is pure signature verification —
offline, no registry, no chain RPC, no phone-home. One honest
qualification: **stateless verification covers authority, not every
caveat**. Per-message caveats (`max_bytes`) are stateless; caveats
over time or aggregates (rate limits, spending totals) require small
volatile counters at the verifier, or observation of a public ledger.
Volatile means resettable: a crash-induced restart reopens rate
windows until certs expire. The system is registry-free, not
counter-free, and says so.

This one structure is used five ways: key rotation, network
membership, messaging authority, location advertisement, and economic
offers. If you understand the cert, you understand the system.

The system in one sentence: **identity names an actor; every actor is
the root of authority over itself; a message is a proof that the
receiver (transitively) invited it.**

## Identity

**Two classes of identity, different lifecycles:**

**Long-lived roots** (a human, an organization, a durable service):
the identity is the address of a **cold root key** that never touches
the wire. Day-to-day operation uses **hot keys** carrying short-lived
delegation certs from the root (`can: speak-as`, expiry ~30 days).

*Why this and not a stable identifier with rotating keys underneath
(the Urbit/Azimuth shape)?* Because that indirection needs someone
to serve it — a registry, a chain, or a CA: an authority above the
identity, which is the one thing this design refuses at the root.
The cold/hot split is the same indirection built offline: the cold
key *is* your personal registry, and the delegation cert is its
lookup response. The price is that the registry itself (the root
key) is unrecoverable — examined below.

- Hot key compromised → root signs a delegation for a new hot key;
  the stolen one dies at expiry. Recovery is routine.
- Root compromised → not death but **capture**: root keys never
  expire, so the thief *is* the identity — able to mint valid
  delegations indefinitely, indistinguishable to counterparties.
  This is the worst event in the system and the protocol offers no
  remedy, deliberately: a remedy would require an authority above
  the identity, which is the thing being refused. The two real
  defenses are chosen *before* compromise: (a) keep the root
  genuinely cold (hardware, offline, used a few times a year);
  (b) make the root a smart-contract account with multisig/social
  recovery — outside the protocol, invisible to it. After capture,
  the only move is out-of-band: tell your counterparties, who stop
  renewing their edges to you. Relationships, which expire, are
  recoverable; the naked identity is not.

**Actor identities** (spawned workers): a single keypair, generated
on the actor's own compute at birth. No delegation machinery at the
leaves. Compromise or loss → the parent kills the lease and spawns a
replacement, which mints a *new* identity; the parent updates its
records (its private table: child identity → lease handle → granted
caps → funding state). Actors are cheap; identity death at the
leaves is normal operation, and the funding tree is the recovery
mechanism.

**Revocation, everywhere in this design, is expiry.** No revocation
lists. Everything important is re-signed on the renewal beat, so a
stolen credential has a bounded lifetime; between expiries,
revocation is best-effort. This trade is taken knowingly, three
times over (keys, capabilities, location records) — and it is why
root capture above is categorically worse: roots are the one thing
that never expires.

## Messaging: capabilities, not open inboxes

The unit of communication is **invoking a capability**, not "sending
to an address." A message envelope carries:

```json
{
  "to": "P#report",            // target identity + facet
  "payload": { ... },
  "proof": [ <cert chain> ],   // authority to invoke
  "seq": 42,                   // replay protection, see below
  "sig": "<sender's signature over the envelope>"
}
```

The receiver P verifies, offline and **in cost order** — envelope
signature first (one cheap check), then the proof chain, whose
length is hard-capped so garbage chains cost an attacker more to
build than the verifier to reject: (1) the envelope signature is
valid; (2) the proof chain's **first link is signed by P itself** —
all authority over P originates at P; (3) the last link's `aud`
equals the envelope's signer — stealing a cert without the key is
useless; (4) nothing is expired, caveats are satisfied. Any failure
→ dropped. If the sender is operating under a hot key, the proof
also carries its root's `speak-as` cert, so the verifier can map
signer → identity without any lookup. There is no registry of who
may call: **the proof chain is the registry**, carried by the
caller.

`seq` is a per-sender monotonic counter; the receiver keeps a
volatile high-water mark per correspondent and drops replays. Lost
on restart — reopening a replay window bounded by proof-chain expiry.
Accepted: replays are re-deliveries of authorized content, and
at-most-once delivery already obliges endpoints to tolerate
duplicates.

**Attenuated re-delegation is free and offline.** If C may
`invoke:P#report`, C can hand its own child C2 a narrower version
(say, rate-limited) by appending a link. P verifies the 2-link chain
without ever having heard of C2. No registration, no callbacks —
this is what makes deep spawn trees administratively free.

**Public reachability is a default, revocable facet.** Every actor,
by default convention, mints a well-known **frontdoor** capability
and publishes it wherever it publishes location records (lighthouse
lookups return both):

```json
{ "iss": "P", "aud": "*", "can": "invoke:P#frontdoor",
  "cav": { "max_bytes": 4096, "postage": "<fee or proof-of-work>" },
  "exp": "+24h", "sig": "P" }
```

So *anyone can message anyone* — the open-world default — but
unsolicited delivery costs the sender more than it costs the receiver
to process (and the fee check itself must be one cheap operation, or
postage becomes the DoS vector). `aud: "*"` is the one deliberate
exception to the `aud`-equals-signer check: any signer qualifies.
Stranger replay needs no `seq` state — each postage token (fee
receipt or PoW solution) is single-use and bound to the envelope
hash, so replaying a frontdoor message is just paying again. A flood
of correctly-paid messages is not an attack; it is revenue.
Sovereignty lives at admission time:
tighten caveats, or go dark by not re-minting the facet (exposure to
already-issued frontdoor certs lasts at most their 24h tail).
Postage settlement is pluggable — proof-of-work as placeholder, real
micropayments as the goal; weaknesses cataloged under Open problems.

**Delivery semantics are deliberately weak** (the Erlang lesson):
at-most-once, best-effort, sender retries, reliability is end-to-end
(the only reliable message is the reply). Mailboxes are volatile —
actor state does not survive a crash; supervisors restart from spec,
not snapshot. No store-and-forward infrastructure exists; senders
hold their own outbound queues, persisted locally if they care.
Delivery requires overlapping liveness — a real restriction, fine
for mostly-on leased compute, unserved for pairs of mostly-off
devices (see Open problems).

## Reachability

**Location is a claim, signed like everything else:**

```json
{ "iss": "P", "can": "reach-me-at",
  "endpoints": ["203.0.113.7:4242", "relay:hub.example:4242"],
  "exp": "+1h", "sig": "P" }
```

Distribution is layered:

1. **Piggybacking (primary):** every message and renewal response
   carries the sender's current location record. Parties in regular
   contact never perform lookups — **the capability graph is the
   routing table**.
2. **Lighthouses (fallback + bootstrap):** a lighthouse is an
   ordinary actor with two facets: `#publish` (gated by a membership
   capability — holding a publish-cap **is what "being in a network"
   means**; issued `delegable: false` unless the network wants
   member-invited members) and `#lookup` (per network policy:
   `aud:*` public directory, or members-only; default members-only).

A "network" is a bundle: `{lighthouse endpoints, lighthouse identity,
your publish-cap}`. Whoever mints the first publish-caps *founded*
the network — network authority is just an identity issuing certs:
the CA pattern rebuilt voluntarily, one level up, subordinate to the
identities that choose to join. Attaching to several networks =
holding several publish-caps. One identity, many networks.

Lighthouses are the one place stable machine addresses matter: they
sit on stable endpoints and replicate (several actors honoring the
same publish-caps). A moved lighthouse propagates like any other
location record over renewal traffic; a fully cold client holding
only a stale bundle is re-bootstrapped out of band. No magic under
the bootstrap: *somebody* hands you your first working endpoint.

Actors behind NAT list `relay:` endpoints — relays are actors selling
a `#relay` facet. (Hard-won prior lesson: remote nodes on cellular or
corporate networks sit behind symmetric NAT that nothing can
hole-punch; design relay-by-default, treat direct paths as an
optimization.) Relays forward envelopes blindly; they can observe
traffic patterns — see Open problems on metadata.

An actor landing on new compute publishes fresh location records to
its networks and its parent as its first act.

## Spawning

Parent P spawns child C on a compute market:

```
spawn(image@sha256:…, payment, intro)
  where intro = { parent: P, endpoints: [...], nonce: one-time }
returns: promise of { id, location, lease_handle }
```

1. P signs a lease with a provider, paying from its own funds, and
   injects `intro` as deployment parameters. The image is referenced
   **by content digest, never by tag** — "the code I funded" has an
   unambiguous, re-spawnable referent. The intro's endpoints are raw
   addresses: the sanctioned bootstrap exception.
2. C boots, generates its keypair — **the private key is born on C's
   compute and never travels; only public keys cross the wire** —
   and sends its birth message to the intro endpoints:
   `{ child_pubkey, sign_C(nonce) }`.
3. P matches the nonce to an outstanding spawn (a small table of
   pending nonces — the **intro nonce is the only bearer token in
   the system**, single-use, exchanged immediately for real certs).
4. P replies with C's **starter kit**: capabilities to invoke P
   (`#report`, `#renew`, …) and initial funding (next section).

`spawn` returns a *promise* — the child mints its own identity, so
the parent learns `id` (the child's address) and `location` (the
child's first signed location record) only when the birth message
arrives. If it never arrives (provider failure, parent outage during
the birth window), C retries until its first lease period lapses
unrenewed — the funding tree garbage-collects failed births. The
resolved `lease_handle` is deliberately distinct from `id`: it is
the provider's own lease object — how you kill the actor — while
`id` is how you talk to it. Killing therefore relies on the provider
honoring its own termination API: the same trust bucket as birth
fidelity, with the same fallback (stop paying) and the same eventual
upgrade (attested runtimes). A parent that loses the lease handle
can only starve a child, not stop it.

**The `#renew` loop:** child-initiated before its certs expire; P's
response carries fresh certs, P's current location record, and — at
P's discretion — the next tranche. A missed beat is natural death:
certs expire, funding stops, the lease lapses. The same loop shape
applies to every renewable edge in the system (lighthouse
memberships, correspondent capabilities), each with its own
counterparty.

**Trust axiom (v0, stated honestly):** the child's identity is
exactly as trustworthy as the purchased compute. Image signing proves
*which* code was requested; nothing yet proves the provider *ran*
it. Two consequences, both sharper than "runs modified code":

- The provider sees the intro nonce in deployment parameters, so
  **a malicious provider can boot its own code and complete the
  birth handshake as a perfectly convincing fake child.**
- The damage model of a fake or subverted child is **not just
  theft**: it sees every message sent to it and can return
  plausible-but-wrong results. Money exposure is bounded by
  tranches; *epistemic* exposure is bounded only by what you send
  down the tree. v0 rule: no secrets to, and no unverified trust in
  computation from, children on low-trust providers.

Accepted for v0 with open eyes; clean upgrade path: TEE remote
attestation (hardware-measured boot, quote binding *measurement →
image digest*) rides along in the birth message without changing the
protocol's shape.

## Economics

**The protocol contains zero economic enforcement.** No chain, token,
or contract type appears in the core — money vocabulary exists only
inside caveats, opaque to the messaging layer. Any settlement rail
(any chain, channels, even off-chain IOUs between trusting parties)
can sit behind the same caveat vocabulary; different relationships
may use different rails. This is deliberate: actors are sovereign,
and trust structures should *emerge from repeated interaction*, not
be imposed.

The default pattern is **bounded exposure through parsimony**:

- Every actor holds its own funds under its own key — a child is
  not a sub-account of its parent.
- Parents fund children in **small tranches on the renewal beat**.
- Ledgers are public: the parent **watches** the child's address. A
  spending caveat like `max: 1 USDC/day` is a stated expectation
  whose violation is detectable; the response is the repeated-game
  move — no renewal, no next tranche. This bound presumes an
  *observable* rail: relationships settling over opaque rails
  (off-chain IOUs) forfeit ledger-watching and lean on repeated-game
  trust alone — a legitimate per-relationship choice, priced in.
- Blast radius of a compromised actor = outstanding tranche ×
  detection lag — an **economic** bound, not a cryptographic one.
  Detection today: ledger watching plus renewal-beat anomalies.
  Richer behavioral attestation is an open area.

**Hard bounds exist — as negotiated contracts, not protocol.**
Sovereignty means the ability to decline and exit, not the absence of
binding agreements. An offer is just a capability with terms in its
caveats, so actors negotiate bilaterally, e.g.:

- **Gas sponsorship** (paymaster-style): the parent covers a
  newborn's transaction fees — solving the "has tokens, can't move
  them" bootstrap — under stated terms, decline-able any time.
- **Guardian roles on child-owned accounts**: the *child* owns a
  smart-contract account and *grants* the parent a bounded role
  (spending co-policy, revocation guardian) as a condition of
  funding — cryptographically hard bounds, entered voluntarily,
  removable when the child has income and leverage. (The inverse —
  parent-owned account, child as session key — was rejected: it
  makes children puppets that cannot earn, save, or survive their
  parent.)
- **Payment channels** between frequent correspondents: one
  negotiation, then gas-free micropayments — the grown-up settlement
  for postage between regulars.

Chain interactions live strictly on the money path: a congested or
unavailable chain delays funding and settlement but never blocks
messaging, key rotation, or rendezvous, which are chain-free by
construction. Note the asymmetry with actor state: mailboxes are
volatile, but *negotiated on-rail state* (channels, guardian roles)
outlives any crash — which is why such contracts should carry
timeout/recovery clauses, since the successor of a dead actor is a
**new identity** that cannot simply resume its predecessor's
contracts.

**Growing up — how a child outlives its parent.** All of a newborn's
authority chains from parent-issued certs, so a fresh orphan dies in
one beat — by design. Independence is acquired, not granted: a
maturing actor accumulates *its own* renewable edges — its own
lighthouse memberships, its own correspondents issuing caps directly
to it, its own income through its facets. Each edge renews with its
own counterparty, so the parent's death costs an independent actor
one edge, not its existence. The maturation arc is thus literal: a
newborn starts sponsored and guarded (an employee with a company
card), diversifies its relationships, and graduates to unguarded
independence.

## What each mechanism buys

| Problem | Mechanism | Residual risk, accepted |
|---|---|---|
| Key compromise (roots, hot key) | short-lived hot-key delegations | stolen key valid until expiry |
| Key compromise (roots, cold key) | cold storage; optional contract-account recovery (extra-protocol) | **identity capture** — no in-protocol remedy |
| Key compromise (actors) | disposable identity; respawn + parent's records | window until parent notices |
| Inbox spam / DoS | ocap proof chains + postage-caveated default frontdoor | transport-level floods; 24h frontdoor tail |
| Confused deputy / over-broad authority | attenuation, intersection semantics, `delegable: false` | misuse within granted scope |
| Replay | per-sender `seq` high-water marks (volatile) | window reopens on restart, bounded by cert expiry |
| Revocation | short expiry + per-relationship renewal | stolen credential lives until expiry |
| Finding moved actors | signed location records: piggyback + lighthouse actors | lighthouse liveness for first contact; cold clients re-bootstrap |
| Child authenticity at birth | one-time intro nonce → immediate cert exchange | provider can fully impersonate the child (until TEE) |
| Which code runs | image by digest; lease handle ≠ id | provider fidelity; epistemic exposure (same TEE path) |
| Theft from compromised actors | tranche funding + ledger observability; negotiated guardians | outstanding tranche × detection lag |
| Stranger micropayments (postage) | pluggable: PoW now, probabilistic payments later | PoW economics are weak (below) |

## Open problems and known weaknesses

A skeptical reader should start here; these are claims too.

1. **Postage-as-PoW has bad spam economics.** Botnets pay nothing
   for cycles; honest senders pay real cost. PoW deters casual
   floods only. The real requirement — micropayments a stranger can
   attach without a chain transaction per message (probabilistic
   "lottery ticket" schemes are the leading candidate) — is not
   designed yet.
2. **Provider impersonation at birth** is the largest v0 trust hole,
   and its damage model includes wrong answers and observed data,
   not just theft. Rule until TEE attestation: low-trust providers
   get low-trust workloads.
3. **Parent compromise is worse than child compromise and is not
   yet modeled.** A captured parent holds lease handles, funding
   flow, and cert issuance for its whole subtree. Recursion helps
   (most parents are someone's child; grandparents notice anomalies
   on their own beat) and guardian arrangements can cap a parent's
   spend too — but a real threat model for interior nodes is open.
4. **Detection is under-specified.** "Tranche × detection lag" is
   only as strong as detection: currently ledger watching plus
   renewal anomalies. Behavioral attestation between parent and
   child is an open design area.
5. **Metadata privacy is deferred, not solved.** Providers see their
   tenants' traffic; relays are ideally placed for traffic analysis;
   long-lived identities make correlation easy.
6. **Lighthouse misbehavior** (stale or withheld records, selective
   lookup answers) is mitigated only by replication and record
   signatures; member auditing is sketched, not designed.
7. **Market assumptions are load-bearing.** Compute, relay, and
   introduction markets are assumed liquid and adversarially robust.
   At bootstrap they will instead be small, altruistic, or run by
   the founding parties — which quietly recreates infrastructure
   until the markets are real.
8. **Per-correspondent state scales linearly.** Seq marks, caveat
   counters, pending nonces: negligible for actor families,
   unexamined for a popular public frontdoor.
9. **Parameters are unchosen.** Delegation windows, renewal beat,
   tranche sizes, clock-skew tolerances: every quantitative security
   claim inherits its strength from numbers that do not exist yet.
10. **Intermittent-to-intermittent delivery is unserved by design** —
    it needs store-and-forward, i.e. someone holding state for you,
    which this design refuses to mandate. If the need materializes,
    mailbox service becomes another negotiated, paid actor role —
    never a protocol feature.

## Prior art, and the deltas

| System | What it shares | Where this differs |
|---|---|---|
| Erlang/OTP | actor semantics, weak delivery, supervision | PIDs are keypairs: global, portable, economic |
| Urbit/Ames | identity-addressed networking, rotatable keys | no global on-chain PKI required; flat, not hierarchical namespace |
| E / Spritely / CapTP | object capabilities | cert-*chain* caps (offline-verifiable, UCAN/SPKI-shaped), not live session refs — actors are intermittently connected sovereigns |
| UCAN / SPKI | delegation-cert authority | applied as the *single* primitive for keys, membership, messaging, location, and money offers |
| Nebula (mesh VPN) | lighthouse rendezvous, cert-gated membership | the CA is dissolved into per-actor authority; lighthouses demoted to ordinary actors |
| AO (Arweave) | actors + inboxes + tokens + spawn | no chain in the message path; inboxes volatile by design |
| Akash / Golem | wallet-purchased compute | used as the spawn substrate, not the identity or message layer |
