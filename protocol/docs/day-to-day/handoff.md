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

- ~~`6sax`~~ ruled and fixed the same session: `renewHandler` now
  re-installs a renewed cert that is one of its own consents
  (`reinstallConsent`; old stays until its exp, expired roots dropped;
  no-op for links). ADR-0005 row. Laws:
  `TestRenewOwnConsentReinstalls`, second beat in `TestSpawnBirth` —
  both mutation-tested. `0bc.4.2` is unblocked.
- M4.1 already ships the `#spawn` client and the `#extend`/`#kill`
  wire types; `0bc.4.2`'s "spawner client" is done — what remains
  there is the provisioner server (lease machine, `Driver`) and the
  `#renew` decorator.
- Carried: `Job.spec.activeDeadlineSeconds` mutability (`0bc.4.3`);
  provisioner's consent to customers = parent's key in v0; `#extend`
  synchronous in a handler; `payment` absent (M5); `fh2y`; open
  problems 8, 9.

## Suggested next steps

- `0bc.4.2`: provisioner actor with the lease
  machine and `Driver{Start, Extend, Kill}`, the `#renew` decorator
  sending `#extend {until: fresh.exp}`; protocol test with a fake
  driver on a *real* `#spawn`/`#extend` path.
- Promote ADR-0008/0009 at `0bc.4.6`; prune exploration-log §M4 then.
