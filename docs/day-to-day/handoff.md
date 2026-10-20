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
- **Then the reviewer bounced it anyway**: it cloned `main`; the
  session's pointer was stored and never read; and bounces were
  unbounded (8 sessions in 2 min). **`6bvk` → decision `m3x4`, built
  the same day (`b8bc661`)**: one branch per task (`#task.branch` =
  `agent/<task-id>`; the child checks it out from the origin if an
  earlier session pushed it, PR base stays `ref`, 409 → the task's
  PR); the prompt names the branch and the PR; a backward agent move
  along one edge is bounded at `Retries`, then parked with the count.
  Images `sandbox-agent@d227fcd6` + `sap-actors@b8bc661`, pins
  `aa4a3b8`. **Re-run of `1d1c85ac` → PR 10 on
  `agent/1d1c85ac37860842` → reviewer approved → `ready`.** The first
  v2 task to make it through.
- Broken windows (`b2e8e54`): `sap new PROJECT TITLE [-desc -goal -why
  -repo -ref -start]` (half of `xt40`); forgejo `bootstrap.md` token
  check reads `sap`, not `ai`.

## Loose threads

- `agents/scratch` has 10 open PRs from the runs, 9 of them stale
  per-run branches — close/merge by hand when convenient. PR 10 is the
  real one; merging it is the `ready → done` move (flow v2's last
  persona confirms the merge).
- The child's `fetch` of the task branch treats any failure as "no
  such branch" and cuts a fresh one; a transient network error there
  ends in a loud non-fast-forward push, not silent loss.
- Another session's uncommitted Forgejo-migration edits (`z15k`:
  `k8s/apps/forgejo/{bootstrap.md,deployment.yaml,migrate-from-github.sh}`,
  `forgejo-runner/bootstrap.md`, `domain-model.md`) were in the
  worktree all along and were not committed here.
- `xt40`'s other half (token revocation) stays open.
- Carried: `bsj` (Longhorn backup) blocks real task-layer use; `pd5c`
  edges; `tmm5`, `88uq`, ntfy first page; `dsuj` GPU hand-off + CP
  question; RDP with sound from the laptop untried.

## Suggested next steps

- Merge PR 10 and move `1d1c85ac` to `done` — the full v2 loop,
  end to end; then a real (non-toy) task in `test`.
- `bsj` (Longhorn backup): flows, goals and now the task branches'
  PR links are state only the parent's store holds.
