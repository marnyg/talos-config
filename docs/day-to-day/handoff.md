# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (eleventh session, two parallel agents) — **the TV is on
the identity plane (`359.9.4.5`) and the phone played media
(`359.9.4.4`): P2.4 is done on both devices, and the flow counters
that made it hard to see are fixed.**

### Phone + counters (`359.9.4.4`, closed)

- **The phone's half of the acceptance criteria is met**: signed in
  via Quick Connect, Big Buck Bunny `DirectPlay` for the whole 9:56
  title (no transcode, no ffmpeg in the pod), surviving backgrounding
  into PiP, with `Conn.Paths()` read mid-playback showing
  `*ip:10.0.0.67` — the LAN-direct path, no relay fallback.
- **The libraries were empty**, which is why no one had played
  anything: Big Buck Bunny (H.264 + AAC, 830 kbps, 60 MB) now sits in
  `/data/movies` as a fixture, written through the **radarr** pod
  (jellyfin mounts movies read-only).
- **`d4960c1`**: `meshtun.Pipe` takes a `*Counters` and adds bytes per
  copied chunk instead of once at flow close — a movie-long flow read
  as zero throughput before. Counted copies bring a 64 KiB buffer
  since wrapping the destination costs `ReadFrom`. Same commit warms
  **both** devices' tunnels in `TestMeshHTTPOverOverlay` (the tv paid
  for a handshake inside a 2 s timeout — the long-standing flake) and
  names the request timeout for what it is.
- **Verified on-device on a fresh APK** (rebuilt on `mar@nixos`,
  `adb install -r`, member identity survived): 45 s into playback
  `bytesIn` read 12.4 MB and climbed to 17.6 MB with `flowsOpen: 10`
  and nothing closed — live accounting, which is the property.

### TV (`359.9.4.5`, closed)

- **The Shield runs the v3 APK and plays media over the plane.**
  Driven end to end over network adb (`adb connect 10.0.0.2:5555`,
  authorized once from the remote). Enrolled through the in-app device
  flow as member **`tv` / `[media]`**, NodeId `ed:b96def6f…`; the
  gateway logged `admitted "tv" [media] → jellyfin.media.svc…:8096`.
  Split DNS correct (`hub→198.18.1.1`, `jellyfin.gw→198.18.1.2` in
  4 ms, `example.com` to the underlay), split routing correct (only
  `198.18.0.0/15` in `tun0`, default stays on wlan0).
- **Media verified where the phone's could not be**: Big Buck Bunny,
  **`DirectPlay`, no transcode**, `RemoteEndPoint 10.244.2.222` (the
  gateway pod), selected path **`*ip:10.0.0.67:48198` = w1 LAN-direct,
  no relay**, 138 ms to connect. `tun0` carried 20.3 MB at 0.89 Mbps —
  the clip's bitrate, not a ceiling (the only library item is 480p).
- **Sign-in went around the wall, not through it.** SIWE still cannot
  run in an app webview (`95la`/`i1il`), so the TV came in on **Quick
  Connect**, authorized via `kubectl exec` against the Jellyfin API as
  the `jellyfin-admin` break-glass account. The TV therefore holds a
  **Jellyfin admin** session, not the wallet identity.
- **The `android-latest` release had never carried the v3 APK** — it
  was still the 94 MB CI-built v1 from 2026-09-19. Published the
  18 MB v3 build (`android/publish.sh`, `talos-mesh.apk @ c063f06`).
  Sideload over that asset would have installed nebula-era v1.
- `k8s/apps/jellyfin/configmap.yaml`: the branding comment said
  "jellyfin.cp1 (nebula TV until P2.4)" — corrected to name Quick
  Connect / the local form as the non-SIWE path and to mark the
  `jellyfin.cp1` registration as dead weight until `vftt`.

## Loose threads

- **The bead's premise was half wrong.** The TV's *active* Jellyfin
  server was `https://jellyfin.pytt.io` — a **foreign** server (id
  `9cdce8763dbc`, "odin", v12.0.0) reached over **Tailscale**
  (`com.tailscale.ipn` is installed; the name resolves into 100.64/10).
  `jellyfin.cp1.mesh.internal` was a *saved* server, not the one in
  use, and the v1 mesh app was not even running (no tun). Owner's
  call: **leave all saved servers in place**, and **leave the
  Tailscale/mesh one-VPN-slot conflict unrecorded** for now.
- **The TV is signed in as Jellyfin `admin`.** Jellyfin admin means
  plugin install, i.e. code execution in the pod
  (`k8s/apps/jellyfin/sealed-secret.yaml:8`). A non-admin user for the
  TV is the obvious hardening and was not done.
- **The Mac daemon is still on the pre-`d4960c1` binary.**
  `~/git/nixos/flake.lock` is bumped to `d4960c1` (uncommitted), but
  `/run/current-system` and `/Library/LaunchDaemons/org.nixos.talos-mesh.plist`
  are from 14:13 and point at a store path whose source has no
  `Counters` — the switch has not run since the bump.
- **The Mac cannot reach the raw Jellyfin facet.**
  `jellyfin.gw.mesh.internal:8096` from `marius-mac` fails to connect
  in ~0.1 s, while `hub` (404) and `jellyfin.gw:80` (302, the ingress)
  both answer — the phone and TV, both in group `media`, reach `:8096`
  fine. Smells like facet authorization by group, unconfirmed.
- **The Jellyfin Android app showed "Server Mismatch"** against
  `jellyfin.gw…:8096` after a force-stop, and connecting anyway
  dropped the session — a re-login per reconnect. Unexplained; the
  stored ServerId and what that address answers disagree.
- `bh74` (a member advertising its own fake-range address) reproduces
  on the TV: `endpoints` includes `iroh:udp=198.18.0.1:59924`.
- The enroll screen's status line ("approve at /status") renders at
  y≈1130 on the Shield's 1080-px screen — invisible; only the QR and
  user code show.
- `tun: flow to 198.18.0.2:853: not a name we minted` counts as a
  `flowError` on every start — that is Android Private DNS probing
  DoT at the fake resolver (a P0.2 finding), logged as an error.

## Suggested next steps

- Cut the now-unblocked nebula scaffolding: `vftt` (`jellyfin.cp1` in
  `k8s/apps/media/ingress.yaml`, the siwe-oidc client list, the
  jellyfin configmap comment), `ri3b` (the hub's `/hosts` + `/policy`),
  `xnat` (the `1gv` gate + ingress-nginx hostNetwork).
- Re-run `darwin-rebuild switch` so the Mac daemon picks up the live
  counters, and commit the `~/git/nixos` lock bump.
- Publish the rebuilt APK (`android/publish.sh`) if the rolling
  release should carry the live-counter build; the asset currently
  holds the `c063f06` one.
- Then P2.5 (`359.9.5`): k8s/Talos endpoint off the mesh.
