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
