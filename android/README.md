# Talos Mesh — Android TV/phone app

The mesh client for devices without a wallet (task `2e1bef85`): a
Tailscale-style two-screen app.

- **Enroll**: the app generates its keypair on-device (the private key
  never travels — ADR-0012), starts an RFC 8628 flow against the hub,
  and shows a QR + user code. Scan with the phone, sign the enrollment
  with the wallet at `/status`, and the app fetches its config and
  splices the key in locally.
- **Connected**: a `VpnService` runs nebula (via the gomobile AAR) on
  the tun fd; the host list comes from the hub's mesh-only
  `GET /hosts`. Boot autostart once enrolled + consented.

DNS is split on-device (`config-server/mobile/dnsshim.go`): the tun
advertises a magic resolver (mesh base + 53); mesh-zone queries ride
the tunnel to the hub's resolver, everything else is forwarded on the
underlay via protected sockets — a sealed hub only costs mesh names,
never general DNS.

## Debugging

The **Debug** button (enrolled screen) opens an introspection view:

- the split-DNS shim's live state (`Tunnel.DebugJSON`): magic/hub
  addressing, current underlay upstreams, counters, and the last 64
  per-query decisions (mesh / hub-reply / underlay, upstream, RTT).
  A growing `meshQueries` vs `hubReplies` gap = the hub's resolver
  isn't answering (sealed hub or tunnel down), not misclassification.
- the tail of this session's nebula log (`cacheDir/nebula.log`,
  truncated at each tunnel start — logcat swallows stderr, so the Go
  side logs to a file).
- **Test DNS**: resolves `hub.mesh.internal` and `example.com` through
  the system resolver — with the tunnel up that's the tun's magic
  resolver, so it exercises the exact mesh-vs-underlay split real apps
  hit, and both lookups then appear in the event ring.

## Building

Two stages; CI (`.github/workflows/android-apk.yml`) runs both and
publishes the debug-signed APK as an artifact.

```sh
# 1. Go core → app/libs/mobile.aar (needs Android SDK + NDK;
#    set ANDROID_HOME / ANDROID_NDK_HOME)
./build-aar.sh

# 2. APK (any recent Gradle; no wrapper is checked in)
cd android && gradle assembleDebug
# → app/build/outputs/apk/debug/app-debug.apk
```

Install on a Shield: enable Developer Mode + unknown sources, then
`adb install app-debug.apk` — or skip adb entirely: every push publishes
the APK to the rolling release, so the TV can fetch
<https://github.com/marnyg/talos-config/releases/download/android-latest/talos-mesh.apk>
directly (e.g. the Downloader app).

## Signing

Debug-signed on purpose. Switching to a release keystore later means
every installed device must uninstall/reinstall (and therefore
re-enroll — cheap: one wallet signature each, same key and address if
the app's data survives; a full reinstall also regenerates the key,
which is likewise fine under ADR-0012).
