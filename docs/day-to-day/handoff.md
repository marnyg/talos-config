# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-17: **ssh to devices over the mesh** (`thz2`, ADR-0038
Accepted, **live**: Mac → `ssh mar@nixos.mesh.internal` works). Owner's framing "this is an ACL problem, not a kind" was
right about the authorization (recipe row = principal/group, consent
= where) and the ADR says so; the kind change is only vocabulary
bookkeeping. New receiver kind `device` `{ssh}` (port 22) in
`config-server/policy`, the Nickel contract, the recipe
(`{facet: ssh, group: admins}`); `irohup -forward ssh=127.0.0.1:22`
makes the daemon serve it; `-bridge` takes any facet with a kind.
Gates: `go test` policy/nodeagent/issuer/irohup, nickel `check.sh`,
`scripts/test-iroh.sh` all green. Hub deployed (`2a1c111`) + unsealed;
nixos repo `6b4e0f3` adds `forward.ssh` to both talos-mesh modules,
switched on the box (gen from committed lock) and the Mac (gen 121).
Found live: a member that starts serving reads as a node in peers'
directories for up to a beat → `6ac3122` makes `Agent.Target` re-beat
on a miss. **Not yet on the daemons**: both run `2a1c111`.

Earlier, 2026-10-16: **workouts tracker built end to end** (epic `yz75`, a
detour from `ivof`). Strong v6.2.5 on the phone was explored over
adb/uiautomator, then its REST backend (`back.strong.app`, JWT via
`/auth/login`, okhttp UA to pass the WAF) gave the structured export:
258 exercises with instructions + media URLs (108 videos, ~400
stills on `files.strong.app`, public), 8 templates, 72 logs — saved
**outside the repo** at `~/strong-export-2026-10-16/` (licensed
content; README there). New Go module `workouts/` (C-free, modernc
sqlite): `docs/desired-state/domain-model.md` (sub-scope), `store/`
(schema + derived records/PRs; Brzycki, rep-max rows = heaviest set
with ≥ n reps — numbers match Strong's), `strongimport/` (idempotent,
synthetic fixture), `web/` (server-rendered PWA, live logging screen
over a JSON API, SW + manifest, inline-SVG charts), `cmd/workouts`
(`serve` / `import`). Commits `d31b54a` → `7862442`, pushed.
`nix build .#workouts-bin` is the gate (added to pre-push + CI's
vendor-hash matrix). Image `ghcr.io/marnyg/workouts@sha256:15ea29f4`
pushed from the nixos builder; `k8s/apps/workouts/` synced by ArgoCD
(PVC bound, Ingress `workouts.gw.mesh.internal` gated `admins`).

## Loose threads

- Bump the nixos repo's `talos-config` input to `6ac3122` and rebuild
  both (box → Mac ssh needs it, or a box daemon restart). The Mac's
  **pending** nixpkgs update (uncommitted lock in `~/git/nixos`, plus a
  quint-language-server npmDepsHash bump) fails on atuin 18.23.0's
  e2e PTY test in the darwin sandbox — build from the committed lock.
- `m6xc` (presentations read one kind per member) filed, not decided.
- Phone as an ssh client needs an APK built from ≥ `2a1c111`.
- **Live and seeded** (`l8mc` closed): package flipped public, volume
  seeded through a busybox pod + a one-off import pod (scratch image
  has no shell, so `deployment.yaml`'s `kubectl cp`/`exec` recipe is
  wrong — rewrite it to the pod recipe in notes.md).
- The Strong password went through chat — rotate it.
- Template editor + metric pages are tested only by the Go walk-through
  (`web_test.go`); no hands-on run on the phone yet.
- Standing from before: `d0o0.1/.2/.3` + `e6pu.2.{1,2,3}` awaiting
  owner close; `whxz`, `e7ru`, `4mg`; 9 `<!-- stale? -->` flags.

## Suggested next steps

- Close `thz2` after box → Mac ssh is confirmed on `6ac3122`.
- Open `workouts.gw.mesh.internal` on the phone, add to home screen,
  log one real session; fix what the thumb finds. Fix the seeding
  recipe in `k8s/apps/workouts/deployment.yaml`.
- Decide whether "build in-house vs deploy wger" deserves an ADR (the
  choice was made in conversation 2026-10-16: Go+SQLite PWA, Strong
  API as the seed; no OSS tracker).
- Back to `ivof` (sandbox-agent live acceptance, parent actor).
