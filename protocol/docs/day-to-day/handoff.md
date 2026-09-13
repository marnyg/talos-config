# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-13 — **M2 closed (`0bc.2`); five-worker hardening swarm
landed** (`main 4b5a9d0 → bdf5488`, two waves, herdr worktrees, briefs
and reports under `/tmp/swarm/{p-verify-result,p-postage-err,
n-static-probe,p-mark-mutex,d-stale-comments}.md`).

- `kp4` — `envelope.Verify` returns `Result{Verified}` **beside**
  `ErrChain`; the actor's capture closure is gone
  (`Chain: cert.VerifyChain`, `ObserveAll(res.Verified)` on both paths).
- `6tf` — `cert.ErrPostageConflict` wraps `ErrUnknownCaveat`
  (`errors.Is` both); `Caveats.PostageConflict` is a verifier-side flag
  beside `Unknown`, never on the wire. `authorize.qnt` untouched: the
  model has no error identities, taint stays one flag.
- `02j` — `clock.Mark` guards itself (`sync.Mutex`, zero value usable,
  don't copy); `actor.process` takes `a.mu` only around the location
  update; `nowLocked` → `now`.
- `djs` — `protocol/doc.go` layout lists `envelope/ actor/` + the
  out-of-module `iroh-transport/`; `consentsFor` doc says it is not the
  rooting rule; `envelope_test.go` no longer claims Decode rejects `"*"`.
- Rulings: `3k5` closed by decision `0i6` (keep the double signature
  verification in v0); `ax7` found **already fixed** on main (40c1755).

## Loose threads

- **Musl probe green** (2026-09-13, run 34755622342): fully static
  x86_64-linux iroh-transport passes 6/6 incl. relay. It took three CI
  runs — pkgsStatic writers (fixed via `buildPackages`), then a stale
  Go `vendorHash` (vendored `../protocol` had changed; cached FOD hid
  it). `cs3` closed; `iroh-transport/README.md §Static link` has the log.
- Rulings still wanted (`thread` beads): `0lo` absent `endpoints` = ∅?;
  `xwu` verb uniformity (gates M3 relay/`reach-me-at` chains); `5yj`
  per-edge serial `Send`; `7w5` signed cheap rejects; `7ei` strict
  `#renew` aud; `7n8` chain cap; `s8n` aud-side speak-as and the mark.
- From the reports: should a rejected-but-valid `loc` refresh the
  location cache? (`Result.Loc` stays nil on `ErrChain`.) Filed as a
  thread bead.
- Beads `kp4 6tf 02j djs` are merged and noted, **open for the owner to
  close**; `ax7` too.

## Suggested next steps

- Rule `xwu` (and `0lo`) before starting M3 `0bc.3`; both bite the
  first `reach-me-at`/`relay` chain.
- Owner picks M3 (`0bc.3`) vs Mesh v3 Phase 0 probes (`359.1.1–.3`).
