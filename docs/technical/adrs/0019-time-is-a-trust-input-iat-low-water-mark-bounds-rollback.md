# ADR-0019: Time is a trust input; an `iat` low-water mark bounds clock rollback

- Status: Proposed _(2026-09-06, from spike `talos-config-439`;
  promote with ADR-0017/0018 when Mesh v3 Phase 1.1 lands)_
- Date: 2026-09-06
- Revises: ADR-0017 (the cert primitive gains `iat`; `authorize()`
  consumes an *effective* clock; the verifier holds one volatile
  cache)
- Amends: invariant 2's actor-owned-state clause (safe-to-lose caches);
  `desired-state/invariants.md` §Structural trade-offs (new entry)
- Related: ADR-0018 (the `speak-as` is a root-signed time fact every
  unseal), `verification/quint/clock.qnt` (model),
  `verification/quint/runway.qnt` (starvation is the same clock)

## Context and Problem Statement

Every guarantee in ADR-0017/0018 reduces to `exp > now`. `now` at a
verifier is a local clock: Talos nodes sync plain, unauthenticated NTP
(`time.cloudflare.com`; no NTS) from an RTC; devices use carrier or
NTP time and can be set by hand. The design's revocation is *expiry*,
so a stolen member cert is inert only because time passed. The two
failure directions are not symmetric:

- **Roll-forward** ⇒ everything is expired ⇒ the mesh is down.
  Availability; heals when the clock does.
- **Rollback** ⇒ expired certs verify again. A LAN attacker with an
  old leaked cert and an NTP spoof gets in. `1gv` already establishes
  the LAN is not a trusted zone; this was the cheapest attack left on
  the design, and `authorize.qnt` modelled `now` as a correct constant.

A time *authority* — anything the verifier must ask "what time is it"
— is an authority above the receiver, which the design refuses.

## Decision Drivers

- Receiver-rooted, offline verification (ADR-0017): nothing may be
  dialled on the authorize path.
- No authority above the owner (invariant 3, protocol axiom).
- Revocation latency ≥ runway (§Structural trade-offs) — rollback must
  not remove that ceiling.
- Stateless verification enforces "may" (§Structural trade-offs) —
  any verifier state must be safe to lose.

## Considered Options

### Option A: Document it as a structural trade-off, do nothing

- Pros: free, honest.
- Cons: leaves the cheapest attack on the design open, in the zone
  (`1gv`) we already know is hostile.

### Option B: Hub-signed time (roughtime-shaped)

Verifiers fetch a signed timestamp from the hub and check certs
against it.

- Pros: a real clock.
- Cons: a time oracle is an authority above the receiver; the verifier
  is no longer offline; a sealed hub has no time to give. Rejected by
  the design's own axioms.

### Option C: Skew tolerance

Accept `exp > now − s`.

- Pros: trivial.
- Cons: bounds nothing — rollback is unbounded — and either eats runway
  or extends stolen validity by `s`. Survives only as the skew
  parameter inside D's alarms.

### Option D: Monotone low-water mark over issuer-signed `iat` (chosen)

Every cert carries `iat`. A validly signed cert from an issuer the
receiver already trusts (resolved through consent → wallet, ADR-0018)
is *proof that `iat` has passed*: the issuer would not sign a future
issue time. The verifier keeps `lw = max(lw, iat)` over every cert it
verifies and checks `exp > max(local, lw)`.

- Pros: no new authority — the time facts come from the chain the
  receiver already trusts, in the *one direction that cannot grant
  access* (a lying issuer can push `lw` forward = deny, never back =
  resurrect). Offline after the first cert. Advances by itself: every
  legitimate caller's daily-renewed `invoke` certs, every name-map
  beat, and the root-signed `speak-as` at every unseal. What rollback
  can resurrect is bounded by the verifier's own starvation — the
  clock `runway.qnt` already measures; the two problems share one
  parameter.
- Cons: `iat` joins the primitive; the verifier holds its first piece
  of state (volatile, safe to lose — degrades to "trust local clock",
  never worse than today); a trusted issuer that lies about `iat` can
  deny until the verifier restarts.

### Option D′: D with the mark's advance capped at `local + s`

Considered to stop a lying issuer pushing `lw` into the future.
**Refuted by `clock.qnt`** (`CAP=true` violates
`invLWCoversHonestSeen` and `invResurrectionBounded`): after a
rollback every *honest* fresh cert looks like it is from the future,
the cap discards exactly the evidence that would have caught the
rollback, and the stolen expired cert is accepted. The cap defends
against the weaker adversary (a trusted issuer lying — who can already
mint any cert) by disabling the defence against the stronger one (LAN
NTP). Rejected.

## Decision Outcome

Chosen: **Option D, uncapped.**

- **Two halves of time.** *Upper bound (denial)* is the local clock —
  availability, fixed by ops (NTP, RTC), never by the protocol.
  *Lower bound (resurrection)* is protocol-enforced by the mark.
- **Primitive:** `{iss, aud, can, cav, iat, exp, sig}`. `iat` is the
  issuer's clock at signing. It participates in the mark only — never
  in authority or attenuation (a child's `iat` is not constrained by
  its parent's; clocks are independent).
- **Verifier rule:** for every cert whose signature verifies **on a
  chain rooted at the receiver** (its own consents; speak-as certs for
  consented principals; member/invoke certs whose resolved issuer is a
  consented principal — never a stranger's self-signed cert, which
  would let any connecting peer push the mark; never a member's
  self-issued `reach-me-at`, which is not an authorize input anyway):
  `lw := max(lw, iat)`; then `authorize()`
  runs with `now = max(local, lw)`. Update first, then judge — a cert
  never rejects itself (`iat < exp`). _(2026-09-06, decisions `7ry`,
  `c4c`.)_ Rootedness is **signature-only provenance**: the consent (or
  speak-as) that roots the chain need not be live at `now`, and its
  `cav.verbs`/`cav.groups` are not consulted (`jo8`) — `now` never
  enters rooting. "Update first, then judge" holds *across* bundles:
  `Authorize` judges one bundle with the caller's `Now`, and its
  `Verified` feeds the mark afterwards (equivalent for a single bundle,
  `clock.qnt` mutant m8).
- **State:** `lw` is a **safe-to-lose cache**: volatile in v0, may be
  persisted opportunistically (node STATE partition, app storage) to
  cover the boot window before NTP sync; loss degrades to the local
  clock and is never a security regression. Invariant 2's actor-owned-
  state clause is amended to name this class (with `seq` high-water
  marks and the blocklist copy).
- **Alarms, not rules:** `iat > local + s` (`s` ≈ 1 h) is logged as a
  clock alarm ("callers from the future" ⇒ I am behind) and still
  advances the mark; a burst of expiry rejections for callers whose
  `iat > local` is the roll-forward signature. Ops signals only.
- **Members schedule renewal off the hub's beat time**, not their own
  clock (strict renewal + a slow device clock would cost a wallet
  act); verification still uses `max(local, lw)`.
- **Residual, accepted:** a *trusted* issuer with a hostile or badly
  wrong clock (compromised hub hot key, hub host clock far ahead) can
  push `lw` forward and deny at every verifier that saw its cert until
  those verifiers restart. Strictly weaker than what that issuer can
  already do (mint any cert within its `speak-as`); recovery is
  re-unseal with a fresh hot key + restart verifiers. Stated in
  `clock.qnt` (`invHonestCorrectClockAccepted` is for liar-free
  histories).

### Consequences

- `0bc.1` cert primitive: add `iat`; canonical JSON field order
  changes before anything is built.
- `authorize()` API (Go, `0bc.1`): takes an effective `now`; the mark
  lives in the verifier (node agent, gateway), one line of state.
- `clock.qnt` joins `check.sh` (`run` + `verify`, mutation-tested:
  cap, no-advance, local-only all die). `authorize.qnt` header notes
  `NOW` is the effective clock. `runway.qnt` unchanged: starvation is
  already its clock; the rollback exposure is the same number.
- Structural trade-off added to `invariants.md`: *time is a trust
  input; the protocol bounds resurrection by starvation, not denial.*
- Talos node agent: refuse to *accept* (not to dial) until NTP has
  synced or a persisted `lw` is restored; Talos already gates
  workloads on time sync.

### Confirmation

Right if: with a node's clock rolled back by a month on a LAN with
live traffic, a member cert that expired last week is rejected within
one legitimate connection; with correct clocks and honest issuers, no
connection is ever rejected by the mark (`clock.qnt`
`invHonestCorrectClockAccepted`); and the property survives in the Go
rapid suite with real signatures.
Wrong if a verifier ever needs to *ask* anything for the time — that is
Option B returning — or if the mark has to be made authoritative over
the local clock in the *forward* direction to get security, which
would mean rollback was not the only hole.
