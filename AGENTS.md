## Repository layout

Monorepo around the **sovereign-actor protocol** (decision
`talos-config-5w1`, ADR-0020):

- `protocol/` — the protocol as its own Go module, with no dependency on
  config-server, Talos, fly, or nebula. Docs sub-scope at
  `protocol/docs/` (its own `desired-state/`, ADRs from 0001, the design
  sketch `sovereign-actor-protocol.md`).
- `config-server/`, `talos/`, `k8s/` — the talos deployment, the
  protocol's first (N=1) consumer; root `docs/` is authoritative for it.

When working under `protocol/`, read the root
`docs/desired-state/{goals,invariants,domain-model}.md` **and**
`protocol/docs/desired-state/` (see the traversal rule below).

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
