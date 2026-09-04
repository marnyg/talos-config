# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-05 — **formal verification caught up with the plans.** The
Quint suite had not moved since 2026-08-22; everything decided since
(ADR-0015 commitment, ADR-0016/0017, the `359.2` glossary pins) was
unmodeled. Three models now cover it, all mutation-tested, `run` and
`verify` tiers green (`verification/quint/check.sh`):

- **`authorize.qnt`** (new, `czi`) — ADR-0017's per-connect check as
  one pure function; 13 laws (receiver-rooted, monotone attenuation,
  fail-closed, issuer-scoped groups, identity-from-member-only, …).
  Needed a fault-injection generator: pure random worlds never reach
  Accept, so every law held vacuously and 7/7 mutants survived until
  the generator was biased to the boundary.
- **`runway.qnt`** (new, `jp2`) — cert lifetimes vs. a sealed hub, in
  hours, with nondet init anywhere in the renewal cycle; clock is
  *starvation* (hours since the last served beat), not "sealed for".
- **`approval.qnt`** (rewritten, `54n`) — ADR-0015 boot-token join:
  stateless HMAC token, leak/forge adversary, wipe + rejoin with no
  second approval, decommission.

**Five spec defects found before any code exists** — each filed as a
`thread` and encoded as an "expected violation" the suite checks
negatively, so fixing the doc flips it visibly:

1. `3cx` (P1, bug) — glossary *Authorize* step (2) never checks the
   member cert's **issuer**; stranger-signed member cert + an Owner's
   `aud:<key>` grant ⇒ Accept with stranger-chosen name/groups in the
   gateway header. Model adds step (2b).
2. `z1z` (P1, bug) — ADR-0017 §Confirmation "sealed < 7 d loses no
   access" is false: runway = lifetime − poll cadence = **6 d** for
   invoke grants; and unseal→immediate redeploy (`fbb`) is "sealed
   0 h" with access gone — state it in starvation terms.
3. `sqm` (spike) — can an *expired* member cert renew? Strict reading
   ⇒ > 30 d starvation costs a wallet act per device.
4. `xwz` (spike) — who issues `reach-me-at` (1 h) for machines? If the
   hub, one sealed hour kills name→endpoint for nodes.
5. `vzj` (P2, bug) — ADR-0015 token cannot be both "single-use" and
   "stateless, no pending table"; holds per hub process only. Token
   also needs an embedded nonce.

## Loose threads

- The five findings above are **filed, not fixed**: ADR-0017, ADR-0015
  and the glossary still carry the refuted sentences. Decide per item
  (fix doc / change design), then flip the `expect_violation` lines in
  `check.sh` and the FINDING blocks in the model headers.
- Domain-model sync proposals from this session (not written — need
  a decision): Authorize step (2b); lifetimes rule as "runway =
  lifetime − cadence, measured as starvation"; reach-me-at issuer.
- Nickel v3 targets (`6z9` note): facet-class policy contract is
  spec-ready; accept tables / name map wait on `359.8.5`'s schema.
- `cmi` (wire `check.sh` into CI) is more urgent now: the suite drifted
  for two weeks unnoticed. `verify` tier takes ~3 min (approval at
  depth 12 dominates).
- Carried: ADR-0017 Proposed until `359.8.1`/`359.8.5`; `359.4/.6/.7`
  Q-threads; `siweoidc` hardcoded groups (`5kh`).

## Suggested next steps

- Rule on `3cx`, `z1z`, `vzj` (one sentence each in the ADR/glossary),
  then close `czi`/`jp2`/`54n` and turn their negative checks positive.
- Then `0bc.1`: the Go `authorize()` + rapid suite now has a Quint
  oracle — port the 13 laws 1:1.
- Or proceed with Phase 0 (`359.1.4` first); nothing here blocks it.
