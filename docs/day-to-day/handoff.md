# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-29 (late evening) — **Criterion 2 remote case measured: the
CGNAT-hotspot pair RELAYS. Verdict deferred pending one discriminating
data point.**

- cp1 re-applied (`84d7ca5`): the `media` firewall rule (tcp/30096) is
  now live on the node — five rules confirmed in `ext-nebula` logs,
  applied without reboot, mesh reconverged direct. TV-path node blocker
  cleared.
- Criterion 2 remote (`d84026e`): laptop on phone hotspot ↔ home
  router **does not punch** — handshake `(relayed)`, packet capture
  shows all overlay traffic laptop↔fly, zero to the home WAN. Config
  exonerated (punchy on both sides, rendezvous unconditional). Full
  detail in `technical/guides/deployment.md`.
- **Two earlier runs were invalid**: the wg tunnel (interface
  `talos-laptop`, *not* `wg0`) was up, and nebula used it as underlay —
  any measurement with wg up is poisoned.
- Toolchain pin committed (`55a116a`): go 1.26 in Dockerfile matches
  go.mod and `buildGo126Module`; all three move together.

## Loose threads

- **Decision pending — criterion 2.** User chose "one more data point":
  a punch test from office Wi-Fi (non-cellular foreign NAT), filed as a
  `+next` task. Direct there ⇒ only cellular clients relay (amend
  criterion, likely ADR). Relay there ⇒ home NAT is symmetric and the
  criterion fires as written (keep wg0, LAN shortcut). Either outcome
  probably wants an ADR against ADR-0002.
- **Laptop state:** `talos-laptop` (wg) was deleted for the test and the
  test nebula killed — run `wgup` to restore the admin path, `nebup` to
  rejoin the mesh.
- **Unfiled:** the dual-overlay underlay-poisoning finding — proposed as
  a `+bug` task twice, no decision yet.
- Revocation policy (thread dc04e3e8) still gates the TV build (task 36).
- certSANs still absent (phase 2 step 1) — talosctl stays on wg.

## Suggested next steps

- Office punch test (the `+next` task has the exact procedure — wg DOWN
  first). Then decide: fire criterion 2, or amend it and draft the ADR.
- Dogfood continues regardless — LAN use is unaffected and measured
  good.
- If continuing after the verdict: settle revocation (dc04e3e8), then
  the TV path (task 36).
