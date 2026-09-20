# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-21 (eleventh session) — **backlog grooming only. No code
changed**; the working tree is untouched since `7a0d084`. The whole
session moved state from prose into the issue graph.

- **Eleven issues closed.** Three were duplicates or resolved gates
  (`im9` was a verbatim copy of `p5g`; `vdv`'s first half landed as
  `359.8.2.2` and its second half *is* `5gz`; `7ci`'s gate `359.1.5`
  passed). Five were nebula-era work the iroh migration had already
  eaten (`adz` `9nf` `exq` `en6` `4ns`). `359.8.2` ("Hub as actors")
  was open with all four children and six decision beads closed and
  its parent Phase 1 closed on 09-19 — a leftover, not work.
  `t6n` is subsumed by `359.11.2`'s `neb*.go` deletion scope.
- **The last session's loose threads are now beads, not prose.**
  `eq91` (the handover issue) was carrying six threads in its body;
  each is now a real issue with its own edges, and `eq91` is closed.
  The three post-TV cleanups (`vftt` cut `jellyfin.cp1`, `ri3b` delete
  the hub's `/hosts` + `/policy`, `xnat` drop the `1gv` gate and
  `hostNetwork`) are blocked on the TV migration so they cannot
  surface as ready work early.
- **The Mesh v3 spine was verified coherent end to end**:
  `359.9.4 → 359.9.5 → 359.10 → 359.11 → .1→.2→.3→.4 → ihn`.
  Deletion genuinely cannot precede soak. 53 → 51 open, deferred
  17 → 11.

## Loose threads

- **Still nothing played.** `359.9.4.4` is the one unproven item in
  P2.4's acceptance criteria — transport is verified on both path
  types, media is not. It is the only real P1 in the repo.
- **`359.9.4.4` was created as `in_progress`, not `open`** —
  `bd create --parent` appears to inherit the parent's status, and
  `bd ready` excludes `in_progress`. So the highest-priority task in
  the repo is currently absent from the ready queue and reads as
  though someone is on it. Nobody is. Fix with
  `bd update talos-config-359.9.4.4 --status open`.
- **`0q0` moved from `blocked` to `deferred`.** Nothing in the graph
  blocked it; the gate is buying hardware. The knowing deviation from
  invariant 2 (`longhorn-bulk` at 1 replica) is unchanged by this —
  but the bead is now less visible, so the deviation is easier to
  forget. See the invariant-2 note under "Workloads / storage".
- **`bd ready --exclude-type` is a silent no-op** in `1.0.3 (dev)` for
  both `epic` and `bug`. Epics therefore rank inside `bd ready` —
  `0bc` and `359` are P1 and outrank real P2 work. Left as-is by
  owner ruling rather than deferring or demoting live epics to satisfy
  a broken filter; `bd ready -n 99 | grep -v '\[epic\]'` is the
  workaround. Unfiled upstream.
- Not done, offered and skipped: `4mg`/`owh` prose-vs-graph drift
  (both say "wait for X" where X has landed or has no edge), and
  `bsj` → `98d` ordering under the Longhorn epic.

## Suggested next steps

- Re-open `359.9.4.4`, then finish it: Quick Connect the Jellyfin app
  in, play something, read `paths` under load.
- Then `359.9.4.5` (the TV), which unblocks `vftt`/`ri3b`/`xnat` in
  one go.
- Then P2.5 (`359.9.5`): k8s/Talos endpoint off the mesh.
