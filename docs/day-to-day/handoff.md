# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-17 evening → 2026-10-18)

**The git half of the child is live** (`e6pu.3`, closes `e6pu`):
`actors/child/workspace.go` — `#task {prompt, repo, ref}` clones from
the forge under the workspace dir, pi runs in the clone, and after
settle (on every status) the child commits, plain-pushes
`agent/<task-id>` and opens the PR with pi's final message as body;
`child_settled` carries `{repo, branch, head, pr}`. The token reaches
git as `http.<url>.extraHeader` through the subprocess env only, and
`cmd/child` scrubs `FORGEJO_*` before pi starts (verified live: pi
reports `FORGEJO_TOKEN` unset). `spawn -repo/-ref`. `forgejo-agent`
resealed for `sap` and on the provisioner's `-outfit`; the `ai` copy,
the shell entrypoint's `AGENT_REPO` path, `/run/agent` tmpfs and
`agent-run.sh -r/-b` are gone. Live: **PR #2 on `agents/scratch`**
from a protocol child (image `sandbox-agent:b8d542b`).


**The agent child is live under kata** (`e6pu`, ADR-0037's "ship the
child in the image"): `k8s/apps/sandbox/image.nix` now carries
`/usr/local/bin/child` (from `actors.static`); with `SAP_INTRO` set the
entrypoint execs it (`SAP_PI_ARGS=--provider,openrouter,--model,…`,
workspace = HOME), everything else in the entrypoint is the plain
agent-run path as before. `/workspace` and `/tmp` are 1777 in the
image because a driver-started Job has no volumes. `sap-provisioner`
runs `c882727` with `-runtime-class=kata` and
`-outfit <sandbox-agent digest>=openrouter`; `openrouter` is resealed
for ns `sap` (`k8s/apps/sap-provisioner/openrouter.sealed.yaml`).
**Acceptance passed** from the Mac (`spawn -prompt`): `#lookup` →
`#spawn` → born in ~110 s (image pull dominates) → `#ping` → `#task` →
45 events up `agent-events` → `child_settled settled`, session JSONL
9 KB in `-session-dir`. Recipe in notes.md.

**The 5gz relay gate is observe-only** (`82d496c`, hub deployed): the
first provisioner reconnect after the gate went warm was denied —
provisioner, lighthouse, laptop parent and every child hold protocol
keys, not member certs, and the hub is relay-only (ADR-0022), so the
whole sovereign-actor plane was locked out. `relayAdmitter.gate=false`
computes the verdict and admits anyway; `/status` says `observing (N
members; non-member admitted)` after such a connect. Spike `qx5c`
owns the real answer. **The hub is SEALED since that deploy** — sign
the two proposals at `/status`.

Earlier in the session: 14 stale in-progress beads closed
(observability, workouts, pins, ssh, ABS, Attic); `3bt9` filed for the
nix.conf remainder. Wave 2 (relay gate etc.) is in the previous
handoff's chronology, now absorbed.

## Loose threads

- `d0o0` stays in progress: the durable `talos/pins/<address>.json`
  copy is its remaining scope.
- The outfit is keyed on the sandbox-agent **digest**: every rebuild of
  that image means re-pinning `-outfit` in the provisioner Deployment
  as well as `agent.yaml` (two places, by hand).
- `agent-run.sh` is now git-less by design; a repo task is `spawn
  -prompt … -repo owner/name`. If a plain-Job git run is ever wanted
  again, it is the child's code, not the shell's.
- PR title = the prompt's first line (≤ 200 chars) — ugly for long
  prompts; a `title` on `#task` or the parent's store naming it is the
  fix when the parent exists.
- Birth cost is the image pull (~450 MB, ~110 s on this cluster; a
  second tag of the same layers pulled again). `-window 4m` covers it;
  the 3 m default does not on a cold node.
- **Swarm 2026-10-18 (four autonomous leaves, all merged+closed):**
  `btmw` workouts lb display + plate calculator (`72db3f9`), `p5g`
  iroh-transport home relay without QAD — `Online` 3.0 s → 0.2 s
  (`8512f58`, vendorHash bump `3971e73`), `q74h` workouts offline
  write queue in `sw.js` v2 (`eb80cff`), `djdl` `authorize.qnt` with
  `res` as the pinned receiver and a raw `pinnedAs` edge in the five
  consent laws (`49e7f62`). Reports in `/tmp/swarm/<name>.md`.
  Watch: the offline queue is **not verified live** (no browser run;
  first dead-spot gym session is the test); `plates` is kg-only even
  when `weight_unit=lb`; `authorize.qnt` depth-2 verify is 333 s —
  still over the 300 s budget (nightly tier); three negative bridge
  laws were dropped as redundant (p1/p3/p4 die on all five restated
  laws — see the report's mutation table).

## Suggested next steps

- Sign the hub's proposals (sealed since the `82d496c` deploy).
- The parent actor (SQLite+FTS5 store, list/watch/steer UI behind the
  person gate) — `cmd/spawn -prompt` is the stand-in to replace; the
  pointer shape it stores is now settled.
- `qx5c`: decide how sovereign actors get admitted to the relay before
  the gate is enforced again.
