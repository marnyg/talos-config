# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (tenth session) — **P2.4 landed and verified on a phone;
`594e` done. The TV has not moved.**

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
- **Verified on the owner's phone** (Sony XQ-BQ52, Android 13, driven
  over adb): enrolled as member `phone`/`media` through the in-app
  device flow; name map reads `gw` as `(gateway)` off `cav.facet`;
  split DNS correct (`hub→198.18.1.1`, `jellyfin.gw→198.18.1.2`,
  `example.com` to the underlay); Jellyfin 10.11.6 served at
  `jellyfin.gw:8096` in the browser **and** in the Jellyfin app.
  Paths (new `Conn.Paths()`, `ea27af4`): `*ip:10.0.0.67` on wifi,
  `*relay` on 5G with wifi off, HTTP 200 in 0.10 s — **both halves of
  the acceptance criteria's transport**, one `ConnectivityManager`
  callback apart.
- **`be0c1d7`**: the app's wallet sign-in died at siwe-oidc with
  `redirect_uri … :8096 … is not registered` (the raw `jellyfin`
  facet is a different origin from the `:80` ingress, and the plugin
  derives the URI from Host). The `:8096` origin is now registered;
  ArgoCD synced it and the same request answers 200.

## Loose threads

- **No media has actually been played.** The transport is verified on
  both paths, but the acceptance criteria's *media* (Direct Play,
  sustained bitrate) is not: the Jellyfin app is signed out.
- **Wallet sign-in cannot work inside an app webview** — SIWE needs an
  injected provider, and MetaMask's dapp browser would keep the
  session in its own cookie jar. Quick Connect (enabled server-side)
  is the bridge and was mid-flight when the session ended; the deeper
  fork is spike `i1il` (let the gateway's verified `X-Mesh-*` log the
  app in, Tailscale's `proxy-to-grafana` pattern) with `95la` deferred
  behind it.
- **The phone is v3-only now**: the CI-signed v1 app had to be
  uninstalled (different debug keystore), which took its nebula config
  with it. Its Jellyfin app points at `jellyfin.gw.mesh.internal:8096`.
- The APK is at `~/Downloads/talos-mesh-p24.apk`; `adb` comes from
  `nix shell nixpkgs#android-tools`.
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

- Finish the phone: Quick Connect the Jellyfin app in (code from the
  app, approved from a wallet-authenticated browser session), play
  something, read `paths` again under load.
- Then the TV — the same install + enroll, and it will hit the same
  sign-in wall (`95la`/`i1il`) and want its own registered origin.
- Only then cut `jellyfin.cp1` (ingress, siwe-oidc client list,
  jellyfin configmap comment) and the hub's `/hosts` + `/policy`.
- Then P2.5 (`359.9.5`): k8s/Talos endpoint off the mesh.
