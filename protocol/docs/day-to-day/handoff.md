# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-23 — **M4.1 built: `protocol/spawn`** (`25e7dd0`; ADR-0008's
handshake and the spawner half of ADR-0009). ~600 LOC + tests.

- **`Spawner`** on an actor: `Spawn(spec)` mints the per-spawn birth
  consent (frontdoor shape on `#birth`) and a 128-bit nonce, installs
  the consent, records the spawn, sends `#spawn {image, params: intro,
  until: consent.exp}` to the provisioner (empty chain under its
  consent) and returns a `Promise` → `Birth{ID, Location, Lease}`,
  resolved once *both* the knock and the lease are in (either order).
  `#birth` binds the nonce to the first key, re-sends the kit verbatim
  to that key, refuses others; mints `[P→C invoke {target [P], facet
  [#renew]}]` and installs it as P's own consent; `Spec.Outfit` adds
  the parent's choice. `Sweep()` drops records + consents at the
  window (`ErrBirthWindow`); called from `Spawn`/`#birth`, no goroutine
  — the owner calls it on its beat.
- **`Born(intro, facets, ttl)`** is the child: `CheckIntro`, stamped
  knock, `CheckKit` (mandate present, every link live, last aud is
  me), `Install`, and the child's own consent to P.
- **Found while building.** (1) *Any* live birth consent admits a
  knock — they are one shape granting one thing; the nonce alone
  selects the spawn, so a "presented under its own consent" rule was
  dropped as fiction. (2) Same-second spawns mint **byte-identical
  consents** (deterministic sig, no per-spawn caveat): the spawner
  holds them as one root and drops only when no pending spawn names
  it — else one spawn's refusal unrooted its twin (`TestSpawnRefused`).
  Glossary updated.
- Tests: two actors over `MemoryNetwork` with a fake *provisioner
  actor* whose `#spawn` runs the child in-process (the "fake driver").
  Race-clean; `scripts/test-iroh.sh` green.
- Earlier the same session: confirmed the Quint own-consent strip had
  already landed (`08efe79`); both handoffs corrected.

## Loose threads

- **`6sax` (P1, blocks `0bc.4.2`): renewing a root consent must
  re-install it.** The kit's `#renew` cert *is* P's consent; after the
  child renews it, P holds only the old one, so the fresh chain folds
  as a link (P must resolve to aud C) and fails; when old expires
  nothing roots. Fix in `renewHandler` (swap fresh into `Consents` when
  old is byte-equal to a held consent — ADR-0005 test 2) or in the
  M4.2 decorator. Decide first.
- M4.1 already ships the `#spawn` client and the `#extend`/`#kill`
  wire types; `0bc.4.2`'s "spawner client" is done — what remains
  there is the provisioner server (lease machine, `Driver`) and the
  `#renew` decorator.
- `Spawn` holds the spawner's mutex around `Authority()`+`Hold()`; an
  owner's own `Hold` (hub-style unseal) interleaving would drop birth
  consents. Fine for a laptop parent; note if a hub ever spawns.
- Dates: the M4 design is stamped 2026-09-24 in ADR-0008/0009, the
  exploration log and the glossary, but landed 2026-09-21
  (`d5672e2`). Fixed only in the § Spawning line touched.
- Carried: `Job.spec.activeDeadlineSeconds` mutability (`0bc.4.3`);
  provisioner's consent to customers = parent's key in v0; `#extend`
  synchronous in a handler; `payment` absent (M5); `fh2y`; open
  problems 8, 9.

## Suggested next steps

- Rule on `6sax`, then `0bc.4.2`: provisioner actor with the lease
  machine and `Driver{Start, Extend, Kill}`, the `#renew` decorator
  sending `#extend {until: fresh.exp}`; protocol test with a fake
  driver on a *real* `#spawn`/`#extend` path.
- Promote ADR-0008/0009 at `0bc.4.6`; prune exploration-log §M4 then.
