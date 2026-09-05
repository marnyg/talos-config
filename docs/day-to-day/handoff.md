# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-06 — **the two structural gaps ahead of Phase 1 are ruled:
`7vv` (unseal) → ADR-0018 and `439` (time) → ADR-0019.** Both
spikes closed; both decisions recorded (`89v`, `dyf`). One new Quint
model (`clock.qnt`), no Go.

**`439` — time is a trust input (ADR-0019).** Split in two: the
*upper* bound (roll-forward = denial) is the local clock, ops; the
*lower* bound (rollback = resurrection of expired certs, the LAN-NTP
attack) is protocol-enforced — every cert carries **`iat`**, the
verifier keeps an **uncapped** monotone low-water mark `lw` over the
`iat` of every wallet-rooted cert it verifies and judges with
`now = max(local, lw)`. No time authority. Rollback exposure =
verifier starvation, the same clock `runway.qnt` measures.
**Model-first finding:** my first draft capped the mark at
`local + s`; `clock.qnt` with `CAP=true` violates two laws — after a
rollback the cap discards exactly the honest evidence that would catch
it. Ruled uncapped; residual = a lying *trusted* issuer can deny until
verifiers restart (weaker than what it can already do).
`clock.qnt` is in `check.sh` (run + verify, 3 mutants die).
Invariant 2 gained "safe-to-lose caches"; §Structural trade-offs
gained "time is a trust input"; glossary *Time / lw*; primitive is now
`{iss, aud, can, cav, iat, exp, sig}` — **`0bc.1` must fix field order
before building.**

**`7vv` — unseal as `speak-as` (ADR-0018).** Master roots two unlike
things — authority and secret material; `speak-as` replaces only the
first, so the hub becomes the protocol's *hot key for a cold root*
(random key per process, wallet signs `speak-as` at unseal, 120 d,
`/sealed` nags at < 30 d) and the master shrinks to a secrets seed.
Three consequences captured: resolve issuer before compare (depth 3),
verification-time validity (runway bound by the `speak-as`), literal
caveats. Invariant 1 "wallet-derived" → "wallet-rooted"; invariant 2
gained actor-owned state. Derived: `861` hub-as-actors spike, `qrb`
actor-owned-state spike (P1, **open: Provisioner seed wallet-derived
vs fly-held**). Also: exploration log pruned (ADR-0016/0017 sections),
ADR-0004 records the KMS grace window as an accepted residual.

## Loose threads

- `runway.qnt` (`jp2`) and `authorize.qnt` (`czi`) do not yet model
  the `speak-as` link; the 30 d member-runway claim (`z1z`) is
  unverified under the new bound until they do. (`clock.qnt` is done;
  `authorize.qnt` only gained a header note — its `NOW` is now the
  effective clock.)
- Boot-token HMAC key source is an **open trade-off** in ADR-0018:
  `hubkey` closes the replay residual but strands tokens served before
  a redeploy; secrets seed keeps today's residual. Model both in
  `approval.qnt` (`54n`); `check.sh`'s negative assertion flips only
  if `hubkey` wins.
- ADR-0019's node-agent consequence (gate *accepting* on NTP sync or a
  persisted `lw`) is a probe item for `359.1.3`; noted there.
- ADR numbering: `k3o`'s monorepo ADR is now **0020**.
- Carried: ADR-0017 Proposed until `359.8.1`/`359.8.5`; the 2026-09-05
  inline amendments still not folded; `cmi` CI for `check.sh`.

## Suggested next steps

- Rule `qrb` (Provisioner seed location) — it is the last open input
  to ADR-0018 and to what `359.8.1` builds.
- Extend `authorize.qnt`/`runway.qnt` with the `speak-as` link per
  the notes on `czi`/`jp2`; that either confirms the 120 d / 30 d-nag
  numbers or refutes them before any code.
- With `7vv` and `439` ruled, **nothing structural blocks Phase 0**
  (`359.1.4` first) or `0bc.1` (primitive with `iat`, `authorize()`
  with effective `now`, rapid laws from `authorize.qnt` + `clock.qnt`).
