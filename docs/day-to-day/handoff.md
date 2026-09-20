# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (tenth session) — **P2.4 code landed; `594e` done. Device
side not yet exercised.**

- **The Android app is a member of the identity plane**, not a nebula
  client (`e0fb4b0`, `6802b35`): `config-server/mobile` binds
  `nodeagent` + `meshtun` on the `VpnService` fd, enrollment is the
  headless device flow through Go (QR + user code from the hub, wallet
  signs at `/status`), and the nebula surface — keygen/enroll/splice,
  the tunnel runner, the split-DNS shim — is deleted (decision `d3z3`).
  Kotlin routes only `198.18.0.0/15`, DNS `198.18.0.2`, and says what
  Android's one-VPN-slot rule costs before the consent dialog (`.1`).
  `NetworkChanged` from `ConnectivityManager` is our substitute for
  iroh-ffi's dead Android network monitor (`.2`).
- **`meshtun` is the shared presentation** (`c075081`): the zone rule,
  the port vocabulary and the connection pool moved out of
  `cmd/irohup` so the two presentations cannot drift; `fakeip` gained
  `NewFDLink` (gvisor fdbased, linux/android) and a mutable upstream
  list. That closes `phz`. irohup keeps only what is darwin about it.
- **APK v2 built on the NixOS box** (18 MB, arm64): `android/shell.nix`
  + `build-aar.sh -tags iroh` linking the cross-built
  `libiroh_ffi.a`. **The APK build left CI** — a stock runner cannot
  produce that `.a` (impure, unfree NDK, ~1 h cold); `android/
  publish.sh` uploads to the same rolling release, the workflow is
  dispatch-only.
- **Both nodes run p0agent 0.1.4** (worker `p0agent-014`, `acabeac`):
  `facets [apid kube-api]` in each agent's log, installer pinned in
  both hardware files, and the `≤ 0.1.3` clause is out of `KindOf`.

## Loose threads

- **Nothing has been installed or enrolled yet.** The APK is at
  `~/Downloads/talos-mesh-p24.apk`; `adb` comes from
  `nix shell nixpkgs#android-tools`. Until a device runs it, P2.4 is
  code, not a migration — the acceptance criteria (Jellyfin app +
  browser, LAN-direct *and* relay) are all unexercised.
- **`jellyfin.cp1` is still in k8s on purpose**: `k8s/apps/media/
  ingress.yaml`, the siwe-oidc client list, the jellyfin configmap's
  branding comment. Cutting it before the TV runs the new APK takes
  Jellyfin away from the TV; it is a one-commit follow-up after the
  device migrates.
- The hub still serves `/hosts` and `/policy` for nobody: the app was
  their last consumer. Deletable once the TV is off nebula.
- `config-server/mesh`'s `TestMeshHTTPOverOverlay` flaked once under a
  full parallel `go test ./...` (2 s HTTP deadline); passes alone,
  repeatedly. Pre-existing, not P2.4's.
- The spike copy `iroh-go/mobile` + `iroh-go/android-p0` are now
  superseded by `config-server/{fakeip,meshtun,mobile}` and could go.
- `0q0` blocked on capacity; `7c3`, `fgr`, `phz`(→done), `t7b2`
  unchanged.

## Suggested next steps

- Sideload + enroll a phone (owner offered one on USB), then the TV:
  `jellyfin.gw:8096` LAN-direct, then the same over the relay.
- Then cut `jellyfin.cp1` and re-check the SSO redirect list.
- Then P2.5 (`359.9.5`): k8s/Talos endpoint off the mesh.
