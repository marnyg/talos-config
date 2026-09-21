# ADR-0007: The lighthouse is a plain actor; postage is a stamp on the envelope

- Status: Accepted
- Date: 2026-09-22 (Proposed and Accepted the same day, owner)
- Builds: `talos-config-0bc.3` (M3). Amends: ADR-0001 (envelope
  canonical form gains an optional `postage` key; the sketch's
  `max_bytes` on the frontdoor is not adopted), glossary **Lighthouse**,
  **Frontdoor**, **Postage**, **Envelope**.

## Context and Problem Statement

M2 left two holes the sketch promises to close. (1) A chain ending in
`aud: "*"` binds when the effective cert carries `cav.postage`
(VerifyChain rule 3) — but nothing *checks* a stamp, so the open
frontdoor was free. (2) Distribution of location records was piggyback
only: an actor is dialable only through a record it, or someone who
already talked to it, handed you. The sketch's fallback — a lighthouse
with `#publish`/`#lookup` — did not exist as protocol code (the talos
hub's Phase-1 lighthouse is a *view* over the Issuer's location cache,
root ADR-0024).

Three questions had to be answered without adding a second authority
mechanism (invariant 1) or an online check (invariant 2): how a
`#publish` chain is rooted in a verb that is not `invoke`; what a
lighthouse *is*; and where a postage token travels, given that the
envelope has no field for it and `payload` is opaque to the protocol.

## Decision Drivers

- **One primitive**: a publish-cap is a cert with `can: publish`; a
  frontdoor is a cert with `aud: "*"` and `cav.postage`; a directory
  entry is a `reach-me-at` cert. No new record types.
- **Verb is the receiver's choice** (ADR-0002, `xwu`): the verifier
  takes the expected verb as input and never infers it from the
  caller's links.
- **The fee check must be one cheap operation** (sketch), or postage
  becomes the DoS vector.
- **No wire change for existing traffic**: a deployed fleet decodes
  envelopes strictly (unknown key rejects).
- **Settlement is pluggable** (goals § v0 scope): PoW now, money later,
  behind the same seam.

## Considered Options

### Where a stamp travels

- **A. Optional envelope field `postage`**, present only when
  non-empty, inside the signature, outside its own preimage.
  Pros: unstamped traffic is byte-identical to M2; the stamp cannot be
  stripped or swapped (signed); one preimage per content regardless of
  the stamp. Cons: the "all keys always present" wording of ADR-0001
  gains one exception; strict decoders older than this ADR reject a
  *stamped* envelope (they have no frontdoor, so none arrives).
- **B. Grind `seq`** until the envelope hash has the required zeros.
  No wire change at all. Rejected: complects the replay counter with
  postage (the counter must stay monotone and clock-seedable,
  ADR-0006) and forces a signature per attempt.
- **C. A postage "cert"** in the proof. Rejected: a token is not a
  delegation; it would be the second authority shape invariant 1
  forbids.
- **D. Inside `payload`.** Rejected: the protocol does not parse
  payloads (ADR-0001), and the handler is the wrong place for
  admission.

### What the lighthouse is

- **A. A plain actor** whose `#publish` binds verb `publish` and whose
  `#lookup` is an ordinary `invoke` facet; the directory is volatile
  state behind the accept table. Network policy is *which grants
  exist*, never code.
- **B. A verifier special case** ("any cert the founder signed may
  publish"). Rejected for the same reason `#renew` was (ADR-0001):
  a rule outside the fold is a second mechanism.

## Decision Outcome

**Stamp: option A. Lighthouse: option A.**

- **Envelope** gains `Postage string`: canonical key `"postage"`,
  omitted when empty. `envelope.PostagePreimage(e)` = SHA-256 of the
  canonical form with `sig` *and* `postage` blanked; a token is bound
  to exactly that content. Re-sending a stamped envelope is a replay
  the receiver's seq high-water mark catches for as long as it
  remembers the sender; a fresh key pays afresh. There is no spent-
  token set (open problem 8 stays open, stated).
- **Postage vocabulary v0** (`protocol/postage`): `pow:<bits>` —
  SHA-256(preimage ‖ nonce) with ≥ bits leading zeros, nonce as hex
  token, `0 < bits ≤ 64`. An unknown scheme **rejects** on the
  receiver and **refuses to send** on the sender (fail closed,
  invariant 5). `postage.Scheme{Solve, Check}` is the seam a money
  rail plugs into; `Check` is one hash.
- **Inbox**: after the chain folds, `eff.Cav.Postage != ""` ⇒ the
  envelope's stamp must check, else reply `Status postage` before the
  handler. A stamp on a chain without a requirement is ignored.
- **Per-facet verb**: `Actor.Verbs[facet]` (absent ⇒ `invoke`) is the
  verb the inbox hands VerifyChain for that facet. The lighthouse binds
  `#publish → publish`.
- **Frontdoor**: `Actor.Frontdoor(req, ttl)` mints the consent `{iss:
  me, aud: "*", can: invoke, cav: {target: [me], facet: [#frontdoor],
  postage: req}}`; the mint refuses an empty requirement (aud `"*"`
  without postage binds nobody). A stranger presents an **empty**
  chain — the receiver's own consent roots it (rule 1) — so the
  caller-side rule is: `Send` drops a held chain's first link when the
  receiver signed it, and reads the postage requirement from the held
  links. A frontdoor cert as a lookup returned it is therefore stored
  as the caller's "grant" unchanged.
- **Lighthouse** (`protocol/lighthouse`): `New(a)` registers
  `#publish` (payload `{loc?, frontdoor?}`; `loc` defaults to the
  envelope's piggybacked record; both must be the *signer's*, verify
  and be live) and `#lookup` (`{ids}` → `{id: {loc, frontdoor?}}`).
  The directory is keyed by the publisher's identity, volatile,
  evicted on read, newer-by-`iat` wins. `Lookup` on the client
  re-validates every record (the lighthouse can withhold, never
  forge) and caches locations. A founder is whoever the lighthouse
  consented `publish` to; a members-only vs public directory is the
  `#lookup` grant's audience.

### Consequences

- Good: M3's three facets are configuration over the M2 runtime — no
  verifier rule was added; `authorize.qnt` is untouched (the verb was
  already a parameter). Stamping is automatic in `Send`.
- Good: the talos consumer's wire is unchanged; the hub's Phase-1
  "lighthouse as a view" (root ADR-0024) can be replaced by this
  package when a second network exists.
- Cost: the postage check sits *after* the chain fold in the mailbox
  loop, not in the transport goroutine — a flood of unstamped
  envelopes still costs one own-signature verify each (an empty
  chain's fold) before it is refused. Mailbox depth is the only
  limiter. Recorded, not solved: moving it earlier needs the
  requirement before the fold.
- Fixed in passing: an actor whose own location record has expired no
  longer piggybacks it (`CurrentLocation` is nil past `exp`), so a
  missed beat degrades to "no piggyback", not to `bad-loc` everywhere.
  Re-publishing is still the beat's job.
- Open (unchosen numbers, open problem 9): PoW bits, frontdoor tail,
  directory size; `max_bytes` is not in the caveat vocabulary and the
  sketch's frontdoor example is read without it.

### Confirmation

`protocol/lighthouse` tests: publish → lookup by a peer that never met
the publisher → stamped frontdoor call succeeds → the same chain
unstamped is refused `postage` without reaching the handler → an
`invoke`-verb cap does not root `#publish` → a refused publish leaves
no record → records expire and re-publish. `TestPostageWireLaw`
(envelope) pins the wire rule: no key when unstamped, key inside the
signature when stamped, preimage independent of the stamp.
