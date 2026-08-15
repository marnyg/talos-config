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

No DNS is pushed to the tun (Android would send *all* queries to it,
and the hub's resolver only answers the mesh zone) — services are
reached by overlay IP; the list shows name + IP.

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
`adb install app-debug.apk` (or any sideload channel).

## Signing

Debug-signed on purpose. Switching to a release keystore later means
every installed device must uninstall/reinstall (and therefore
re-enroll — cheap: one wallet signature each, same key and address if
the app's data survives; a full reinstall also regenerates the key,
which is likewise fine under ADR-0012).
