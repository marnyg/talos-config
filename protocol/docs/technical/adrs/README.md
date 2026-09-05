# Protocol ADRs

Architecture decisions scoped to the **sovereign-actor protocol
itself** (not the talos deployment). Protocol ADRs start at **0001** in
this scope; there are none yet.

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
