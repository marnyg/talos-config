# ADR-0001: One N-link chain verifier; self-authenticating envelopes; stream vs actor facets

- Status: Accepted _(owner ruling 2026-09-12, same day as the
  grill-design on `talos-config-0bc.2`; the N-link `authorize.qnt` and
  the Go port are `0bc.2.1`/`0bc.2.2`)_
- Date: 2026-09-12
- Revises: root ADR-0017 (the fixed 2-link `[consent, grant]` chain
  becomes the connection-level special case of an N-link verifier; the
  caveat vocabulary gains `endpoints` and `postage` — first version
  bump); root ADR-0018 (`speak-as` now also resolves the **audience**
  side: `cav.verbs ∋ invoke` lets a hot key present its principal's
  grants)
- Amends: glossary **Facet** (root + protocol), new glossary terms
  Envelope, Invocation, Reply, Location record, Renewal beat mechanics
- Related: `../../day-to-day/exploration-log.md` §M2 (the ruled-out
  alternatives), `docs/mesh-v3-iroh.md` §ALPN ↔ facet mapping,
  `protocol/cert/authorize.go` (`Authorize`, orphaned `Attenuate`)

## Context and Problem Statement

M1 (`protocol/cert`) shipped `Authorize` for the talos mesh's
connection-level check: inputs are an ALPN, the QUIC peer key, a
`Bundle{Member, Grants, SpeakAs}` and the receiver's consents; it
builds exactly one chain shape, `[consent(R→w), grant]`, and matches
`aud` raw against the peer. `Attenuate` exists, is tested, and nothing
calls it.

The protocol sketch's messaging model differs in three ways: it
verifies **per envelope**, not per connection; proof chains are
**N-link** and attenuated link by link (`C → C2 → …`); and there is no
member cert — the `frontdoor` facet is `aud: "*"` and a receiver
verifies a chain to an actor it has never heard of. Its facets are
fine-grained (`#renew`, `#publish`, `#frontdoor`), while
`docs/mesh-v3-iroh.md` rules out fine-grained ALPNs as a metadata leak
and the glossary said "on the wire a facet is an ALPN class". And the
closed verb set contains `reach-me-at`, but `Caveats` has no field
that could carry an endpoint.

M2 has to build the envelope and the actor runtime on top of M1
without growing a second authority mechanism (invariant 1).

## Decision Drivers

- Invariant 1 (one primitive) and invariant 2 (offline,
  receiver-rooted): a second verifier with slightly different chain
  semantics is how a second authority mechanism sneaks in.
- Goals: "defined at the signed-envelope layer; anything that moves
  signed bytes qualifies" — the protocol must not depend on an
  authenticating transport.
- The cold/hot split (ADR-0018) must let a root rotate its hot key
  without every correspondent re-issuing grants.
- The Go verifier is pinned 1:1 to `verification/quint/authorize.qnt`;
  whatever changes must change in the model first.
- The talos deployment must keep working as a plain VPN in front of
  services that know nothing of actors (Jellyfin, `apid`).

## Considered Options

1. **Separate `envelope.Verify` beside `cert.Authorize`.** Fastest to
   ship M2; leaves `Attenuate` orphaned, duplicates speak-as
   resolution, and the Quint model covers only one of the two.
2. **One N-link chain verifier; `Authorize` becomes its special
   case.** Requires extending `authorize.qnt` first.
3. **One ALPN per actor facet.** Single facet derivation, but reopens
   the metadata leak mesh-v3 closed and makes every facet a new
   ClientHello string.
4. **Facet derivation split by kind, verifier derivation-blind.**
   Stream facet = ALPN at connect; actor facet = `to.facet` inside the
   encrypted envelope on one fixed ALPN.
5. **Grants always issued to the hot key** (no aud-side speak-as).
   Simplest verifier; every correspondent of a root re-issues on each
   rotation.
6. **Sig-less envelopes riding iroh's peer authentication.** One
   signature cheaper per message; correctness then depends on the
   transport, and the in-memory test transport must fake peer auth.
7. **Self-authenticating envelope, transport peer as hint only.**

## Decision Outcome

Options **2, 4, 7**, plus aud-side speak-as (the negation of 5).

- **One chain verifier**, `VerifyChain(receiver, consents, chain,
  speakAs, signer, facet, now) → (eff, verified, err)`, folds
  `cert.Attenuate` over N links and enforces the sketch's four rules
  (`signer` is the presenting key — the envelope's `from` or the QUIC
  peer; `facet` is caller-derived, see below; a `group:` audience
  returns the sentinel `ErrGroupAud` and the talos layer resolves it
  as today): first link signed by the receiver (the
  receiver prepends its own consents; the caller carries only the
  links it holds); last link's `aud` binds to the presenting signer;
  nothing expired under the effective clock; no unknown caveat.
  Connection-level `Authorize` = `AcceptTable[ALPN]` → facet, that
  verifier on `[consent, grant]`, then the talos-only layer (member
  identity, `group:` audiences, blocklist, `Peer` binding).
- **Audience binding**: last-link `aud` ∈ {signer key; `"*"` — fail
  closed unless a `postage` caveat is present; a principal `S` for
  which the proof carries a `speak-as S→signer` with `cav.verbs ∋
  invoke`}. `group:` audiences stay connection-level in M2 (no member
  cert in the envelope path, no actor use case yet).
- **Facets**: the verifier takes `facet` and never sees how the caller
  derived it. Actor facet is the primary form; stream facet is the
  compatibility mode (an actor standing in front of a non-actor
  service).
- **Envelope** `{from, to:{target,facet}, seq, payload, proof[], loc?,
  sig}` and **Reply** `{re, from, payload, loc?, sig}` are
  self-authenticating (JCS + scheme-selected signature as for certs;
  `payload` opaque). Signer ≠ transport peer is allowed, which makes
  the per-(sender, receiver) `seq` high-water mark load-bearing rather
  than defence in depth. One invocation = one bidirectional stream;
  at most one reply; replies carry no proof chain (the open stream is
  the invitation; invariant 2 holds because only the requester could
  have opened it).
- **Caveat vocabulary v2**: `endpoints []string` (transport-tagged
  opaque strings, the object of `reach-me-at`; attenuates by
  intersection) and `postage` (opaque requirement string; presence is
  **monotone** — a link may add it, none may remove it; two links that
  both set it must agree, else reject).
- **Ordering**: `authorize.qnt` gains the N-link chain, aud-side
  speak-as, `"*"`+postage and the two caveats **before** the Go port;
  the rapid laws follow the model.

### Consequences

- Good: one verifier, one model, `Attenuate` earns its keep; the
  talos VPN path is unchanged in behaviour and becomes a documented
  special case; hot-key rotation stays local to the root.
- Bad: M2 acquires a Quint prerequisite; every envelope costs one
  signature verification the transport had already paid for; the
  `seq` table is per-correspondent state (open problem 8 — keyed for
  known correspondents only, strangers are M3's postage problem).
- Open (numbers, open problem 9): chain-length hard cap, `max_bytes`,
  mailbox depth, renewal fraction of the shortest held lifetime.
