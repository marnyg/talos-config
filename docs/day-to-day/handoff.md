# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-16 — **Mesh v3 P0.2 Android: phone end to end, battery pass,
throughput deferred to at-home** (branch `spike/mesh-v3-p0.2`, bead
`359.1.2` in_progress). Progress log with every state and number:
`docs/mesh-v3-p0.2-android.md §Progress log`.

- Built: android `libiroh_ffi.a` via nixpkgs cross (no Rust changes),
  `p0mobile.aar` with iroh **statically** linked (fixes: cgo preamble
  prose, `#cgo linux,!android`, stage only the `.a`, `tools.go` for
  x/mobile), spike APK (`useLegacyPackaging`, 16 KB LOAD align).
  Sideload needs `adb install` (Files-app installer fails silently);
  `nix shell nixpkgs#android-tools` on the Mac, phone = Sony XQ-BQ52.
- Running: stand-in Jellyfin = the owner's compose `jellyfin` on the
  NixOS box (`:8096`, user `abc`) with a synthetic **95 Mbps CBR 4K**
  file bind-mounted at `/data/p0test`, library "P0 Test"; stand-in
  agent = user unit `p0agent-standin` (NodeId `5852d8b0…db513c`, UDP
  7842 → `127.0.0.1:8096`, serve now logs a 5 s path/Mbps ticker).
- Measured: Jellyfin app **DirectPlay** through the tunnel, **49 Mbps
  avg / 75 peak — relay path only**, because nobody was on the home
  LAN (Mac 10.144.x, phone 10.150.x + Tailscale, box 10.0.0.11) and
  QAD is off ⇒ no WAN punch is even attempted. **Battery 32 min:
  99→91 %; tunnel uid = 8.5 mAh ≈ 3 % of drain** (screen 131, decoder
  36). Battery pass; throughput verdict waits for the Shield at home.
- Findings for the writeup: p0mesh kicks Tailscale off the phone (one
  VpnService); iroh-ffi panics a thread on `ndk-context` (network
  monitor without JNI context — non-fatal, but no net-change
  detection); Android Private DNS tries DoT at the fake resolver
  (netstack should refuse non-:53 to it); box-side `peer-direct=[]`
  means *nothing validated*, not *nothing advertised*.
- Owner confirmed: reuse the box's Jellyfin (not w1); the cp1 reboot
  2026-09-15 10:31Z was the owner's. **§P0.2 written up** in
  `mesh-v3-iroh.md` (table + Phase-1 findings), pending only the
  throughput row.

## Loose threads

- **Scratch on the NixOS box from P0.2** (tear down at the gate): user
  unit `p0agent-standin` + `~/p0-jf/` (key, token, 5.7 GB file,
  compose backup), the `/data/p0test` line in
  `~/disks/1TB-old/server/docker-compose.yml`, library "P0 Test", the
  temporary `iptables -I nixos-fw -i wlp12s0 -p udp --dport 7842`.
  `mar@nixos:~/p0` is a single-branch clone at `fa003f8` + scp'd
  files, not a real checkout of the branch.
- **`~/disks/1TB-old` is 100 % full** (Jellyfin's config/DB live
  there); jellyfin and plex share `./config/plex`; compose images
  unpinned `:latest`. Surfaced as broken windows, owner ruling pending.

- **cp1 runs an image git does not declare**: installer
  `ghcr.io/marnyg/talos-installer:v1.12.6-p0agent-0.0.3` (four
  extensions) vs `talos/hardware/minipc.yaml`'s factory `6a9acc…`.
  Knowing deviation from invariant 2, bead `5cz` (blocks the gate):
  upgrade back or make the imager chain the declared image in Phase 1.
- Scratch relay still up and open (`kql`); `ext-p0agent` dials it on
  every boot. Both ghcr packages are public.
- cp1's LAN lease moved `.42 → .58` in one day; `talos/talosconfig`
  endpoints still say `10.99.0.54` (wg0 era) — every `talosctl` needs
  `-e/-n`. The mesh route needs `nebup`; the LAN route needs the current
  lease.
- `mar@nixos:~/p0` is checked out at the spike branch (detached); it is
  the x86_64 builder for `.#p0relay-static`.
- Bridge trick for `talosctl` over iroh: dial a name that is in apid's
  cert SANs (`talos-wu6-eib`) via a hosts entry — `127.0.0.1` is not a
  SAN. The hosts line was removed at session end.

## Suggested next steps

- **`359.1.2` step 6 at home:** phone/Shield on the home Wi-Fi, APK
  Start (inputs in the progress log), play *P0 Remux Test* in the
  Jellyfin app; box: `journalctl --user -u p0agent-standin -f` must
  show `*ip:10.0.0.x` and ≥ 80 Mbps sustained ≥ 10 min. If it stays
  `*relay:`, the firewall rule above is the first suspect (it does not
  survive a box reboot). Then step 7 (cp1 extension `0.0.4` forwarding
  `mesh/http/v1=127.0.0.1:30096`, rides on `5cz`), the
  `mesh-v3-iroh.md §P0.2` table, close `359.1.2`, gate `359.1.5`.
- Phase-1 items surfaced today (record, don't build): `ndk_context`
  init from Kotlin; refuse non-:53 flows to the fake resolver;
  Android one-VPN constraint vs. Tailscale in the app's UX.
- `xwu` (verb = root consent's verb) stays the M3 pre-work.
