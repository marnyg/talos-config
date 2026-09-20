# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (eleventh session) — **the TV is on the identity plane;
`359.9.4.5` closed. P2.4 is done on both devices.**

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
- **Four `config-server/` files are modified in the worktree from an
  earlier session and deliberately left uncommitted**: `meshtun/{tun,
  pool}.go`, `cmd/irohup/main.go`, `mesh/nebhttp_e2e_test.go`. They
  move `BytesIn/BytesOut` into `Pipe` so a long flow shows throughput
  *while* it runs — exactly the gap that made `bytesIn: 0` misleading
  mid-stream this session. `meshtun` is behind the `iroh` build tag,
  so it cannot be compiled on the Mac; `go vet ./mesh/...` passes.
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
- Decide what to do with the four uncommitted `meshtun` files —
  finish and commit on the NixOS box (where the `iroh` tag builds), or
  discard.
- Then P2.5 (`359.9.5`): k8s/Talos endpoint off the mesh.
