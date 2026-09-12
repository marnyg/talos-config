# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-12 — **`iroh-go.yml` green on x86_64-linux; `3i3` done**
(`main 09410f5 → HEAD`). Short session, single thread.

- The two red 2026-09-06 runs were **not** the feared 2.5 h timeout —
  they died in 10 min building `iroh-relay`: upstream iroh's
  `.cargo/config.toml` pins `linker = clang` + `-fuse-ld=lld` for
  `x86_64-unknown-linux-gnu`, which the nix sandbox can't satisfy
  (`collect2: cannot find 'ld'`). Darwin never hits that target
  section. Fix: `postPatch = rm -f .cargo/config.toml` in the
  `iroh-relay` derivation (`iroh-go/nix/default.nix`). `iroh-ffi` and
  `uniffi-bindgen-go` sources carry no such file.
- Second failure was the workflow itself: `nix build .#iroh-relay` in
  the custom-relay step re-pointed `./result`, orphaning
  `./result/bin/smoke`. `--no-link` fixes it.
- Run 34694970750: **`smoke` 45 s warm (16 m 39 s cold), `drift`
  8 m 41 s** — bindgen builds in ~8 min on the 4-vCPU linux runner vs
  ~45 min on M-series. Checked-in `iroh-go/iroh/` has zero drift.
  Times are in `iroh-go/README.md` §Sizes/times and the workflow
  header; the "x86_64-linux unverified" rows in the README and
  `docs/mesh-v3-iroh.md` are updated. `timeout-minutes` untouched.
- CI hygiene: `actions/checkout@v5` in all workflows (Node 20
  deprecation), `use-flakehub: false` on magic-nix-cache (kills the
  bogus per-job `##[error]`).

## Loose threads

- **`49x`** (debt) — yamlfmt trailing-comma quirk `{…,}` in
  `talos/mesh-policy.yaml:46,72` and the v3 fixture.
- **`359.8.5`** still carries the two `6z9` questions (hub `relay` as
  grant vs membership; does the hub dial node `apid` under v3).
- Static-musl Talos-extension link (`pkgsStatic`) remains unattempted;
  `iroh-go.yml` verifies the glibc-dynamic x86_64-linux path only.
  When iroh is bumped, re-check whether upstream still ships the lld
  pin (the `postPatch` is harmless either way).
- GH Actions cache evicts after 7 idle days; a quiet fortnight means a
  ~25 min cold `iroh-go.yml` run — tolerable, no `schedule:` added.
- Boot-token HMAC key source (`54n`), ADR-0017 Proposed until
  `359.8.1`/`359.8.5`, ADR-0019 NTP gate → `359.1.3` — carried.

## Suggested next steps

- `/skill:grill-design` on `0bc.2` (M2 envelope + actor runtime) — the
  only un-briefed item on the protocol critical path.
- Phase 0 probes `359.1.1–.3` once fly scratch + an Android device
  exist (`359.1.1` can use `nix build .#iroh-relay`).
