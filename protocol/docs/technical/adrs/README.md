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
  an absent set caveat is ∅. _Proposed 2026-09-13 (rulings `xwu`, `0lo`)._
- [0003](0003-receiver-answers-for-speak-as-principals.md) —
  a receiver answers for a principal it holds a live `speak-as` from
  (rule 4: `Target ∋ R or ∋ P`); grants to hot-key-served facets name
  the root; the receiver-held set is a type (`cert.Receiver`).
  _Accepted 2026-09-18 (`kau`)._

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
