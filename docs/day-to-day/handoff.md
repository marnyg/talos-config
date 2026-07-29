# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-29 (evening) — **Mesh v2 phase 1: node side deployed and
verified; device enrollment written, not yet deployed.**

- Deployed: cp1 on schematic `011ccccd…`, `ext-nebula` Running,
  `nebula0` at `10.42.218.125/16`. Handshake confirmed both directions,
  one CA (`b881d6ff…`) on both sides, node WAN endpoint visible to the
  hub — so the cert/mesh-DNS agreement property holds in the running
  system, not just in tests. Details in
  `technical/guides/deployment.md`.
- **Three groups, not two** (`f7f2598`): `machines`, `admins`, and a new
  `media` for shared-space appliances, which get Jellyfin's NodePort and
  nothing else. The group is signed into the cert, so the hub decides it
  and a name declared in both lists is a startup error.
- `/mesh/enroll` + `nebup` (`f7f2598`, `353f30e`): wallet-signed
  single-use challenge → self-contained config (inline PEM). The signing
  flow moved out of `wgup` into package `walletsign`, since that is the
  part that survives wg0's deletion.
- Verified with stock `nebula-cert`: laptop → `[admins]`, androidtv →
  `[media]`, 90-day windows, addresses matching the derivation.

## Loose threads

- **Uncommitted `Dockerfile` change** (`golang:1.25`→`1.26`), apparently
  from making the deploy work. Three places pin the toolchain — `go.mod`,
  `buildGo126Module` in `flake.nix`, the Dockerfile tag — and they must
  move together.
- **Nothing enrolled yet**: the laptop is not on the mesh until a
  `fly deploy` picks up `/mesh/enroll`, then `nebup`. Until a second
  member exists there is nothing to measure, so the dogfood clock has not
  started.
- certSANs still deliberately absent (phase 2 step 1) — `talosctl` stays
  on wg0.
- TV onboarding via device-flow + QR is designed and filed (task 38); the
  unverified part is Mobile Nebula's import UX on Android TV.
- Machine certs: 5 years, re-minted only on a config serve (thread
  dc04e3e8). Device certs: 90 days, renewed by re-running `nebup`.

## Suggested next steps

- `fly deploy`, wallet unseal, then `nebup` on the laptop; confirm a
  direct (not relayed) path to cp1 and start the ≥1 week dogfood.
- Measure what the kill criteria actually ask for: direct-vs-relay rate
  from nebula logs, and throughput against the ~80 Mbps floor.
- Then decide the TV path (task 38) and revocation policy (dc04e3e8)
  before any shared-space device enrolls.
