# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-22 — **M3 built: lighthouse as a plain actor, postage stamps
the envelope** (ADR-0007, Proposed; `0bc.3`).

- `envelope`: optional `Postage` field — canonical key `"postage"`
  present only when non-empty (unstamped traffic is byte-identical to
  M2), inside the signature; `PostagePreimage` = SHA-256 of the form
  with `sig` and `postage` blanked. `TestPostageWireLaw` pins it.
- `postage/` (new): `pow:<bits>` vocabulary, `Scheme{Solve, Check}`
  seam (PoW default; money later). Unknown scheme fails closed both
  ways.
- `actor`: `Verbs[facet]` (absent ⇒ invoke) replaces the hard-bound
  `invokeChain`; inbox checks `eff.cav.postage` after the fold →
  `Status postage`; `Send` stamps when the held chain names a
  requirement and drops a receiver-signed first link (a frontdoor cert
  is held as-is); `Frontdoor(req, ttl)` mints the `aud "*"` consent;
  `CheckLocation` exported.
- `lighthouse/` (new, ~150 LOC + client): `New(a)` registers `#publish`
  (verb publish, `{loc?, frontdoor?}`) and `#lookup`; volatile
  directory of *published* records; `Publish`/`Lookup` helpers,
  `Lookup` re-validates and caches.
- Domain model: Lighthouse / Frontdoor / Postage / Envelope entries
  say what is built; ADR index gained 0006 (was missing) and 0007.
- config-server and iroh-transport build unchanged.

## Previous sessions

2026-09-20 — `Actor.Serves` → reach-me-at `cav.facet` (ADR-0005 addition,
recorded in talos ADR-0026).

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

- ADR-0007 is **Proposed** — owner to accept. Numbers unchosen: PoW
  bits, frontdoor tail, directory size.
- Postage is checked *after* the chain fold in the mailbox loop; an
  unstamped flood still costs one own-sig verify each. Mailbox depth is
  the limiter. Moving it earlier needs the requirement before the fold.
- An actor whose own `reach-me-at` expires keeps piggybacking it and is
  refused `bad-loc` everywhere until it re-publishes (pre-existing;
  surfaced by `TestRecordsExpire`). Worth a guard in `Send`.
- No spent-token set: a stamped envelope's "single-use" is the seq
  high-water mark. Open problem 8 stands.
- The Quint models do not cover postage checking or `Verbs` (the verb
  was already a parameter of `authorize.qnt`).
- Carried: ADR-0001's ≈ 1 h `reach-me-at` text; held `speak-as` out of
  `Result.Verified` (ADR-0003); `DefaultMailbox = 64`.

## Suggested next steps

- Accept ADR-0007; close `0bc.3` (user confirms).
- M4 `0bc.4` (spawn-as-k8s-Job) is now unblocked on the protocol side.
- Talos consumer: replace the Phase-1 "lighthouse as a view" (root
  ADR-0024) with `protocol/lighthouse` when a second network exists;
  not needed for N=1.
