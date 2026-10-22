# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## 2026-10-22: browser RDP shipped — Guacamole in front of win2k25 (`ey3a`)

- **`k8s/apps/guacamole` is live and Synced** (`platform-guacamole`,
  revision `9c33e30`): guacd + webapp, both rootless, `restricted`
  PSS, Ingress `guac.gw.mesh.internal` behind the default admins gate.
  guacd dials the ClusterIP the VM already has, so the raw `rdp` facet
  and `xfreerdp` keep working; no mesh-policy row was needed.
- **`http://`, not `https://` — do not "fix" this**: ADR-0033's `:443`
  device front (meshtls.Front) has no hijack/Upgrade path, so a `wss`
  tunnel dies there. Over `:80` the Upgrade rides ingress-http exactly
  as Vaultwarden's websocket does. Sessions are capped at 1 h by the
  gateway's `ConnMaxAge`, not by nginx.
- **Verified live**: pods Ready, `user-mapping.xml` actually read (the
  entrypoint symlinks `/etc/guacamole` into a throwaway
  `/tmp/guacamole-home.*`), placeholder login returns a token,
  `/guacamole/` 200 behind the gate's 401, guacd → `:3389` open.
  **Not verified: the ws tunnel end to end, NLA to Server 2025, audio
  in the browser** — the first real session is the test.
- **Your two steps**: reseal `sealed-secret.yaml` with the real guest
  creds (`k8s/apps/guacamole/bootstrap.md`), then delete the pod
  (subPath never refreshes), then open it from the phone.
- **Next in line** (`f0jl`, blocked on `ey3a`): `vmpower` — a
  start/stop page + idle auto-stop keyed off Guacamole's
  `activeConnections`, which first needs a decision record: it makes
  git not the whole truth for `/spec/runStrategy` (`ignoreDifferences`
  on `platform-vms`).

## Also 2026-10-20 (separate session): app state encrypted + backed up to Tigris (ADR-0044; `9io`, `bsj`)

- **`longhorn-encrypted` is the default class**: dm-crypt under one
  cluster passphrase (sealed `longhorn-system/longhorn-crypto`; age
  copy `talos/clusters/homelab/longhorn-crypto-secrets.yaml.age`),
  `Retain`, auto-joins recurring-job group `backup`. **20 volumes
  migrated** with `scripts/encrypt-pvc.sh` (copy once, keep the claim
  name, old PV kept `Retain` + label `encrypt-pvc/old=2026-10-20`).
  Plain `longhorn` stays for VM disks/ISOs, kps/loki/alertmanager/
  grafana — and **`gateway-state`, not done**: the gateway fronts
  `cp1.mesh.internal:6443`, so scaling it to 0 cut kubectl (restored
  over LAN in ~2 min, nothing changed). Needs a LAN-only run.
- **Backups**: Tigris bucket `talos-longhorn-backup` (fly, personal),
  sealed creds, `backup-nightly` 03:45 retain 14 on group `backup`.
  `transfer`'s first backup **completed** (19 GB). The first nightly
  run covers all 20.
- **`longhorn-user` folded into the default** earlier the same session
  (decision `pp9h`); `transfer` got Prune/Delete=false + daily
  snapshots (`transfer-daily-snapshot`, retain 7).
- **Collateral**: `fly storage create` attached the bucket to the hub
  app and rolled it → **hub sealed, needs your unseal** (fly secrets
  unset again). ApplicationSet now `ignoreApplicationDifferences` on
  `automated` (hand-pause sticks). Alloy drops the CSI line that logs
  `CRYPTO_KEY_VALUE`. `origin` is Forgejo → GitHub lags a push-mirror
  hop; Argo reads GitHub.
- **Backup health** (same session): ntfy alerts (`longhorn-backups`
  PrometheusRule: target down, backup Error, >30 h stale, metrics
  missing, restore test >8 d) over ksm custom-resource metrics; weekly
  restore test `k8s/apps/backup-verify` (Sun 05:45: restore every
  volume from Tigris into a scratch encrypted PVC, hash every file).
  First manual run of backup-nightly put all 20 volumes in Tigris —
  after fixing the skip of detached volumes (libation-db).
- Restore test **green** (transfer bit-identical to live; 4 more volumes clean).
- **Next**: delete the old plaintext PVs
  (`kubectl get pv -l encrypt-pvc/old`); gateway-state over LAN.

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
  question; RDP with sound from the laptop untried (Guacamole's
  in-browser audio is the cheapest way to test it).
- `ey3a`: NLA and the ws tunnel unproven; `vmpower` (`f0jl`) needs the
  `runStrategy`/selfHeal decision recorded before code.

## Suggested next steps

- Merge PR 10 and move `1d1c85ac` to `done` — the full v2 loop,
  end to end; then a real (non-toy) task in `test`.
- `bsj` (Longhorn backup): flows, goals and now the task branches'
  PR links are state only the parent's store holds.
