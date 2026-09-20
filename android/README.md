# Talos Mesh — Android TV/phone app

The mesh client for devices without a wallet: a Tailscale-style
two-screen app, and since Mesh v3 P2.4 a **member of the identity
plane** — the same runtime as the node agent and the desktop daemon
(`config-server/nodeagent` + `meshtun`), on a `VpnService`.

- **Enroll**: the app mints its NodeId on-device (the key never
  travels — ADR-0012), runs the headless device flow against the hub
  (`nodeagent.EnrollDevice`) and shows the QR + user code. Scan with
  the phone, sign the enrollment with the wallet at `/status`, and the
  app receives its Kit (member cert, beat grant, speak-as).
- **Connected**: the `VpnService` routes **only `198.18.0.0/15`** into
  the tun; names under `mesh.internal` that the plane's name map knows
  resolve to fake IPs (resolver `198.18.0.2`), and a TCP flow to
  `<fake IP>:<port>` is one stream to that member — `jellyfin.gw:8096`
  is the gateway's raw Jellyfin facet, `<svc>.gw:80` its ingress with
  the verified device identity in `X-Mesh-*`. Everything else on the
  device, including the app's own iroh UDP, stays on the underlay.
  Non-mesh DNS is forwarded to the underlay's resolvers through
  `protect()`ed sockets. Boot autostart once enrolled + consented.

One `VpnService` per device: connecting evicts any other VPN; the app
says so before asking for consent.

State lives in `filesDir/member/` with the node agent's layout (`key`,
`kit.json`, `bundle.json`, `hub.json`, `mark`); the key is the only
thing that is state.

## Debugging

The **Debug** button (enrolled screen) opens an introspection view:

- the member's status (`Tunnel.StatusJSON`): identity, beats, flow and
  DNS counters, the fake-IP table, the endpoint's advertised addresses,
  and the last fatal error of the member loop. `beats: 0` after a
  minute = the hub is sealed (every deploy, until the wallet unseals
  it) or unreachable, not misconfiguration.
- the tail of this session's Go log (`cacheDir/mesh.log`, truncated at
  each tunnel start — logcat swallows stderr, so Go logs to a file).
- **Test DNS**: resolves `hub.mesh.internal` and `example.com` through
  the system resolver — with the tunnel up that's the fake resolver,
  so it exercises the exact mesh-vs-underlay split real apps hit.

## Building

Two stages, **by hand on the NixOS builder** (`mar@nixos`) for now: the
AAR links `libiroh_ffi.a` cross-built for `aarch64-linux-android`
(`iroh-go/nix/android.nix`, impure/unfree, ~1 h cold, cached after),
which a stock CI runner cannot produce. `shell.nix` here carries the
SDK + NDK + gradle; the workflow is dispatch-only until the cross build
is cached for CI.

```sh
cd android
NIXPKGS_ALLOW_UNFREE=1 nix-shell --impure shell.nix
# 1. Go core → app/libs/mobile.aar (gomobile bind of config-server/mobile,
#    -tags iroh, iroh statically linked)
IROH_FFI_ANDROID_LIB=$(NIXPKGS_ALLOW_UNFREE=1 nix build --impure \
  -f ../iroh-go/nix/android.nix --print-out-paths)/lib ./build-aar.sh
# 2. APK (arm64 only)
gradle --no-daemon assembleDebug
# → app/build/outputs/apk/debug/app-debug.apk
# 3. publish to the rolling release (gh authenticated)
./publish.sh
```

Install on a Shield: enable Developer Mode + unknown sources, then
`adb install -r app-debug.apk` — or skip adb entirely: the rolling
release keeps the latest APK at
<https://github.com/marnyg/talos-config/releases/download/android-latest/talos-mesh.apk>
(e.g. the Downloader app). Sideloading through the Files app fails
silently on some phones after Play Protect; adb works.

## Signing

Debug-signed on purpose. Switching to a release keystore later means
every installed device must uninstall/reinstall (and therefore
re-enroll — cheap: one wallet signature each, same key and address if
the app's data survives; a full reinstall also regenerates the key,
which is likewise fine under ADR-0012).
