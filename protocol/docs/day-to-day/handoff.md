# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-23 (second session) — **M4.2 built: `protocol/provisioner` +
the spawner's `#renew` decorator** (`2dafea6`; ADR-0009's two halves).
~330 LOC + tests. `0bc.4.2` done pending close.

- **`protocol/provisioner`** — an actor with `#spawn`/`#extend`/
  `#kill` over a lease table, rendered through `Driver{Start, Extend,
  Kill}` (`StartSpec{Lease, Image, Params, Until}` → `Handle`). Lease
  machine `pending → running → {lapsed, killed}`; pending is the
  lease *during* `Driver.Start` (the slow call runs without the table
  locked, so an owner's `Sweep` can pass); terminal leases leave the
  table. **Rulings made building it:** (1) a lease has an **owner** —
  the actor the chain bound at `#spawn` (`Invocation.From`) — and
  `#extend`/`#kill` from anyone else are refused, even a caller the
  provisioner consents to; (2) `Sweep` kills lapsed leases *through
  the driver* on every platform (it is the deadline on docker while
  the process lives, bookkeeping on k8s), a failed `Kill` keeps the
  lease for the next sweep; (3) `#extend` may shorten (the owner's
  lease); (4) image must be `name@sha256:<64 hex>` (`CheckImage`);
  (5) a driver refusal is the wire refusal and leaves the lease as it
  was. `Params` are passed to `Start` unread.
- **Spawner** — a **born table** (child → lease + the deadline the
  provisioner holds as this spawner knows it: `#spawn`'s `until` at
  birth, then each acknowledged `#extend`); `New` decorates the
  installed `#renew` handler: after it answers, the latest `exp`
  re-issued to a born caller becomes `#extend {until}` to its
  provisioner, synchronously; a refusal is `slog`-logged
  (`Spawner.Log`), never surfaced, and the next beat retries. No
  `#extend` when nothing reaches further (frozen clock). `Kill(child)`
  sends `#kill` and forgets; `Sweep` forgets children past their
  deadline. `ExtendReply{until}` added to the wire types.
- **Tests** now run the spawner against the *real* provisioner over a
  fake `Driver` whose `Start` runs the child in-process — ADR-0009's
  "fake driver" acceptance on a real `#spawn`/`#extend`/`#kill` path.
  `provisioner_test.go` pins ownership, refusals, driver failures,
  sweep retry, pending-not-swept. Race-clean; `scripts/test-iroh.sh`
  green.

## Loose threads

- **The lease table is volatile.** A provisioner restart forgets its
  leases: the spawner's next `#extend` is refused "unknown lease"
  (logged) and the child lapses at its last deadline — k8s by
  `activeDeadlineSeconds`, docker by the label sweep at the next start
  (`0bc.4.4`). Passive leases make this safe, not seamless; a
  re-adopt-by-label at start is an idea, not v0.
- `#extend` and the decorator's `Send` block the mailbox loop while
  they run (ADR-0009 accepted consequence); `DriverTimeout` = 60 s
  bounds each driver call. A slow `Start` holds the provisioner's loop
  for an image pull. A goroutine per `Start` is the obvious change if
  it hurts.
- The provisioner's consent to a customer is one root over all three
  facets in the tests (v0: the parent's key; ADR-0009 open item).
- Carried: `Job.spec.activeDeadlineSeconds` mutability (`0bc.4.3`);
  `payment` absent (M5); `fh2y`; open problems 8, 9.

## Suggested next steps

- `0bc.4.3` k8s driver and `0bc.4.4` docker driver — outside
  `protocol/` (as `iroh-transport/`), implementing
  `provisioner.Driver`; the pre-push vendorHash hook will bite on
  every `protocol/` touch.
- `0bc.4.5` `cmd/child`: `spawn.Born` + a renew loop + self-lapse.
- Promote ADR-0008/0009 at `0bc.4.6`; prune exploration-log §M4 then;
  glossary "built" notes for Provisioner/Driver/Lease (proposed below
  in the session report, not yet written).
