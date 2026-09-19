# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-19 (second session of the day) — **`seq` is an I-JSON integer**
(ADR-0006, Proposed). The consumer's first desktop caller (`irohup`)
and a hub key rotation together surfaced a wire bug in ADR-0005's
`SeqBase`: the canonical form is RFC 8785, whose numbers are IEEE-754
doubles, so a `UnixNano`-seeded `seq` (~1.79e18) is **not exact on the
wire**. Two sends to one receiver in one process — `#renew` then
`#bundle` in a single beat, which a hub rotation forces — collapsed
onto one canonical `seq` and the second was refused as a replay.

- Worse than a lost message: the receiver's high-water mark advances on
  the first (verified) envelope, parking it at ~1.79e18, which no
  correctly-scaled sender can ever exceed. A member was locked out of
  its hub for 40 min; only a hub redeploy cleared the volatile mark.
- Fix (`envelope`): `MaxSeq = 2^53−1`, `ErrSeqRange`, enforced in
  **both** `Sign` and `Verify` — the receiver check sits before the
  high-water check, so an out-of-range `seq` cannot move a mark.
  `Actor.SeqBase` now documents µs or coarser.
- `TestSeqExactOnTheWire` pins the law; `TestSequenceValidation` no
  longer hand-signs `seq` 0/−1 (they do not sign at all now).

## Loose threads

- ADR-0006 **Accepted** 2026-09-19 (owner). The envelope contract now
  carries a range refusal; `MaxSeq` is part of the wire law.
- The Quint models do not model `seq` width. `authorize.qnt` is about
  the chain; the replay counter is Go-only. If the models ever grow a
  replay channel, the exactness law belongs there first.
- `t29` / ADR-0005 stands, but this is its first *regression*: a
  consumer-driven addition (`SeqBase`) landed with a wire hazard the
  consumer then hit in production. Cheap lesson to record: additions
  that touch the canonical form need a round-trip test, not just a
  unit test.
- Carried: ADR-0001's ≈ 1 h `reach-me-at` text; held `speak-as` out of
  `Result.Verified` (ADR-0003); `DefaultMailbox = 64`; the stream-facet
  preamble reply is a transport convention, not envelope-signed.

## Suggested next steps

- Still nothing else queued by the consumer: `irohup` dials with what
  exists. Watch for a pooled stream-facet `Conn` per (peer, facet) —
  that belongs in `iroh-transport`, not here.
- M3 `0bc.3` (lighthouse actor, `#publish`/`#lookup`, PoW postage) is
  the next protocol-side build when picked up.
