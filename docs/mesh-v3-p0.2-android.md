# Mesh v3 — P0.2 Android feasibility: working plan

Bead `talos-config-359.1.2` (blocks gate `359.1.5` and `359.9.4`).
Parent plan: [`mesh-v3-iroh.md`](mesh-v3-iroh.md) §Phase 0 check 2,
kill-criterion 2. **Status: in progress since 2026-09-16 on branch
`spike/mesh-v3-p0.2` — see "Progress log" at the end.** When
this spike lands, fold the result into `mesh-v3-iroh.md §P0.2` (same
shape as §P0.1/§P0.3) and delete this file.

This file exists so a fresh session can pick the spike up without
re-deriving it. Read it together with `docs/day-to-day/handoff.md`.

## Pass / fail

**Pass:** the Jellyfin Android app streams a 4K remux **≥ 80 Mbps
sustained** through the tunnel (parity with mesh-v2 kill-criterion 3),
and battery over a 2 h stream on a phone is acceptable to the owner.
**Fail ⇒ kill-criterion 2 fires:** record why in `mesh-v3-iroh.md`,
re-defer `359`, keep nebula. A fail is a valid outcome.

## Path under test (the end-state Android/TV design in miniature)

```
Jellyfin app ──TCP to fake IP──▶ VpnService tun fd
                                    │  (Kotlin owns fd, routes, DNS server)
                                    ▼
                         gvisor netstack (Go, fdbased link endpoint)
                          ├─ UDP/53 on the fake resolver:
                          │    *.mesh.internal → fake IP from 198.18.0.0/15
                          │    everything else → underlay via protected socket
                          │    (dnsshim pattern, config-server/mobile/dnsshim.go)
                          └─ TCP forwarder: per flow to a fake IP →
                                iroh stream, ALPN mesh/http/v1, to the mapped NodeId
                                    │
                                    ▼ (relay first, LAN-direct hole-punch expected)
                          p0agent serve  -forward mesh/http/v1=<jellyfin>:30096
```

Split routing: only `198.18.0.0/15` + the fake resolver IP are added
as VPN routes; the rest of the phone's traffic never enters the tunnel.
One name in the spike: `jellyfin.mesh.internal → cp1's NodeId`
(hard-coded name→NodeId map; the real one is git-derived, Phase 1).

Everything on the device is **Go in one gomobile AAR**: `iroh-go`
(in-house uniffi binding, ADR-0021) + `gvisor.dev/gvisor/pkg/tcpip`
(already a config-server dep via `nebstack`). This is the
Tailscale-Android shape and the one the plan commits to
(`mesh-v3-iroh.md` §Architecture "Android/TV app").

**Ruled out for the spike:** Kotlin `computer.iroh:iroh:1.1.0` from
Maven + a separate netstack — two runtimes with per-flow streams
bridged across JNI, and it would not exercise the binding we own.
Reconsider only if the Go cross-compile (step 1) fails.

## Decisions taken 2026-09-16 (owner confirmed)

1. **Node side:** iterate against `p0agent serve` on the NixOS box
   (`mar@nixos`, LAN `10.0.0.11`) forwarding to cp1's LAN IP:30096;
   then rebuild the cp1 extension **once** (`0.0.4`, add
   `-forward mesh/http/v1=127.0.0.1:30096` in
   `talos/extensions/p0agent/rootfs/usr/local/etc/containers/p0agent.yaml`,
   `build.sh`, `talosctl upgrade`, ~10 min drain) for the final
   measurement. The NodeId changes between the two (stand-in vs cp1
   key) — the APK takes it as input, not a constant.
2. **Build host:** nix `androidenv` (SDK + NDK, unfree) on the NixOS
   box — the existing x86_64 builder for `.#p0relay-static`. Neither
   this Mac nor the box has an Android SDK/NDK/adb/cargo today; CI
   (`.github/workflows/android-apk.yml`) has an SDK+NDK and is the
   fallback if androidenv fights back for more than ~2 h.
3. **APK:** a **separate minimal spike APK**, not the shipped app:
   `iroh-go/android-p0/` (one screen: relay URL, peer NodeId, Start /
   Stop, live counters). The shipped `android/` app's CI publishes to
   the rolling `android-latest` release on every push touching it —
   do not touch it in the spike.
4. **Devices / owner's part:** owner streams on the Shield (throughput)
   and a phone (2 h battery). Owner sideloads the APK, enters relay +
   NodeId, starts the tunnel, opens the Jellyfin app against
   `http://jellyfin.mesh.internal:30096` (HTTP, NodePort — no ingress
   / TLS in the spike), plays the remux, reads battery % at start and
   end. *Open:* which library file is ≥ 80 Mbps — the mesh-v2
   measurement used one; find it or pick the largest remux
   (Jellyfin shows bitrate in the item's media info).

## Steps, in fail-fast order

1. **Cross-compile `libiroh_ffi.a` for `aarch64-linux-android`** — the
   real feasibility question; nothing else matters if this fails.
   `cargo build --release --target aarch64-linux-android` on the
   iroh-ffi 1.1.0 source with the NDK clang as linker (`cargo-ndk` or
   `CARGO_TARGET_AARCH64_LINUX_ANDROID_LINKER`), Rust toolchain with
   the android target added. Prefer a nix derivation under
   `iroh-go/nix/` (`iroh-ffi-android`) that reuses the vendored
   crates (`fetchCargoVendor`, same `regen/iroh-ffi.Cargo.lock`);
   fall back to an ad-hoc `rustup`+`cargo-ndk` shell on the box if nix
   cross fights back. Expect ring/aws-lc-rs or similar C deps to need
   the NDK sysroot — that is the known trap. Deliverable: the `.a` plus
   a note on what it took. Time-box: **half a day**.
2. **gomobile bind with cgo static link.** New Go package
   `iroh-go/mobile/` (`package p0mobile`): `Start(tunFd int, relay,
   peerHex, upstreamDNS string) (*Tunnel, error)`, `Tunnel.Stop()`,
   `Tunnel.StatsJSON()`. Link flags per arch in a `link_android.go`
   (`#cgo android,arm64 LDFLAGS: -L${SRCDIR}/../lib/android-arm64
   -liroh_ffi`); the generated binding in `iroh-go/iroh/` is
   platform-neutral so nothing regenerates. `gomobile bind
   -target=android/arm64 -androidapi 26`. Deliverable: an AAR whose
   `iroh.Endpoint` binds and dials the scratch relay from an
   `adb shell` smoke (`am start` + logcat) before any netstack exists.
3. **Netstack + fake IP.** In `p0mobile`: `fdbased.New` on the tun fd,
   `stack.New` with ipv4 + tcp + udp; `tcp.NewForwarder` → for each
   flow to a fake IP look up the NodeId, `ep.Connect(addr, alpn)`,
   `OpenBi`, splice both ways; `udp.NewForwarder` on `<resolver>:53`
   → answer `A` for `*.mesh.internal` from the fake pool, forward the
   rest to `upstreamDNS` on a socket Kotlin has `protect()`ed (Go
   needs the fd protected: either Kotlin creates and protects a
   DatagramSocket and passes its fd, or Go dials and asks Kotlin back
   via a callback interface — pick the fd-passing route, it is what
   the shipped app does). Lift the pool/mapping and the query
   classification from `config-server/mobile/dnsshim.go`; do not
   import `config-server` (module boundary — copy the ~100 lines).
   Stream side of `p0agent` is the reference for splice/close
   semantics (`iroh-go/cmd/p0agent/main.go` `serveConn`).
4. **Spike APK** `iroh-go/android-p0/`: `VpnService` that
   `Builder.addAddress(198.18.0.1/32).addRoute(198.18.0.0/15)
   .addDnsServer(198.18.0.2).setMtu(1280).establish()`, hands the fd
   to `p0mobile.Start`, foreground notification, Start/Stop activity
   with counters (bytes in/out, flows, direct vs relay path from
   `conn.Paths()`). Model on `android/app/src/main/java/dev/marnyg/mesh/MeshVpnService.kt`
   and `MainActivity.kt` — copy, don't share code. MTU 1280 keeps QUIC
   payload clear of fragmentation over the underlay.
5. **Node side stand-in:** on the NixOS box, `p0agent serve -relay
   https://marnyg-iroh-relay-spike.fly.dev -key /tmp/p0key -forward
   mesh/http/v1=<cp1 LAN IP>:30096` (binary from
   `nix build .#p0relay-static` — it ships p0agent too; check
   `iroh-transport/nix`). Log its NodeId; that is the APK input.
6. **Measure** (owner + agent):
   - Throughput: `p0agent` logs bytes/s per stream (add a 5 s ticker
     if it does not yet); Jellyfin dashboard shows the play method
     (must be *Direct Play*, not transcode — otherwise the bitrate
     floor is meaningless) and bitrate. Also `logcat` counters from
     the APK. Target ≥ 80 Mbps sustained for ≥ 10 min, on the Shield,
     on Wi-Fi and/or wired as the owner's TV is connected.
   - Path: expect `*ip:10.0.0.x` (LAN-direct) after the first
     seconds; if it stays `*relay:` the fly relay's bandwidth is what
     is being measured, not the design — note and fix before judging.
   - Battery: phone, 2 h stream, `dumpsys batterystats` or %-delta;
     compare against the same stream over the shipped nebula app if
     the owner has time (parity is the bar, not perfection).
7. **cp1 extension 0.0.4 + final run** (decision 1). Then write
   `mesh-v3-iroh.md §P0.2` with a table like §P0.3, close the bead,
   move to the gate `359.1.5`.

## Known traps (from P0.1 / P0.3 / the shipped app)

- The Mac cannot send LAN UDP from unsigned binaries (Cisco filter on
  `en0`, P0.1) — measure LAN-direct from the device, not the laptop.
- The NixOS firewall dropped hole-punches from NAT'd peers until
  inbound UDP was allowed on `wlp12s0` (P0.1). The stand-in agent's
  UDP port must be reachable from the TV's Wi-Fi.
- QAD is off on the scratch relay; LAN-direct still works via DISCO
  (P0.1) as long as one side has a LAN-reachable address — both do here.
- Android: `addDnsServer` captures *all* DNS on most versions, hence
  the underlay-forward in step 3 (this is exactly the shipped app's
  `dnsshim` reason). `protect()` must be called on the forwarding
  socket or the query loops back into the tun.
- iroh-ffi's uniffi Go binding does its contract-version check in
  `init()` and panics on mismatch — the Android `.a` must come from
  the **same** iroh-ffi commit/lock as `iroh-go/iroh/*.go`
  (`5e45109`, `regen/iroh-ffi.Cargo.lock`).
- `gomobile bind` needs `ANDROID_HOME` + `ANDROID_NDK_HOME`; the
  shipped `android/build-aar.sh` shows the pinned-tool pattern
  (`GOBIN=$tmp go install golang.org/x/mobile/cmd/{gomobile,gobind}`)
  — `iroh-go/go.mod` will need `golang.org/x/mobile` for that.
- Android 14+ requires `foregroundServiceType="specialUse"` or the
  VPN type declared; the shipped manifest has the working incantation.

## Not in the spike (record, don't build)

Certs/`authorize()` on accept (ALPN gates the forward table only);
git-derived name→NodeId map; HTTPS / ingress over the tunnel (HTTP to
the NodePort is enough for a bitrate); enrollment / device flow;
TV/phone re-enrollment (`359.9.4`); the desktop fake-IP TUN.

## Progress log

### 2026-09-16 — steps 1–5 built, blocked on media

Branch `spike/mesh-v3-p0.2` (pushed). Bead `359.1.2` in_progress.

| step | state | where |
|---|---|---|
| 1 cross-compile `libiroh_ffi.a` | **✓ built** on the NixOS box, `/tmp/android-ffi-result/lib/libiroh_ffi.a` (36 MB; a `.so` lands next to it). Eval clean, NDK 27 toolchain + bionic, cross `rustc 1.93.0` for `aarch64-linux-android`, ~1 h unattended. **No Rust or lock changes**; ring/aws-lc found the NDK sysroot via nixpkgs' cross stdenv | `iroh-go/nix/android.nix` = `import ./. { pkgs = pkgsCross.aarch64-android-prebuilt }` — same pipeline, same patched lock. Impure/unfree (`NIXPKGS_ALLOW_UNFREE=1 nix build --impure -f iroh-go/nix/android.nix`) |
| 2 gomobile package | **✓ bound**: `app/libs/p0mobile.aar` (11.6 MB), `libgojni.so` 31.5 MB arm64, iroh linked **statically**, `NEEDED` = bionic only (`liblog libandroid libdl libm libc`), 0 undefined `uniffi_iroh_*`. Three fixes it took: (a) `mobile/tools.go` (tools tag) keeps `x/mobile` + its cmds in go.mod through `go mod tidy` — same pattern as `config-server/tools.go`; (b) `iroh/link_android.go` had prose in the cgo preamble (fed to clang as C) — blank line before `#cgo`; (c) `#cgo linux` also matches GOOS=android and bionic has no libpthread → `linux,!android` in `iroh/link.go`; plus `build-aar.sh` now stages a dir holding only the `.a` because lld preferred the `.so` | `iroh-go/mobile/` (own module `…/iroh-go/mobile`, `replace ../`): `tunnel.go` (Start/Stop/StatsJSON, one iroh connection with redial, per-flow `OpenBi` + p0agent's `pipe`), `netstack.go`, `dns.go`. `iroh-go/iroh/link_android.go` adds `-llog -ldl -lm` for GOOS=android |
| 3 netstack + fake IP | **written** | gvisor `fdbased` on the fd, promiscuous + spoofing NIC, default route, `tcp.NewForwarder` → `handleTCP`, `udp.NewForwarder` on :53 only → `fakeDNS`. Fake plan: tun `198.18.0.1`, resolver `198.18.0.2`, names from `198.18.1.0` up; `*.mesh.internal` A → fake IP, AAAA → empty NOERROR, other names → underlay via `SocketProtector`-protected socket. TCP rcv/snd buffers 1 MiB default / 4 MiB max, SACK, moderate-rcvbuf on |
| 4 spike APK | **✓ built**, `gradle --no-daemon assembleDebug` in 54 s inside `shell.nix` (aapt2 override worked first time). Copied to the Mac: `~/Downloads/p0mesh-debug.apk` (33 MB). Not yet sideloaded | `iroh-go/android-p0/` (`dev.marnyg.p0mesh`): `P0VpnService.kt` (`addAddress 198.18.0.1/32`, `addRoute 198.18.0.0/15`, `addDnsServer 198.18.0.2`, MTU 1280, `detachFd` → `P0mobile.start`), `MainActivity.kt` (relay + peer inputs in prefs, Start/Stop, 1 s stats poll with a Mbps readout), `build-aar.sh` (gomobile, android/arm64 only, `IROH_FFI_ANDROID_LIB` = dir with the android `.a`), `shell.nix` (androidenv SDK 34 + NDK 27.0.12077973 + gradle + jdk17; being realised on the box, `/tmp/android-shell.log`) |
| 5 stand-in agent | **running** on the box as user unit `p0agent-standin` (`systemctl --user`, `Restart=on-failure`, linger on): `p0agent serve -relay …spike.fly.dev -key ~/p0-jf/p0key -bind 0.0.0.0:7842 -forward mesh/http/v1=127.0.0.1:8096`. **NodeId `5852d8b0e1e1c836b628f3ad1986d22042101eb9f672704ef89c1d8858db513c`** — the APK's peer input. Homed in 3.1 s. (`:41641` is Tailscale's, hence 7842.) Smoke on the box: `p0agent bridge … -listen 127.0.0.1:18096` → `curl /health` = Healthy; the Direct Play stream moved 5.7 GB in 14.8 s ≈ 3.1 Gbps over iroh, path went `*relay` → `*ip` inside the first stream. Forwards to the **stand-in Jellyfin** below, not cp1:30096 | `~/p0/result/bin/p0agent` is the P0.3 static build. cp1 LAN lease today: **`10.0.0.58`** (apid :50000 answers; `talosctl -e 10.0.0.58 -n 10.0.0.58` works). Kubeconfig fetched to the Mac at `/tmp/kc` (`kubectl --server https://10.0.0.58:6443 --insecure-skip-tls-verify`; kube SANs are mesh-only) |
| 6 measure | **✓ PASS** (see the evening entry: 97 Mbps avg / 154 peak, 10.6 min, LAN-direct) | see below |

**Stand-in media (owner decision 2026-09-16: reuse the box's existing
Jellyfin).** The cluster's Jellyfin has no media (below), so the owner's
compose-managed `jellyfin` container on the box (`~/disks/1TB-old/server/
docker-compose.yml`, lscr.io/linuxserver 10.10.7, `:8096`, user `abc`)
is the target. Synthetic test file — the library had nothing ≥ 80 Mbps
and the 1TB disk is 100 % full, so it lives on the NVMe:
`~/p0-jf/media/P0 Remux Test (2026).mkv`, ffmpeg libx264 3840×2160
level 5.1 **CBR 95.1 Mbps** (`nal-hrd=cbr`, padding — synthetic content
would otherwise compress to nothing), AAC, 8 min, 5.7 GB. Bind-mounted
`:ro` at `/data/p0test` (one line in the compose file, backup
`~/p0-jf/docker-compose.yml.bak-p0`), library **"P0 Test"** (movies, no
internet metadata) added as `config/plex/data/root/default/P0 Test/`.
`PlaybackInfo` with an h264/aac mkv profile → `SupportsDirectPlay: true`,
no `TranscodingUrl`. Item id `b49edd95044f91b75df41887342bf657`; token
for curl in `~/p0-jf/token`. Owner-side URL from the phone/Shield once
the tunnel is up: **`http://jellyfin.mesh.internal:8096`** (any port on
the fake IP lands on the forward target; 8096 keeps the Jellyfin app's
default). Caveat for the writeup: this measures the phone ↔ NixOS box
path, not phone ↔ cp1 — step 7 still does the cp1 run.

**Blocker found (outside the spike):** Jellyfin cannot stream anything.
`media/{movies,tv,downloads}` Longhorn volumes are `faulted`: their only
replica is on **w1**, down since 2026-08-10 (`numberOfReplicas: 1`,
bead `0q0`). Jellyfin pod is `ContainerCreating` / `FailedAttachVolume`.
Also noticed: **cp1 rebooted at 2026-09-15 10:31Z** (dmesg), cause
unknown at time of writing; longhorn CSI sidecars and media pods from
before show `ContainerStatusUnknown`. Options put to the owner: (1)
power w1 on, (2) stand-in Jellyfin on the NixOS box behind the stand-in
agent with a caveat in the writeup, (3) both. **Owner decision
pending.**

### 2026-09-16 (later) — steps 1–5 done, APK in hand

Everything up to the device is built and running; the owner's part
(decision 4) is next. **Inputs for the APK:** relay
`https://marnyg-iroh-relay-spike.fly.dev`, peer NodeId
`5852d8b0e1e1c836b628f3ad1986d22042101eb9f672704ef89c1d8858db513c`
(the stand-in on the box). Jellyfin app server URL once the tunnel is up:
`http://jellyfin.mesh.internal:8096`, user `abc`. Play *P0 Test → P0
Remux Test (2026)*; Jellyfin's dashboard must say Direct Play, the APK
readout should sit ≈ 95 Mbps; `journalctl --user -u p0agent-standin` on
the box logs the paths (`*ip:` = LAN-direct, `*relay:` = the fly relay's
bandwidth, not the design). Rebuild path for changes: `scp` the changed
files to `mar@nixos:~/p0/…` (single-branch clone, detached), then
`NIXPKGS_ALLOW_UNFREE=1 nix-shell --impure iroh-go/android-p0/shell.nix
--run 'IROH_FFI_ANDROID_LIB=/tmp/android-ffi-result/lib ./build-aar.sh
&& gradle --no-daemon assembleDebug'`.

### 2026-09-16 (afternoon) — phone end to end; LAN-direct deferred to at-home

**APK runs on the owner's phone** (Sony XQ-BQ52, Android 13, 4 KB pages):
VpnService → gvisor netstack → fake DNS → iroh → stand-in agent →
Jellyfin. `jellyfin.mesh.internal` resolves, the Jellyfin app browses and
plays the 95 Mbps file **Direct Play**. Sideloading needed `adb install`
(`nix shell nixpkgs#android-tools` on the Mac; the Files-app installer
failed silently after the Play Protect prompt, no useful reason).

**Throughput measured: 56–70 Mbps, relay path, from outside the home**
(box-side 5 s ticker; the phone agreed). Every session stayed on
`*relay:` — and legitimately so: **nobody was on the home LAN today**.
Mac `10.144.x`, phone `10.150.9.x` (+ Tailscale), box at home
`10.0.0.11`. No LAN candidate on either side can reach the other, and
with **QAD off** on the scratch relay (P0.1 decision: needs the relay to
own a TLS cert; remote-direct is out of scope per ADR-0006) neither side
learns a public `ip:port`, so WAN hole-punching is never attempted.
The number is therefore the **fly relay's ceiling to a phone across the
internet**, not a property of the design. **Step 6 throughput (≥ 80 Mbps
LAN-direct, Shield) is deferred to the next at-home session**; everything
is in place for it (unit running, APK installed, inputs above).

Findings along the way, all recorded for the writeup:

- iroh **does** enumerate interfaces inside an Android 13 app (logcat:
  `iroh-discovered direct addrs [10.150.9.121:43090 …]`). The
  `AddExternalAddr` plumbing (Kotlin `LinkProperties` → Go) added on the
  netlink theory stays as belt-and-braces; it was not the cause.
- Box-side `peer-direct=[]` means *no candidate validated*, not *none
  advertised* (iroh's `remote_addr` holds pinged addresses). Comment fixed.
- **Starting p0mesh kicks Tailscale off the phone** — Android runs one
  VpnService at a time. Both sides advertised Tailscale addresses that
  were dead on arrival. Same constraint applies to the shipped app.
- logcat: `thread panicked at ndk-context-0.1.1: android context was not
  initialized` from inside iroh-ffi, non-fatal (tunnel comes up). iroh's
  Android network monitor wants a JNI context we never hand it (gomobile,
  no `JNI_OnLoad` hook of ours) — network-change detection on the phone
  is probably dead. Phase 1 item: initialise `ndk_context` from Kotlin,
  or accept and redial on `ConnectivityManager` callbacks ourselves.
- Android's Private DNS tried DoT (`198.18.0.2:853`) against the fake
  resolver; the flow went into iroh and died at the box. Harmless (falls
  back to :53) but the netstack should refuse non-:53 flows to the
  resolver IP rather than forwarding them.
- Debug-APK packaging: AGP stored `libgojni.so` uncompressed on an
  incremental build (64 MB APK) and compressed on the clean one (33 MB).
  Now pinned: `useLegacyPackaging = true` + `-Wl,-z,max-page-size=16384`
  (LOAD align `0x4000`) so it also loads on 16 KB-page devices. APK 13.5 MB.
- `p0agent serve` now logs its advertised direct addrs at start and, per
  connection, a 5 s `Mbps to peer, paths=…` ticker while bytes move.

**Jellyfin app, Direct Play confirmed server-side** (`/Sessions`:
`Jellyfin for Android XQ-BQ52 | P0 Remux Test | PlayMethod: DirectPlay`,
no TranscodingInfo, no ffmpeg). Firefox first: buffers forever (no MKV
demuxer) while pulling the raw stream — 1.69 GB in 5 m 07 s ≈ 44 Mbps
avg over the relay; useless as a player, fine as a throughput probe.

**Battery run 14:34–15:07 CEST, relay path, Direct Play 4K, screen on,
unplugged: 99 % → 91 % (8 %, ≈ 15 %/h).** Box-side ticker: 346 samples,
29 of 32 min streaming, **avg 48.8 Mbps, peak 74.6**, one iroh session,
zero redials. `dumpsys batterystats` (computed drain 312 mAh, actual
255–292, capacity 3644):

| consumer | mAh | detail |
|---|---|---|
| Jellyfin app (u0a445) | 205 | screen 131 · video decode 36.4 · Wi-Fi 28.5 · audio 7.7 |
| **p0mesh tunnel (u0a431)** | **8.5** | cpu 3.84 (31 m 32 s fgs) · Wi-Fi 4.63 |
| rest (idle, mobile radio, system) | ~100 | |

The tunnel — iroh QUIC + gvisor netstack + fake DNS at ~49 Mbps for half
an hour — is **≈ 3 % of the drain, ≈ 0.4 %/h of the battery**; screen
and the 4K decoder dwarf it. **Battery: pass**; the nebula-app parity
comparison is moot at this magnitude. Stopped at 32 min: the trend was
clear and the per-app split is the number that matters. Caveat: relay
path, not LAN-direct — crypto and netstack work are the same either way,
so the app-side figure transfers; only the Wi-Fi share could shift.

**P0.2 status at this point:** feasibility (steps 1–5) **pass**, battery
**pass**, throughput pending the at-home LAN-direct run (below).

### 2026-09-16 (evening, at home) — step 6 LAN-direct: PASS

Owner back on the home Wi-Fi (Mac `10.0.0.7`, phone `10.0.0.6`, box
`10.0.0.11`). Box state unchanged since 14:32 (same `p0agent-standin`
process, no reboot); the `iptables` hole was **never added** — and was
not needed: the very first session at 17:12 went `*ip:10.0.0.6` through
nixos-fw via conntrack.

Run 17:15–17:26, phone, Jellyfin app Direct Play of *P0 Remux Test*,
box-side 5 s ticker (`journalctl --user -u p0agent-standin`):

| | |
|---|---|
| samples | **127 (10.6 min)**, one iroh session `83b7d4f077`, zero redials |
| path | **`*ip:10.0.0.6:43395` on 127/127** — relay listed as a candidate, never selected |
| avg / min / max | **97.0 / 0.0 / 154.0 Mbps**; 121/127 samples ≥ 80 (the one `0.0` is the ramp-up sample, the other five are buffer-full pauses) |
| per-minute avg | 60.7 (ramp) · 112.9 · 95.1 · 95.0 · 95.2 · 102.4 · 104.4 · 95.1 · 95.1 · 90.2 · 100.2 · 95.2 |

Steady minutes sit at 95.0–95.2 = the file's 95.1 Mbps CBR to the
decimal, so the **player** is the limiter; the 115–154 Mbps bursts when
the player refills its buffer are the tunnel's real headroom. **≥ 80
Mbps sustained ≥ 10 min LAN-direct: PASS.** Measured on the phone, not
the Shield — the bar is device-agnostic and the Shield gets its turn in
the parents'-TV deployment (`4te`).

**P0.2 verdict: PASS on all three checks** (feasibility, battery,
throughput). Remaining in this bead: step 7 (cp1 extension `0.0.4` with
the `mesh/http/v1=127.0.0.1:30096` forward, rides on `5cz`) — a
completeness run against the real node, not a gate criterion.

## Scratch infra this spike adds (tear down or adopt at the gate)

- NixOS box: user unit `p0agent-standin` + `~/p0-jf/` (key, token, 5.7 GB
  test file); the `/data/p0test` line in `~/disks/1TB-old/server/
  docker-compose.yml` and the `P0 Test` library in Jellyfin. No firewall
  hole yet (LAN peers should punch via conntrack; add
  `iptables -I nixos-fw -i wlp12s0 -p udp --dport 7842 -j ACCEPT` if the
  APK stays on `*relay`).
- cp1: extension `0.0.4` with the Jellyfin forward (joins bead `5cz`).
- Possibly `ghcr.io/marnyg/{p0agent,talos-installer}` new tags.
- No new fly apps; reuses the scratch relay (`kql`).
