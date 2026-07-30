# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Dogfood/upkeep mode — the config-server foundation pass is
complete: ethsig, deviceflow, machines, and mesh packages extracted
(`888df71` → `6e17f3f`), h2c migrated. main is now wiring + handlers.
Nothing is mid-flight; next work is picked, not owed.

**Toward goal:** "Provisioning plane stays minimal" in
`desired-state/goals.md` — the extractions keep the hub small and
legible by making its seams (signature verification, device-flow
grants, machine composition, the overlay) explicit packages.

**Out of scope:**
- Further package-carving in config-server — the seams that mattered
  are cut; don't extract for its own sake.
- Phone onboarding UX / TV client — unchanged deferrals (5183f6ea,
  2e1bef85).
