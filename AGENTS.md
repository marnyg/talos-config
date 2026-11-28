## Repository layout

The talos deployment — hub (`config-server/`), cluster (`talos/`,
`k8s/`), and the personal apps riding it (`workouts/`, `tasks/`,
`vmpower/`, `android/`); root `docs/` is authoritative for it.

The **sovereign-actor protocol** it is built on lives in its own repo
since 2026-11-28 (ADR-0052, superseding the monorepo ADR-0020):
[github.com/marnyg/sap](https://github.com/marnyg/sap) — `protocol/`,
`iroh-go/`, `iroh-transport/`, `actors/` (incl. the network's fly app
`actors/net`), their images and docs. This repo consumes it:

- `config-server/go.mod` requires `github.com/marnyg/sap/{protocol,
  iroh-transport,iroh-go}` by tag; the flake input `sap` supplies
  libiroh_ffi and iroh-relay (`nix flake update sap` to move the pin).
  A protocol change the hub needs is a sap release, then a version
  bump here (+ `vendorHash`). For working on both: a gitignored
  `go.work` in `config-server/` pointing at a local sap checkout.
- `k8s/apps/sap-*` and `k8s/apps/sandbox/agent.yaml` pin sap's images
  by digest; sap's build scripts print the lines to paste.

Before changing how the hub uses the protocol, read sap's
`docs/desired-state/`.

## Git hooks

`git config core.hooksPath .githooks` once per clone. `.githooks/pre-push`
refuses a push whose range touches a module's `go.mod`/`go.sum` or
`nix/default.nix` while its Go `vendorHash` is stale (the fixed-output cache hides this locally; CI's
`vendor-hash` job only goes red after the push). The other hooks there
just delegate to beads' shims in `.beads/hooks/`, so `bd hooks` keep
working. `SKIP_VENDOR_HASH=1 git push` bypasses the check.

**Commits are ssh-signed** by the owner key (`talos-config-lehx`): the
hub serves `talos/` from the git tip and accepts only a tip whose
signature verifies against `talos/allowed-signers`. Once per clone:

    git config gpg.format ssh
    git config user.signingkey ~/.ssh/id_ed25519.pub
    git config commit.gpgsign true
    git config gpg.ssh.allowedSignersFile "$(git rev-parse --show-toplevel)/talos/allowed-signers"

The pre-push hook refuses an unsigned tip (`SKIP_SIGNED=1` bypasses).
No GitHub-UI merges: those are signed by GitHub's key, not the owner's.

## Quality gate: the iroh-tagged tests

`go test ./...` under `config-server/` is **C-free and does not gate the
hub**: the iroh binding sits behind build tag `iroh`, so the tagged
suite can be red while the untagged one is green. Only
`nix build .#config-server-bin` (and so the hub image, and so a deploy)
ran it — which is how `main` went undeployable for three commits and a
broken node beat was found by a live provisioning run
(`talos-config-ydq0`, 2026-09-22).

**After touching `config-server/` (or bumping its sap versions), run
`scripts/test-iroh.sh` before pushing.** It is the nix build's test
phase without the sandbox (~40 s warm). The protocol's own tagged
suites (`iroh-transport`, `actors-bin`) are sap's nix builds now. This
is separate from the pre-push `vendorHash` check — same module,
different failure.

<!-- docs-skill:start -->
## Documentation contract

This repo uses a structured docs layout (see `docs/README.md`). Follow these rules in every session:

1. **At session start**, read the closest `docs/desired-state/` to the work you're about to do:
   - Always read repo-root `docs/desired-state/{goals,invariants,domain-model}.md`.
   - If you're working under a sub-scope with its own `docs/desired-state/` (e.g. `services/X/docs/desired-state/`), read that too.
   - Follow links from parent `docs/README.md` into sub-scopes whose code you'll touch. Skip the rest.

2. **Also read** `docs/day-to-day/handoff.md` and `docs/day-to-day/focus.md` at session start to pick up cross-session context.

3. **Invariants are hard constraints.** If a change would violate one in any `desired-state/invariants.md`, stop and escalate to the user — do not silently resolve. Either the invariant is wrong (update it) or the change is wrong (revisit).

4. **At the end of meaningful work** (feature done, milestone hit, before PR, or when the user signals end-of-session), invoke `/skill:docs-update` to refresh `day-to-day/` files and prompt for ADRs.

5. **Budget:** keep each scope's `goals.md` + `invariants.md` under ~300 lines total; `domain-model.md` is exempt — as expressive as the domain requires, and it may split into `domain-model/<area>.md` files (root file = summary + diagram + glossary + links; follow links only into areas your work touches). Traverse at most ~3 scopes deep per session. If you're hitting the budget, propose pruning.
<!-- docs-skill:end -->

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
