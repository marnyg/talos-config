# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-19, ADR-0043 Accepted; `cmd/sap`; the worker's credential hunt)

- **The first real task ran** (`kmlg`, credits topped up): `62a0ec24`
  went framed → needs-human (the framer asked three concrete
  questions) → framed → groomed → executing → needs-human (steered
  "give up") → reviewing → **ready with PR #7** on `agents/scratch`.
  ADR-0043 → **Accepted**; `kmlg` closed. `drou` closed (ADR-0041
  was done).
- **`cmd/sap`** (`hp79`, `4799bf8`, deployed `ef66863`): the parent
  from a shell — `login`, `me`, `tasks`/`task`/`move`,
  `sessions`/`session`, `watch` (SSE tail rendered like the UI, `-raw`
  one line per event), `file`, `steer`, `abort`; `-json` everywhere.
  Pure net/http, no iroh tag; `actors-bin` ships `bin/sap`. The gate:
  `Login.Token` is the cookie's value, `api()` takes it as
  `Authorization: Bearer`; `sap login` gets it via `/cli/authorize`
  (GET asks, same-origin POST redirects the token to the CLI's
  `127.0.0.1` port with its nonce). ADR-0037 amended. Verified live
  from the laptop.
- **Reading the run with `sap watch` found the defect** behind the
  six-minute executing session: flow v1's worker persona said "push,
  and open a PR", but the child does that after the run
  (`child/workspace.go`). The agent hunted for a credential and read
  `FORGEJO_TOKEN` out of **`/proc/1/environ`** — `os.Unsetenv` does
  not touch the kernel's copy — and echoed it into the transcript.
  `87966ea`: `cmd/child` goes non-dumpable (`prctl(PR_SET_DUMPABLE,
  0)`; same-uid, no-caps pi gets EACCES on `/proc/1/*`), and
  **DefaultFlow v2** tells the worker to commit only. Projects pin
  their flow version: `test` stays on v1.
- Gates green on both commits: `go test ./...`, `nix build
  .#actors-bin`. `87966ea` pinned on `sap-parent` (`2293ffe`; children
  take the image the parent names, so the hardening ships with it).

## Also this day (2026-10-19, separate session): w2 joined the cluster

- **The Windows gaming PC is Talos node `w2`** (spike `dsuj`, notes
  there): inventoried over ssh while it still ran Windows, declared
  from the measurements (`talos/hardware/asus-tuf-b550-pro.yaml`,
  `talos/machines/04-42-1a-ed-9d-38/`), first boot straight to Ready
  on `10.0.0.77`, p0agent enrolled, Longhorn holding both Corsair NVMes
  whole (tag `nvme`). Only hitch: the data disks kept their Windows
  GPT and needed `talosctl wipe disk` (gotchas.md).
- **w2 is powered off** (`talosctl shutdown`, ~20:40Z) so the owner
  can move it to another room. It rejoins by itself when cabled —
  nothing to do but keep the hub unsealed. The USB FE dongle it was
  reached through under Windows must stay unplugged.
- The three talos files landed inside an unrelated commit (`b7284dc`,
  another session's broken-windows sweep picked them up).

## Loose threads

- **`97rz` (P1): rotate the Forgejo agent token.** The leaked value
  sits in session `bce13a83`'s events and file; rotation is the
  sealed secret `k8s/apps/sap-provisioner/forgejo-agent.sealed.yaml`
  plus the bot's token on Forgejo.
- Broken windows fixed end of session (`d498a68`, deployed
  `d5e7405`): `PUT /api/projects/{id}/flow` + `sap project -flow/
  -version` (project `test` moved to v2 with it); `ghcr-push.sh` runs
  `nix eval --refresh` and reads HEAD before the eval (it had built a
  stale rev twice); the `cmd/child` scrub comment points at `hideProc`.
- `sap` has no `new` (filing tasks is the UI's), no token revocation
  (TTL only, as the cookie) — `xt40`; no laptop package beyond
  `actors-bin`.
- `pd5c`'s other edges: transition as a typed stream event/tool,
  `tools[]` per persona, per-project credential sets.
- Carried: rotation never exercised live (`l3on`); `bsj` (Longhorn
  backup) blocks real task-layer use; `tmm5`, `88uq`, ntfy first page.
- `dsuj` after w2: GPU passthrough (vfio bind `10de:2488`/`228b`,
  KubeVirt `permittedHostDevices`, win2k25 `hostDevices`) and the
  second-control-plane question are still open; w2 is a worker.

## Suggested next steps

- Rotate the token (`97rz`); file a fresh task in `test` (now flow
  v2) and `sap watch` the worker commit without pushing.
- `bsj`.
