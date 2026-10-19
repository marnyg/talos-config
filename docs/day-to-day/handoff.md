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
  .#actors-bin`. Image `87966ea` was building at session end — **pin
  it on `sap-parent`** (the pod runs `4799bf8`; children take the
  image the parent names, so the hardening is live only once pinned).

## Loose threads

- **`97rz` (P1): rotate the Forgejo agent token.** The leaked value
  sits in session `bce13a83`'s events and file; rotation is the
  sealed secret `k8s/apps/sap-provisioner/forgejo-agent.sealed.yaml`
  plus the bot's token on Forgejo.
- `ghcr-push.sh`'s stale-eval guard did not fire once today (built
  `4799bf8` again right after committing `87966ea` in the same shell
  line); a second run built the right rev. Watch the first log line.
- `sap` has no `new` (filing tasks is the UI's), no token revocation
  (TTL only, as the cookie), no laptop package beyond `actors-bin`.
- `pd5c`'s other edges: transition as a typed stream event/tool,
  `tools[]` per persona, per-project credential sets. A way to move an
  existing project to a newer flow version is missing.
- Carried: rotation never exercised live (`l3on`); `bsj` (Longhorn
  backup) blocks real task-layer use; `tmm5`, `88uq`, ntfy first page.

## Suggested next steps

- Pin `87966ea`, rotate the token (`97rz`), file a fresh task in a new
  project (flow v2) and watch the worker commit without pushing.
- `bsj`.
