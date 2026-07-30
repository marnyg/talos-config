# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 is complete and in dogfood mode; the post-phase-2
QoL/debt list is cleared (2026-07-30: split DNS, sealed-secrets 0.38.4,
overlay-route guard, template dedup). No active build focus — the one
open thread from the list is the EPHEMERAL media-disk question
(task be79fbb1), which needs a design decision before any code.

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — achieved in
its committed scope; current work is upkeep of that state. The
EPHEMERAL thread serves "git is the single source of truth" (what may
a reinstall legitimately forget?).

**Out of scope:**
- Phone onboarding UX (+later; recurs at 90-day cert renewal).
- TV mesh client (task 2e1bef85, deferred by decision 3dfef644).
- Auto-enroll for undeclared device names (thread a7920bda — needs an
  invariant-1 decision first).
