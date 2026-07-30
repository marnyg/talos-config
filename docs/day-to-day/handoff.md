# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-31 — **stage-2 extraction complete** (`6e17f3f`; task 7bf3b809).
Continues the foundation pass across an interrupted session; no behavior
change, verified with go vet, staticcheck, deadcode -test, full tests,
and `nix build`.

- The interrupted session landed the h2c migration (`e512047`,
  `http.Server.Protocols` replaces the deprecated x/net h2c handler)
  and the `machines/` package (`6be908d`, part 1).
- This session lifted the ~1900-line neb* cluster into `mesh/` (part 2):
  `mesh.Manager` + exported config renderers; `DeviceConfig` moved out
  of nebenroll.go; enrollment/TV/unseal HTTP handlers stay in main.
  `Manager.Start` is the exported test seam (the `deviceflow.Store.Now`
  pattern). Filenames keep the neb* prefix to minimize churn.

## Loose threads

- **h2c migration landed but task 8b42f959 still open** — it was scoped
  to include kmsprobe verification against a deployed hub; the code is
  in `e512047`, the post-deploy probe hasn't been run. Close only after
  a deploy + `kmsprobe` pass.
- **ADR-0008 still Proposed** (media volume + invariant-2 amendment) —
  review → Accepted.
- **Task 7bf3b809 (stage-2 extraction) is done** — awaiting the user's
  confirmation to `task done` it.
- Media library still empty; `argocd-dex-server` Error pod still
  unowned; cached `~/.config/talos-mesh/laptop.yml` still pre-phase-2
  (`nebup -reenroll`).

## Suggested next steps

- Deploy, run kmsprobe, close 8b42f959.
- Review ADR-0008 (Proposed → Accepted).
- Refill the media library (a later reinstall then exercises u-media
  re-adoption for real).
