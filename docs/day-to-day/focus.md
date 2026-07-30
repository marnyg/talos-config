# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Mesh v2 **phase 2 entry**. Phase 1's exit checks all passed
2026-07-30 (node reboot, hub re-seal ×2, roaming — see
`mesh-v2-nebula.md`). The single remaining gate is criterion 4's
**phone half**: measure Mobile Nebula phone UX or formally drop driver
2. The TV half is decided (3dfef644: LAN-direct at home, APK deferred).

**Toward goal:** "Mesh v2" in `desired-state/goals.md` — LAN-direct
peer paths, one overlay, one derivation tree. Phase 2 (certSANs →
endpoint to mesh IP → strip wg0) also closes invariant 5's dual-overlay
exception and retires the DHCP-lease-drift failure class (thread 24).

**Out of scope:**
- Building the TV APK (task 2e1bef85, only on a real remote-TV need).
- Chasing direct remote paths — relay is parity by ADR-0006; revisit
  only if native IPv6 lands (task 43).
- App SSO / OIDC; ENS commitment layer (+idea).
