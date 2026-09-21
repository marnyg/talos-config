# Protocol ADRs

Architecture decisions scoped to the **sovereign-actor protocol
itself** (not the talos deployment). Protocol ADRs start at **0001** in
this scope.

- [0001](0001-one-chain-verifier-self-authenticating-envelopes.md) —
  one N-link chain verifier (connection-level `Authorize` is its
  special case); self-authenticating envelopes; stream vs actor facets.
  _Accepted 2026-09-12._
- [0002](0002-chain-verb-from-root-consent-absent-set-caveats-are-empty.md) —
  a chain's verb is its root consent's verb (one fold for every verb);
  an absent set caveat is ∅. _Accepted 2026-09-17 (rulings `xwu`, `0lo`;
  built `xwu`)._
- [0003](0003-receiver-answers-for-speak-as-principals.md) —
  a receiver answers for a principal it holds a live `speak-as` from
  (rule 4: `Target ∋ R or ∋ P`); grants to hot-key-served facets name
  the root; the receiver-held set is a type (`cert.Receiver`).
  _Accepted 2026-09-18 (`kau`)._
- [0004](0004-target-wildcard.md) — `cav.target` admits the wildcard
  `"*"` (caveat vocabulary v3): honored at every receiver that
  consented to the chain's sovereign for the facet; rule 4 unchanged;
  no other set caveat gets a sentinel. _Accepted 2026-09-18/19
  (`zeb`; Proposed 2026-09-18 from the `359.8.5` grill-design)._
- [0005](0005-consumer-driven-runtime-additions.md) — the `actor`
  runtime grows by consumer-driven additions (`Hold`, `Multi`,
  `SeqBase`, `Observe`/`RestoreLowWater`) held to three tests: a real
  consumer asks, runtime state only, invariant class preserved and
  opt-in; the verifier's inputs are installed atomically and judged
  from a snapshot. _Proposed 2026-09-19 (closes `t29`)._
- [0006](0006-seq-is-an-i-json-integer.md) — `seq` is an I-JSON
  integer (`≤ 2^53−1`), refused out of range on both sides so an
  out-of-range seq can never park a receiver's mark. _Accepted
  2026-09-19 (`jsq`)._
- [0007](0007-lighthouse-as-plain-actor-postage-stamps-the-envelope.md) —
  the lighthouse is a plain actor (`#publish` binds verb `publish` via
  `Actor.Verbs`, `#lookup` is ordinary `invoke`); a stranger's postage
  is an optional signed `postage` key on the envelope, bound to a
  preimage that excludes it; `pow:<bits>` is vocabulary v0 behind a
  pluggable `postage.Scheme`. _Proposed 2026-09-22 (M3, `0bc.3`)._

The protocol's **founding** decisions are root ADRs, referenced here,
not copied:

- `../../../../docs/technical/adrs/0017-authority-as-caller-carried-delegation-certs.md`
  — authority is caller-carried delegation certs; policy compiles to
  grants.
- `../../../../docs/technical/adrs/0018-unseal-is-a-speak-as-delegation-to-an-ephemeral-hub-key.md`
  — the cold-root / hot-key `speak-as` split.
- `../../../../docs/technical/adrs/0019-time-is-a-trust-input-iat-low-water-mark-bounds-rollback.md`
  — time as a trust input; `iat` low-water mark bounds rollback.
- `../../../../docs/technical/adrs/0020-monorepo-around-the-actor-protocol.md`
  — the monorepo decision that created this scope.
