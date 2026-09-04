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

**Five spec defects found before any code exists** — filed, then
**ruled the same session** (decisions `h3c zqw dvf syw 6o1`) and folded
into ADR-0017, ADR-0015 and the glossary:

1. `3cx` → **step (2b)**: the member cert's issuer must hold a live
   consent grant from the receiver (else stranger-chosen name/groups
   reach the gateway header).
2. `z1z` → runway claim is **6 days of starvation** (lifetime − poll
   cadence; clock = time since last completed beat, so unseal→redeploy
   flapping accumulates). Class lifetimes unchanged.
3. `sqm` → renewal is **strict**: expired certs don't renew; > 30 d
   starvation costs one wallet act per device.
4. `xwz` → `reach-me-at` is **self-issued by every actor**; hub relays.
5. `vzj` → ADR-0015 token is **single-use per hub process,
   TTL-bounded**, with an embedded nonce; cross-redeploy replay of a
   leaked token is an accepted residual (`check.sh` asserts it stays).

**Design review after the models** (conversation, no code): incidental
issues separated from structural ones. Structural trade-offs (revocation
latency ≥ runway; stateless ⇒ "may" not "how much"; capabilities end at
the stream terminator) are now stated in `invariants.md` §Structural
trade-offs. Unaddressed structural gaps filed: **`439`** time as trust
dependency (spike, P1 — the one to actually worry about), **`7vv`**
unseal as `speak-as` cert instead of KDF seed (spike, P1 — master-as-
signature is phishable and unrotatable), `zph` bootstrap anchor is web
PKI (inv. 3 honesty), `4un` policy-compiler round-trip property,
`1gv` gateway header forgeable from LAN, `ure` blocklist propagation.

## Loose threads

- ADR-0017 is still *Proposed*; the 2026-09-05 amendments live inline
  with dated notes rather than a new ADR — fold them into the text when
  it is promoted at `359.8.1`/`359.8.5`.
- `docs/mesh-v3-iroh.md` and `sovereign-actor-protocol.md` were not
  re-swept for the 7-day / reach-me-at wording; grep `7 d`, `reach-me-at`
  before Phase 1.
- Nickel v3 targets (`6z9` note): facet-class policy contract is
  spec-ready; accept tables / name map wait on `359.8.5`'s schema.
- `cmi` (wire `check.sh` into CI) is more urgent now: the suite drifted
  for two weeks unnoticed. `verify` tier takes ~3 min (approval at
  depth 12 dominates).
- Carried: ADR-0017 Proposed until `359.8.1`/`359.8.5`; `359.4/.6/.7`
  Q-threads; `siweoidc` hardcoded groups (`5kh`).

## Suggested next steps

- Rule on `7vv` (unseal design) and `439` (time) before `359.8.1` —
  both change what Phase 1 builds.
- `0bc.1`: the Go `authorize()` + rapid suite now has a Quint oracle
  with a settled spec — port the 13 laws 1:1 (step 2b included);
  add ITF trace replay so spec and code cannot drift silently.
- Or proceed with Phase 0 (`359.1.4` first); nothing here blocks it.
