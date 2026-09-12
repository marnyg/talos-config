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

- `0bc.2.1–.6` and `49x` closed 2026-09-13; `0bc.2` (M2 epic-level
  task) left open for the owner to close. Worker workspace `w1E` and
  branch `swarm/iroh-transport` removed.
- **pkgsStatic/musl probe not yet executed** — `cs3`: read the first
  `iroh-transport.yml` `static` job, record in `iroh-transport/README.md`.
- Worker threads filed 2026-09-13 as 14 beads (`ax7 kp4 02j 0lo xwu
  5yj 3k5 7w5 7ei 7n8 6tf s8n djs cs3`) — list in the protocol handoff.
- Carried: `359.8.5` / `6z9` questions; `54n` boot-token HMAC;
  ADR-0017/0019 still Proposed; GH cache 7-day eviction; `4te`
  parents' TV.

## Suggested next steps

- `cs3` (CI static-probe log), then `ax7`/`kp4` (small, unblock
  `reach-me-at` on the wire).
- Close `0bc.2` if M2's acceptance is judged met.
- Pick the next milestone: `0bc.3` M3 lighthouse, or Phase 0 probes
  `359.1.1–.3` (they gate `0bc.2.7` on real Talos nodes).
