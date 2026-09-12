# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-12/13 — **M2 built: the six-worker swarm on `0bc.2` landed**
(`main c67e9e7 → ac91a93`, four waves, herdr worktrees, briefs under
`/tmp/swarm/`). Protocol-scope detail is in
`protocol/docs/day-to-day/handoff.md`.

- Waves 1–3 merged in-session: `authorize.qnt` N-link verifier (`.1`),
  `protocol/cert` `VerifyChain` + caveats v2 (`.2`), `protocol/envelope`
  (`.3`), `protocol/actor` runtime (`.5`), `49x` yamlfmt block-style.
- Wave 4 (`0bc.2.6` iroh-transport) was **cut off by API 429s** after
  the worker wrote `iroh-transport/nix/default.nix`; the next session
  finished it: `flake.nix` wiring (`.#iroh-transport`; `-static`
  Linux-only via `mkMerge`), `vendorHash`, `iroh-transport/README.md`,
  `.github/workflows/iroh-transport.yml`. `nix flake check --impure`
  green; `nix build .#iroh-transport` 6/6 on aarch64-darwin.
- `git pull --rebase` **flattened every `merge swarm/*` commit** (default
  rebase drops merges). Content identical; the merge SHAs quoted in the
  `0bc.2.x` bead notes no longer exist (see the `0bc.2` note).

## Loose threads

- **Beads still `in_progress`, awaiting your close:** `0bc.2.1 .2 .3 .5
  .6`, `49x`. The `iroh-transport` herdr workspace (`w1E`, branch
  merged) is still open — retire with `herdr worktree remove`.
- **pkgsStatic/musl probe not yet executed** — no Linux builder here.
  The first `iroh-transport.yml` `static` job run (push of `ac91a93`)
  is the result; read it and record in `iroh-transport/README.md`.
- **Worker threads not yet filed as beads** (see protocol handoff for
  the list): `validateAud` rejects `"*"`; `envelope.Verify` drops
  `verified` on reject; `clock.Mark` needs its own mutex; per-edge
  serialised `Send` vs windowed HWM; strict `#renew` aud; absent
  `endpoints` = ∅ vs unconstrained; chain-length cap; `quint verify`
  authorize tier now ~94 s (check.sh comment stale).
- `protocol/doc.go` layout comment still lists only `cert/` + `clock/`.
- Carried: `359.8.5` / `6z9` questions; `54n` boot-token HMAC;
  ADR-0017/0019 still Proposed; GH cache 7-day eviction; `4te`
  parents' TV.

## Suggested next steps

- Close the six beads; retire `w1E`; check the CI static-probe log.
- Triage the worker threads into `debt`/`thread` beads (one `bd create`
  each) or rule them.
- Pick the next milestone: `0bc.3` M3 lighthouse, or Phase 0 probes
  `359.1.1–.3` (they gate `0bc.2.7` on real Talos nodes).
