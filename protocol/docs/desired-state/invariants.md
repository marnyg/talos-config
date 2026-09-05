# Invariants — sovereign-actor protocol

<!-- Properties that must always hold for the protocol. Short,
     declarative, falsifiable. Deployment-independent: no talos, fly,
     nebula, or Kubernetes here. If a change would violate one, that
     change is wrong OR the invariant is wrong — never silently both.
     Distilled from ../sovereign-actor-protocol.md. -->

1. **One primitive.** All authority is the delegation certificate
   `{iss, aud, can, cav, iat, exp, sig}`. Keys, membership, messaging
   authority, location records, and economic offers are all this one
   shape. No second authority mechanism may appear beside it.

2. **Authority verification is offline and receiver-rooted.** Checking
   a chain's *authority* is pure signature verification — no registry,
   no chain RPC, no phone-home. A valid chain's **first link is signed
   by the receiver itself**; all authority over an actor originates at
   that actor. A chain that does not begin with a receiver-signed cert
   is unverifiable, not denied. (Honest scope: this covers authority,
   not every caveat — see invariant 8.)

3. **No authority above an identity.** Every actor is the root of
   authority over itself. Where CA-like patterns emerge (a network
   founder issuing membership caps), they are subordinate to the
   identities that choose to join, never above them. There is no
   registrar, no root CA, no global PKI.

4. **`can` is a closed, versioned verb set; the verb never carries its
   object.** Verbs are drawn from `{member, invoke, speak-as,
   reach-me-at, relay, publish}`; target and facet live in structured
   caveats (`cav.target`, `cav.facet`) so that chain attenuation is
   field-wise intersection, not string parsing.

5. **Attenuation only.** A chain link may add caveats, never remove
   them; effective authority is the intersection over the chain. A
   verifier that meets a caveat it does not understand **rejects**
   (fail closed). `delegable: false` forbids any link following the one
   that carries it.

6. **Revocation is expiry.** No revocation lists. Every credential is
   re-signed on a per-relationship renewal beat; a stolen credential is
   valid only until it expires. There is no global clock tick — each
   edge renews with its own counterparty.

7. **Private keys are born where they live and never travel.** Only
   public keys and signatures cross the wire. A spawned actor generates
   its keypair on its own compute at birth. Identity death at the
   leaves is normal operation; the funding tree is the recovery
   mechanism. Long-lived roots keep a cold key off the wire and operate
   through hot keys bearing short-lived `speak-as` delegations.

8. **Stateless verification enforces "may", never "how much".** Per-
   message caveats (`max_bytes`) are stateless; caveats over time or
   aggregates (rate limits, spending totals, replay `seq`) require
   small **volatile** counters at the verifier, or observation of a
   public ledger. Volatile means resettable: a restart reopens those
   windows until certs expire. The system is registry-free, not
   counter-free.

9. **Time is a trust input; resurrection is bounded by starvation.**
   Every guarantee is `exp > now`, and `now` is a local, unauthenticated
   clock. Roll-forward only *denies* (availability; an ops problem).
   Rollback — the security direction — is bounded by a monotone
   **low-water mark** over issuer-signed `iat`: a verifier keeps
   `lw = max(lw, iat)` over every cert whose signature it verifies, and
   judges with `now = max(local, lw)`. `iat` never participates in
   authority or attenuation. There is no time authority — a time oracle
   would be an authority above the receiver (violates 3). The mark is a
   safe-to-lose cache; loss degrades to the local clock, never to a
   stronger grant.

10. **Zero economic enforcement in the core.** No chain, token, or
    contract type appears in the messaging layer. Money vocabulary
    exists only inside opaque caveats. Any settlement rail may sit
    behind that vocabulary; different relationships may use different
    rails. A congested or unavailable chain delays funding and
    settlement but never blocks messaging, key rotation, or rendezvous,
    which are chain-free by construction.

11. **Raw machine addresses appear only in short-lived bootstrap
    artifacts**, never in durable state. Identity-addressing is the
    steady state; the capability graph is the routing table. Bootstrap
    exceptions (the intro handed to a newborn, lighthouse endpoints in
    a network bundle) are the sanctioned places for literal `ip:port`.

12. **Delivery is deliberately weak.** At-most-once, best-effort,
    sender retries; reliability is end-to-end (the only reliable
    message is the reply). Mailboxes are volatile — actor state does
    not survive a crash; supervisors restart from spec, not snapshot.
    No store-and-forward is mandated by the protocol.

## Structural trade-offs (consequences of 1–9, stated so they are not "fixed")

These are the price of stateless, self-rooted, offline-verifiable
authority. A change that removes one has almost certainly reintroduced
a registry, an online check, or an authority above the identity —
escalate rather than "fix".

- **Revocation latency ≥ renewal beat.** Since revocation is expiry and
  verifiers are offline, a stolen credential stays valid for its
  remaining lifetime; between expiries revocation is best-effort.
- **Root capture has no in-protocol remedy.** Root keys never expire,
  so a thief who takes one *is* the identity. The only defenses are
  pre-compromise (cold storage; optional extra-protocol contract-account
  recovery) and out-of-band exit (counterparties stop renewing).
- **Birth trust equals compute trust.** Image signing proves *which*
  code was requested; nothing in v0 proves the provider *ran* it. A
  malicious provider can impersonate a newborn. Bounded by tranches
  (money) but only by discipline (epistemic) until TEE attestation.
- **Postage is detection-and-response, not prevention.** PoW deters
  casual floods only; real per-message micropayments are undesigned.
  Economics built on the protocol are bounded exposure through
  parsimony (tranche × detection lag), not cryptographic prevention.
