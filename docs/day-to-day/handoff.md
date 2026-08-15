# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-15 — **Android TV/phone mesh app: built, shipped, owner-verified
on a real TV in one session** (task `2e1bef85`; build-gate fired: remote
TV at the parents' needs Jellyfin over the mesh).

- Server half (`fd82497`): mesh-only `GET /hosts` — the device-list API,
  same zone mesh DNS serves, JSON + liveness. Firewall now admits
  group=media on tcp/80; per-route gates keep `/config` admins-only.
- Go core (`a1cf0c5`): `devkey` extracted from nebup (shared keygen +
  key-splice); `mobile` package = enrollment client (RFC 8628 against
  the existing hub endpoints), VpnService config parse, and nebula on
  the VpnService tun fd via upstream `overlay.NewFdDeviceFromConfig`
  (no fork). Bind surface gobind-verified against the Kotlin call sites.
- App (`e560147`): two-screen Kotlin (enroll QR/poll → toggle + host
  list), leanback + touch in one APK, boot autostart, framework views
  only. CI builds AAR + APK and publishes to a rolling release
  (`12ff234`):
  `https://github.com/marnyg/talos-config/releases/download/android-latest/talos-mesh.apk`
- Hub deployed + unsealed (owner), app sideloaded and tested end to end
  on the owner's TV: enroll → wallet approve → tunnel → host list. Works.
- Also fixed the three 2026-08-14 review leftovers (`0ce982d`): "denyd"
  typo, stale dnsRespond comment, empty-name challenge guard (+test).

## Loose threads

- **Parents' TV not yet done** — that's the actual goal; owner's TV was
  the dress rehearsal. Remote = relayed through fly (ADR-0006), untested
  with real Jellyfin playback bitrates.
- **Jellyfin from the app is by overlay IP** (no DNS on the tun — see
  exploration log). If Jellyfin needs its ingress *name* (Host-header
  routing), that's unresolved; IP:NodePort works today.
- APK is ~94 MB (4 ABIs bundled); `abiFilters arm64-v8a` would quarter it.
- **w1 still down** (since 2026-08-04) — still gates the storage arc.
- 90-day renewal automation (`49443c38`) owed before ~2026-11-12; the
  app re-enrolls manually like every other client.

## Suggested next steps

- Ship the APK + a Downloader-app one-liner to the parents' TV; watch
  relay throughput during actual playback.
- Power-cycle w1 (unchanged from last session) — opens the storage arc.
- Decide the Jellyfin-addressing story for TV clients (IP:port vs name)
  before the parents' visit, not during it.
