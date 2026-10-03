# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (late): **beads groomed and the owner's `todo` file turned
into issues; no code changed.**

- **Grooming.** Closed nine issues whose premise had moved: `0q0`
  (bulk tier already 2 replicas), `6z9` (Nickel contracts shipped and
  in CI), `owh`, `98d`, `cjo`, `ap2` (nebula-era), `90a` (dup of the
  new `9z4e`), epic `18h` (ADR-0029 was its goal; `9io`/`bsj` stand
  alone), `0bc.2.7`. Epics `359` and `0bc` dropped P1 → P2 — both
  goals are reached; what is left is field items and M5. `4mg`
  (one-wallet-interaction unseal) rose to P2: its revisit trigger
  ("when nebula goes") has fired. 68 → 60 open.
- **New issues from the `todo` file** (deleted): `hwtp` seerr, `lwi3`
  sillytavern off the Windows PC, `4iob` retire the old docker host,
  spikes `dsuj` (Windows PC as a Talos node: GPU passthrough VM ↔ AI
  pods, second control plane), `ch74` (SMB for the VM; share vs
  sync-flow), `9z4e` (HTTPS over the mesh via protocol identities),
  `kanr` (agentic workloads: kagent / google-ax).
- **Stale nebula/wg0 text removed** (commit on `main`): `CLAUDE.md`'s
  WireGuard + nebula sections replaced by a Mesh v3 pointer; four k8s
  manifests stopped citing `10.42/16` as the live reason for the
  siwe-oidc ClusterIP pin.

## Loose threads

- **`vzbf`** (gateway WebSocket-after-Close panic) still
  `in_progress`: image `21badd6` rolled 2026-10-03 ~22:20; pod at 0
  restarts ~2 h in. Close after a day of SignalR traffic.
- The `storage` row logs only on a warn *change*; a healthy first
  poll is silent in `fly logs`. Expect `degraded` during a disk
  rebuild — that is the row working, not a fault.
- Old docker host: media containers stopped, not removed; its DBs are
  behind the cluster's (do not re-import). Now `4iob`.
- `dsuj` is blocked by `lwi3` (sillytavern lives on that PC). The
  KubeVirt/CDI/Windows-guest groundwork from August is already in
  `k8s/apps/{kubevirt,cdi,vms}` — noted on the bead.

## Suggested next steps

- Close `vzbf` once `kubectl -n gateway get pods` shows 0 restarts
  over a day.
- `hwtp` (seerr) is the smallest concrete item and the first new
  consumer of the storage tiers.
- `lwi3` unblocks the Windows-node spike `dsuj`.
