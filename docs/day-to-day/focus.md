# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** No active build focus — mesh v2 and the post-phase-2 QoL list
are done, and the EPHEMERAL media-disk thread closed 2026-07-31 (media
lives on a 300GiB user volume that reinstalls preserve). The system is
in dogfood/upkeep mode; the library needs refilling.

**Toward goal:** "Git is the single source of truth" in
`desired-state/goals.md`-adjacent invariant 2 — the reinstall question
("what may a reinstall legitimately forget?") is now answered: derived
state rebuilds from git, the media volume is the one deliberate
exception, everything else is forgettable.

**Out of scope:**
- Phone onboarding UX (+later; recurs at 90-day cert renewal).
- TV mesh client (task 2e1bef85, deferred by decision 3dfef644).
- Auto-enroll for undeclared device names (thread a7920bda — needs an
  invariant-1 decision first).
