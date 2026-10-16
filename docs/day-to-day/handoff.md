# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-17: **ssh to devices over the mesh** (`thz2`, ADR-0038
Proposed). Owner's framing "this is an ACL problem, not a kind" was
right about the authorization (recipe row = principal/group, consent
= where) and the ADR says so; the kind change is only vocabulary
bookkeeping. New receiver kind `device` `{ssh}` (port 22) in
`config-server/policy`, the Nickel contract, the recipe
(`{facet: ssh, group: admins}`); `irohup -forward ssh=127.0.0.1:22`
makes the daemon serve it; `-bridge` takes any facet with a kind.
Gates: `go test` policy/nodeagent/issuer/irohup, nickel `check.sh`,
`scripts/test-iroh.sh` all green. **Not yet deployed**: the hub must
redeploy + unseal to compile the row; the Mac plist and the NixOS
unit (outside the repo) need the `-forward` flag.

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

- `thz2` open until a live `ssh nixos.mesh.internal` from the Mac;
  `m6xc` (presentations read one kind per member) filed, not decided.
- **Pod is in `ErrImagePull`**: the ghcr package is private and
  container visibility has no API — flip it in the GitHub UI, then
  seed the volume (`l8mc`, commands in `deployment.yaml`'s header).
- The Strong password went through chat — rotate it.
- Template editor + metric pages are tested only by the Go walk-through
  (`web_test.go`); no hands-on run on the phone yet.
- Standing from before: `d0o0.1/.2/.3` + `e6pu.2.{1,2,3}` awaiting
  owner close; `whxz`, `e7ru`, `4mg`; 9 `<!-- stale? -->` flags.

## Suggested next steps

- ssh live run: add `-forward ssh=127.0.0.1:22` to the box's unit and
  the Mac's plist, `fly/deploy.sh` + unseal, wait one beat, `ssh
  nixos.mesh.internal`; then ADR-0038 → Accepted, close `thz2`.
- `l8mc`: make the package public → ArgoCD self-heals → `kubectl cp`
  the export → `workouts import` → open on the phone, add to home
  screen, log one real session; fix what the thumb finds.
- Decide whether "build in-house vs deploy wger" deserves an ADR (the
  choice was made in conversation 2026-10-16: Go+SQLite PWA, Strong
  API as the seed; no OSS tracker).
- Back to `ivof` (sandbox-agent live acceptance, parent actor).
