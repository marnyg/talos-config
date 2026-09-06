# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-06 — **swarm batch 2 landed** (`main a0e538a → 5c46b0e`): four
bd issues run as `pi` workers in `~/git/swarm/<id>` worktrees, each
reviewed + gated by the orchestrator, fast-forwarded, pushed. Briefs
in `~/git/swarm/_briefs/`, reports in `~/git/swarm/_reports/`.

- **`6z9` (part a)** — `verification/nickel/mesh-policy-v3.ncl` + spec
  fixture: closed receiver kinds, closed per-kind facet sets, no
  ports, one-of host/group, `group: machines` never on a node facet;
  12 new mutations. Part (b) stays open behind `359.8.5`. `867beac`.
- **`zev`** — `clock.qnt` issuer classes `Honest | Lying | Stranger`;
  `invStrangerNeverAdvances` (equality form) + `invStrangerNeverAccepted`;
  10 mutants. `verify clock.qnt` is now ~110 s. `15ef32f`.
- **`44r`** — `protocol/cert`: set-valued `resolve()`, one-consented-`w`
  group rule, grant-side `cav.groups ∋ g`, 8 rapid law ports,
  `TestRogueVouchesBothHubsRejects`, exhaustive `TestFaultPairSweep`
  (2424 scenarios — rapid at 100 checks was too shallow for
  pair-faults; m14 died only ~6 %/run). `de21566`.
- **`ow7`** — `iroh-go/`: in-house Go binding via uniffi-bindgen-go
  0.7.1+v0.31.0 off iroh-ffi 1.1.0 (three textual fixups, no fork);
  flake packages `iroh-go iroh-go-smoke iroh-ffi iroh-ffi-static
  iroh-relay uniffi-bindgen-go`, app `iroh-go-regen`; direct and
  custom-relay smokes pass on aarch64-darwin **and** aarch64-linux.
  **Recommendation: bindgen, no sidecar.** `c043158`; data in
  `docs/mesh-v3-iroh.md`.
- Ruled with `44r`: the model's step 2b now also requires the member's
  consent to `target ∋ R` (`5c46b0e`); Go already did.

## Loose threads

- **`2g6` (thread)** — "rooted at the receiver": signature-only
  (`clock.qnt`) or live-at-`now` (Go `buildRooted` needs the consent
  unexpired at `now`)? Plus verb-scoped rootedness in case (c) and
  per-bundle judge-then-update ordering. `2qp` must not port blind.
- **`359.8.5`** now carries two questions from `6z9`: is hub `relay`
  an `invoke {facet: relay}` grant or membership-implied (glossary
  lists `relay` as both a facet and a verb); does the hub still dial
  node `apid` under v3?
- **`htt`** — iroh-ffi 1.1.0's lock pins core iroh 1.0.2; bump tested
  green (identical binding) but not committed. **`g3u`** — x86_64-linux
  and static-musl Talos-extension link are unverified; a CI job on
  `ubuntu-latest` closes the first.
- Protocol sub-scope glossary drift: `protocol/docs/desired-state/
  domain-model.md` *Attenuation* still says the group rule is "same
  key as the grant's iss" (pre-`9l3`), and *Time / low-water mark*
  lacks "rooted" (also `8vg` for `mark.go` + invariants §9).
- Debt filed: `m60` (CI does not run the Nickel check), `81u`
  (`nix flake check` red on `main`: yamlfmt on 17 YAML files).
- Boot-token HMAC key source (`54n`), ADR-0017 still Proposed until
  `359.8.1`/`359.8.5`, ADR-0019 node-agent NTP gate → `359.1.3` —
  carried unchanged.

## Suggested next steps

- Rule `2g6`, then swarm `2qp` (Go port of the stranger laws) — the
  zev report lists the 7 laws to port.
- `0bc.2` needs a `/skill:grill-design` pass before it can be briefed.
- Phase 0 probes `359.1.1–.3` once fly scratch + an Android device
  exist; `359.1.1` can now use `nix build .#iroh-relay`.
