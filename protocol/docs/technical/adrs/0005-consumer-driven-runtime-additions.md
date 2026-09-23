# ADR-0005: The `actor` runtime grows by consumer-driven additions that never touch the verifier

- Status: Proposed _(2026-09-19, closes the `talos-config-t29`
  thread: "draft ADR-0005 if the pattern spreads" — it has, three
  times)_
- Date: 2026-09-19
- Related: ADR-0001 (the runtime as sketched: frozen after `Listen`),
  ADR-0003 (`Receiver` — the verifier-side contract these additions
  leave alone), invariants 8, 9, 12; root ADR-0018 (the unseal
  lifecycle that asked for `Hold`), root ADR-0024 (one identity on two
  wires — `Multi`), root `talos-config-359.8.3` (the node agent —
  `SeqBase`, `Observe`, `RestoreLowWater`)

## Context and Problem Statement

ADR-0001 sketched `actor.Actor` as configured-then-frozen: set the
exported fields, call `Listen`, never touch them again. The first real
consumer (the talos hub, then its node agent) has asked for four
lifecycle affordances the sketch did not have, each landed as a small
exported method without an ADR:

| addition | asked by | why the frozen shape did not fit |
|---|---|---|
| `Hold(consents, speakAs)` (2026-09-17, `356bd2b`) | hub unseal (root ADR-0018) | a sealed Issuer `Listen`s with no authority; an unseal installs it; a re-unseal replaces it — under a running `Listen` and concurrent `Send`s |
| `Multi` (2026-09-18, `4230731`) | hubkey on iroh **and** in-process (root ADR-0024) | one identity, N transports; `Dial` routes on `ErrUnreachable` |
| `SeqBase func() int64` (2026-09-19) | node agent restarts | `seq` must be monotonic per (sender, receiver) across the **sender's** restarts too, or a rebooted node is a replay until the hub forgets |
| `Observe(verified)`, `RestoreLowWater(lw)` (2026-09-19) | node agent's stream facets | a verifier running `cert.Authorize` **outside** the inbox must still advance (and may seed) the ADR-0019 mark |
| `EditConsents(edit)` (2026-09-23) | `protocol/spawn` (ADR-0008) | per-spawn birth consents and kit chains come and go on a live actor whose owner may also `Hold`; a separate `Authority()`+`Hold()` pair lets either side clobber the other — one atomic read-modify-write on `Consents` only |
| `renewHandler` re-installs a renewed own consent (2026-09-23, `6sax`) | `protocol/spawn` (the kit's `#renew` chain is P's consent) | a consent that is also a direct relationship's leaf must stay rooted across beats: without it the holder's `[fresh]` folds as a link and fails; a no-op for links |

Each was obviously right in isolation; together they change what
`Actor` is. The question this ADR answers: is that drift, or a rule?

## Decision Drivers

- ADR-0001's verifier (`cert`, `envelope`, `clock`) is pinned to the
  Quint models; the runtime is not, and should not become a second
  thing that needs a model.
- Consumers are real now (a hub, a node); the protocol must serve
  them without the protocol package learning their lifecycles.
- Invariants 8/9/12: counters and marks are volatile or safe-to-lose;
  nothing here may introduce durable actor state or a time authority.
- No transport, deployment or key-management vocabulary may enter
  `protocol/` (ADR-0001 dependency direction).

## Considered Options

### Option A: Freeze the runtime; consumers wrap it

`Actor` stays configure-then-`Listen`. A consumer that needs a
lifecycle builds a new `Actor` per phase (unseal = new actor) or wraps
transports itself.

- Pros: the sketch's simplicity; nothing to model.
- Cons: a re-unseal would drop the mailbox, the location cache, the
  edges and the mark — all safe-to-lose but all *useful*; `Multi`
  cannot be written outside the package without re-implementing
  `Dial`'s routing; `SeqBase` is unreachable from outside.

### Option B: Additions are fine when they meet three tests (chosen)

An exported runtime addition is admissible when (1) a **real
consumer** asks for it — not a sketch, not symmetry; (2) it touches
**runtime state only** — mailbox, edges, counters, caches, marks,
authority *installation* — and never the verifier's inputs or rules
(`cert.Authorize`, `VerifyChain`, the envelope check order); (3) it
keeps the state class it touches in its invariant's class — a
counter stays volatile (8), a cache stays safe-to-lose (9), a mailbox
stays a mailbox (12) — and is **opt-in** where a default exists so
existing consumers and tests see no change.

- Pros: the runtime tracks real lifecycles; the verifier stays pinned
  to the models; each addition is a method with a paragraph, not a
  framework.
- Cons: `Actor`'s surface grows by accretion; the "frozen after
  `Listen`" line in ADR-0001 is no longer literally true and must be
  read as "the verifier inputs are installed atomically, the rest is
  runtime".

### Option C: Split `Actor` into a frozen core and a mutable shell

- Pros: makes the boundary a type.
- Cons: premature at four methods; the boundary is already visible in
  the code (everything mutable goes through `mu` or self-guards, the
  verifier is called with a snapshot). Revisit if a fifth addition
  needs the type.

## Decision Outcome

Chosen: **Option B**. The four additions stand as the pattern's
record; future ones are held to the three tests in a one-line note
on the method (which consumer, which state class, opt-in or not).
ADR-0001's "frozen after `Listen`" is read as: **the verifier's inputs
(`Consents`, `SpeakAs`) are installed atomically (`Hold`) and judged
from a snapshot; everything else the actor owns is runtime state and
may have a lifecycle method.**

### Consequences

- `Hold`, `Multi`, `SeqBase`, `Observe`, `RestoreLowWater` are
  documented as one family (glossary, this ADR); `t29` closes.
- A runtime addition that would change *what a chain proves* (a new
  verifier input, a new check) is **not** in this family — it is a
  verifier change and needs its own ADR plus a model update.
- Persisting any of the safe-to-lose caches (mark, locations, seq)
  stays the consumer's job via plain values (`LowWater()`,
  `RestoreLowWater`, `Locations`), never a file format in `protocol/`.
- If the family reaches a size where the three tests are argued per
  method, Option C is the next ADR.

### Confirmation

Right if: the next consumer (irohup, the gateway, an M3 lighthouse)
lands on the runtime with at most a method or two and **no** change
under `cert/`, `envelope/` or `clock/` for lifecycle reasons.
Invalidated if: an addition in this family is later found to have
changed an authorization outcome — that would mean the boundary in
test (2) is not real.
