# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** the Android mesh app v1 is live (built, released, verified on
the owner's TV 2026-08-15). Remaining app scope is deployment to the
actual remote TV (parents') and the Jellyfin-addressing decision
(IP:port vs ingress name). Storage arc still queued behind reviving w1.

**Toward goal:** closes the last open slice of "Mesh v2 — phones/TV
join the network" in `desired-state/goals.md`: TV client existed only
as a deferred task; the remote-TV use case fired its build-gate.

**Out of scope:**
- Release-keystore signing, APK size trimming, app polish — v1 is
  sideload-and-works; iterate when a real need appears.
- Mesh DNS on Android / hub resolver forwarding — reach services by IP
  until the Jellyfin-addressing decision forces the question.
- Any storage migration while `longhorn-bulk` is faulted (w1 down).
