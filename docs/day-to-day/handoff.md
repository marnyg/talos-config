# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-29 (evening) — **Mesh v2 phase 1: node and device sides both
deployed and verified. Three of four kill criteria cleared.**

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

- Measured (see `technical/guides/deployment.md`): direct LAN path at
  1.8ms with 0% loss (criterion 2, LAN), and 229/168 Mbit/s over the mesh
  against a 326/182 LAN baseline — 2–3× the 4K-remux floor, so criterion
  3 passes.

## Loose threads

- **Uncommitted `Dockerfile` change** (`golang:1.25`→`1.26`), apparently
  from making the deploy work. Three places pin the toolchain — `go.mod`,
  `buildGo126Module` in `flake.nix`, the Dockerfile tag — and they must
  move together.
- **The one remaining design risk is criterion 2's remote case**: laptop
  on a CGNAT hotspot against the home router. LAN punching proves little
  about hard NAT, and if that pair falls back to relay the whole plan's
  first driver evaporates. One `ping` decides it — under ~20ms is direct,
  because fly is 20ms away.
- cp1's stored config predates the `media` firewall rule, so it needs a
  re-apply before any TV can reach Jellyfin over the mesh.
- certSANs still deliberately absent (phase 2 step 1) — `talosctl` stays
  on wg0.
- TV onboarding via device-flow + QR is designed and filed (task 38); the
  unverified part is Mobile Nebula's import UX on Android TV.
- Machine certs: 5 years, re-minted only on a config serve (thread
  dc04e3e8). Device certs: 90 days, renewed by re-running `nebup`.

## Suggested next steps

- Run criterion 2's remote test from a phone hotspot. It is the last
  gate that could still send the whole plan back to "keep wg0".
- Start the ≥1 week dogfood — ordinary use, watching for relay fallback.
- Settle the revocation policy (dc04e3e8) before building the TV path
  (thread 37), since a shared-space cert is exactly what revocation is
  for. Phase 2 (uuid 1afafb50) stays closed until the criteria clear;
  invariant 5's dual-overlay exception is what a stalled phase 2 costs.
