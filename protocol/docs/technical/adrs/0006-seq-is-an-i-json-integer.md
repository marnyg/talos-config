# ADR-0006: `seq` is an I-JSON integer, refused out of range on both sides

- Status: Proposed
- Date: 2026-09-19

## Context and Problem Statement

The canonical form of an envelope is RFC 8785 (JCS), whose number
serialization is IEEE-754 double. `seq` is declared `int64`. Integers
above 2^53 are therefore **not round-trip exact through the canonical
form**: `1789827445163589001` canonicalizes to `…589000`, the same
bytes as its predecessor. ADR-0005's `Actor.SeqBase` told senders to
seed `seq` from their clock so a restart cannot replay; the talos node
agent seeded `time.Now().UnixNano()` (~1.79e18). Two sends to the same
receiver in one process — `#renew` then `#bundle` in a single beat,
which is precisely what a hub key rotation forces — collapsed onto one
canonical seq and the second was refused as a replay.

The failure is worse than a lost message. The receiver's high-water
mark advances on a *verified* envelope, so the first (nanosecond) seq
parks the mark at ~1.79e18. No honest sender using a correctly-scaled
clock can ever exceed that, so the sender is locked out of that
receiver until the receiver's volatile mark is lost. Observed live
2026-09-19: a member could not beat its hub for 40 minutes; only a hub
redeploy cleared it.

## Decision Drivers

- The wire is JCS; changing it is not on the table (certs and envelopes
  share the canonicalizer, and signatures are over it).
- `seq` is load-bearing, not defence in depth (domain model): the
  envelope path does not bind signer to transport peer.
- Invariant "safe-to-lose caches degrade to a weaker check, never to a
  stronger grant" — a poisoned mark is the opposite: it is a *denial*
  that only a restart clears, i.e. a self-inflicted lockout.
- Clock-seeded seq must stay stateless (no persisted counter, `zey`).

## Considered Options

### Option A: encode `seq` as a JSON string

Sidesteps the double entirely; any int64 survives.

- Pros: full int64 range; no caller-side scaling rule.
- Cons: wire break for every signed envelope in existence; strings
  compare lexicographically, inviting ordering bugs; JCS's whole point
  is that the canonical form is ordinary JSON.

### Option B: cap `seq` at 2^53−1 and refuse out of range on both sides

State the range as part of the envelope contract (I-JSON, RFC 7493
§2.2). `Sign` refuses to produce one; `Verify` refuses to accept one
*before* the high-water check, so an out-of-range seq cannot advance a
mark. Clock seeds are documented as microseconds or coarser.

- Pros: no wire change; the exactness law is enforced where it is
  violated; poisoning becomes impossible rather than merely unlikely;
  µs seeding is good until ~2255.
- Cons: a documented scaling rule callers must respect (enforced, so
  they learn immediately); one theoretical sender — >1 M sends/s to one
  receiver for a century — would exhaust the range.

### Option C: sender-side scaling only (fix `SeqBase` docs, no checks)

- Pros: smallest diff.
- Cons: leaves the receiver poisonable by any sender, including a
  buggy or hostile one; the lockout is the expensive half of the bug.

## Decision Outcome

Chosen: **Option B**. The cheap fix (C) leaves the damaging half — a
receiver's mark parked beyond every honest seq — reachable by anyone who
can get one envelope verified. The range check belongs next to the
high-water check, in cost order, before the mark moves.

### Consequences

- `envelope.MaxSeq = 2^53−1` and `envelope.ErrSeqRange` are part of the
  contract; `Sign` and `Verify` both enforce `1 ≤ seq ≤ MaxSeq`.
- `Actor.SeqBase` documents microseconds (or coarser) and points at
  `MaxSeq`; the talos node agent seeds `UnixMicro`.
- Envelopes with `seq = 0` or negative no longer sign: tests that
  hand-built them now construct and sign the wire directly.
- A member built before this fix can still poison a receiver built
  before it; both were upgraded 2026-09-19 (extension `p0agent` 0.1.2,
  hub redeploy).

### Confirmation

`TestSeqExactOnTheWire` (protocol/envelope) pins both halves: the range
refusal on sign and verify, no mark movement on an out-of-range
envelope, and exactness of `MaxSeq−1` → `MaxSeq` through
Encode/Decode/Verify. The live confirmation is a member renewing and
fetching its bundle in one beat across a hub key rotation — cp1 and
irohup both did, three times, on 2026-09-19.
