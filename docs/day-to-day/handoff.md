# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-20, token rotated; flow v2 proven and found two harness gaps)

- **`97rz` closed**: Forgejo bot token `sandbox` → `sandbox-20261020`,
  resealed (`0eb7f5d`), Argo unsealed, `200` from `sap`; the leaked
  token DELETEd as `sysadmin` through the in-cluster API (the sysadmin
  password is the sealed `forgejo/forgejo-sysadmin` — no paste needed).
- **First flow-v2 run** (task `1d1c85ac`, project `test`,
  `agents/scratch`): the worker committed only — no push, no PR, no
  credential hunt, 10 s. The harness then **pushed nothing**:
  `child/workspace.go` `result()` read an empty index after `git add -A`
  as "nothing changed" (v1's shape; v2's worker commits itself). Fixed
  `d0f3214` — "changed" is HEAD moved off the cloned commit; the clone
  also gets the bot's git identity (the worker had hit "Author identity
  unknown"). `TestTaskRepoAgentCommitsStillPushed` (red on old code).
  Image `sandbox-agent@f50fe10e`, three pins `8486b79`, rolled. Re-run
  → **PR 8**.
- **Then the reviewer bounced it anyway**: it clones `repo@ref` =
  `main`; `sessionEnded` (`parent/engine.go`) applies the transition
  but drops the session's pointer. Filed **`6bvk` (P1)** — needs the
  branch model decided (one branch per task vs per run + PR base).
  Also: review bounces are not `retries`, so the loop is unbounded (8
  sessions in 2 min before an `sap abort` parked it). The task sits at
  `needs-human` with PR 8.
- Broken windows (`b2e8e54`): `sap new PROJECT TITLE [-desc -goal -why
  -repo -ref -start]` (half of `xt40`); forgejo `bootstrap.md` token
  check reads `sap`, not `ai`.

## Loose threads

- **`6bvk`** is the blocker for any v2 task reaching `ready`: decide
  the branch model, carry `sess.Branch` into the next state's ref,
  bound the review loop.
- Another session's uncommitted Forgejo-migration edits (`z15k`:
  `k8s/apps/forgejo/{bootstrap.md,deployment.yaml,migrate-from-github.sh}`,
  `forgejo-runner/bootstrap.md`, `domain-model.md`) were in the
  worktree all along and were not committed here.
- `xt40`'s other half (token revocation) stays open.
- Carried: `bsj` (Longhorn backup) blocks real task-layer use; `pd5c`
  edges; `tmm5`, `88uq`, ntfy first page; `dsuj` GPU hand-off + CP
  question; RDP with sound from the laptop untried.

## Suggested next steps

- `6bvk`, then re-run `1d1c85ac` (`sap move 1d1c85ac37860842
  executing`) and watch the reviewer land it at `ready`.
- `bsj`.
