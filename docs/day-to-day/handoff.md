# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-06 (evening) — **ruled `2g6`, then swarm batch 3 landed**
(`main f814e99 → bbcec8f`). Workers ran as `pi` agents in herdr panes
(tab `swarm-3`), one worktree each under `~/git/swarm/<id>`; briefs in
`_briefs/`, reports in `_reports/`. Orchestrator re-ran every gate
before fast-forwarding.

- **Rulings (decisions `7ry` `jo8` `c4c`, from `2g6`)**: "rooted at the
  receiver" is **signature-only provenance** (consent expiry at `now`
  irrelevant — kills the `now → rooted → lw → now` loop; residual: an
  ex-principal's hot key can still push the mark, same class as Lying);
  case (c) rooting ignores speak-as `cav.verbs`/`groups`; ADR-0019's
  "update first, then judge" holds *across* bundles, not within one.
- **`2qp`+`8vg`** — `buildRooted` is provenance-only (`rootedPrincipals`
  + `rootedHotKeys`, no `resolve()`, no `now`); 7 `clock.qnt` laws
  ported 1:1 in `protocol/clock/rooted_laws_test.go` over 7 issuer
  shapes; 6 mutants killed incl. the pre-7ry/pre-jo8 behaviours; docs
  synced (mark.go, protocol invariants §9, ADR-0019 addendum, both
  glossaries). `authorize.qnt` unaffected — its rootedness is
  authorization-rootedness, correctly live-at-`now`. `5245ff7` `b703369`.
- **`81u`+`m60`** — `nix flake check --impure` is **green on `main`**
  for the first time: 18 YAML files yamlfmt'd (0 semantic diffs, yq
  re-proved by the orchestrator), `--impure` canonical (devenv reads
  `$PWD`), `meta.description` on 5 apps; `nickel` job in `verify.yml`
  via `nix shell --inputs-from . nixpkgs#nickel`. `4823d1b`…`c30b9f6`.
- **`htt`+`g3u`** — iroh-ffi lock patched to **core iroh 1.1.0**
  (regen byte-identical, eval-time assert ties lock to pin); new
  `.github/workflows/iroh-go.yml` (`smoke` + `drift` on
  `ubuntu-latest`, magic-nix-cache). `b0a7cd6` `d004258`.
- **Orchestrator commit `bbcec8f`** — `verify.yml` quint jobs now use
  the flake-pinned `nix shell … nixpkgs#quint` (no npm/java pins),
  `nix-installer-action@v22` everywhere; `**/testdata/rapid/`
  gitignored; gofmt + dead import in `protocol/cert`; mesh-v3 doc says
  core 1.1.0.

## Loose threads

- **`iroh-go.yml`** first `main` run is in flight, cold (~45–60 min
  smoke + 90–120 min drift, estimates). ADR-0021 is **Accepted** (owner
  ruling, ahead of the run). **`3i3`**: check the run, paste CI times
  into `iroh-go/README.md`. If the 4-vCPU runner blows
  `timeout-minutes`, raise it rather than split.
- **`49x`** (debt) — yamlfmt trailing-comma quirk `{…,}` in
  `talos/mesh-policy.yaml:46,72` and the v3 fixture; block-style rewrite
  or upstream.
- **`359.8.5`** still carries the two `6z9` questions (hub `relay` as
  grant vs membership; does the hub dial node `apid` under v3).
- Static-musl Talos-extension link (`pkgsStatic`) remains unattempted;
  `iroh-go.yml` verifies the glibc-dynamic x86_64-linux path only.
- Boot-token HMAC key source (`54n`), ADR-0017 Proposed until
  `359.8.1`/`359.8.5`, ADR-0019 NTP gate → `359.1.3` — carried.

## Suggested next steps

- Check the first `iroh-go.yml` run; close `3i3`.
- `/skill:grill-design` on `0bc.2` (M2 envelope + actor runtime) — the
  only un-briefed item on the protocol critical path.
- Phase 0 probes `359.1.1–.3` once fly scratch + an Android device
  exist (`359.1.1` can use `nix build .#iroh-relay`).
