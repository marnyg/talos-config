# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-13 — **M2 closed (`0bc.2`), post-M2 hardening swarm landed**
(`main 4b5a9d0 → bdf5488`; five workers, two waves, all merged with
`--no-ff`; protocol detail in `protocol/docs/day-to-day/handoff.md`).

- Protocol: `kp4` `Verified` on reject, `6tf` `ErrPostageConflict`,
  `02j` self-guarded `clock.Mark`, `djs` stale comments. `3k5` ruled
  (decision `0i6`), `xom` closed (contradicts invariant 1), `ax7` found
  already fixed.
- `cs3`: the first CI musl probe **never reached musl** — the pkgsStatic
  import of `iroh-go/nix` made the cargo-vendor python helper static
  (no `requests`). Fixed: vendor + source prep via `pkgs.buildPackages`;
  README §Static link records it. The next `static` job on `main` is
  the real probe.
- Swarm mechanics that worked: `swarm-prep` wrote acceptance/design
  onto the beads + `/tmp/swarm/<name>.{task,context}.md`; opus-5 for
  mechanical workers, fable for the nix hypothesis; orchestrator re-ran
  every acceptance before `--no-ff` merge; `git pull --ff-only` only.

## Loose threads

- Merged-but-open beads awaiting owner close: `kp4 6tf 02j djs ax7`.
  `cs3` stays open until the post-`bdf5488` CI `static` job is read.
- Rulings wanted (`thread`): `0lo xwu 5yj 7w5 7ei 7n8 s8n` — `xwu`
  gates M3's relay/`reach-me-at` chains.
- Carried: `359.8.5` / `6z9` questions; `54n` boot-token HMAC;
  ADR-0017/0019 still Proposed; GH cache 7-day eviction; `4te`
  parents' TV.

## Suggested next steps

- Read the CI `static` job for `bdf5488` (`gh run list --workflow
  iroh-transport.yml`) → settle `cs3` and the Talos-extension link story.
- Rule `xwu`/`0lo`, then pick the next milestone: `0bc.3` M3 lighthouse, or Phase 0 probes
  `359.1.1–.3` (they gate `0bc.2.7` on real Talos nodes).
