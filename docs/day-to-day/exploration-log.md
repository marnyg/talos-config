# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## TV mesh client (2026-07-30)

- 2026-07-30 — Considered the official Mobile Nebula app as the Android
  TV client. Ruled out: Flutter buttons ignore d-pad clicks on Google
  TV (DefinedNet/mobile_nebula#148), no camera for the QR import path,
  and Play won't list it on TV (no leanback intent). A BT-mouse
  sideload workaround exists but is not an acceptable steady-state UX.
  Landed on: home TV stays off the mesh (LAN-direct Jellyfin, decision
  3dfef644); a thin Kotlin/leanback APK bundling the nebula gomobile
  AAR is the design if a remote-TV need ever appears (task 2e1bef85).
  Phone path unaffected — verified in source that the app imports
  externally-derived private keys (`add_certificate_screen.dart`).
