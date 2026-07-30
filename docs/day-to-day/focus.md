# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Dogfood/upkeep mode — mesh v2, the media user volume, and a
config-server foundation pass (ethsig + deviceflow packages, `888df71`)
are done. Nothing is mid-flight; next work is picked, not owed.

**Toward goal:** "Provisioning plane stays minimal" in
`desired-state/goals.md` — upkeep means keeping the hub small and
legible rather than growing it; the package extractions serve that by
making the seams (signature verification, device-flow grants) explicit.

**Out of scope:**
- Stage-2 extraction (machines package + neb* cluster, task 7bf3b809)
  — deferred until something touches that code anyway.
- h2c migration (task 8b42f959) — its own change with kmsprobe
  verification, not bundled work.
- Phone onboarding UX / TV client — unchanged deferrals (5183f6ea,
  2e1bef85).
