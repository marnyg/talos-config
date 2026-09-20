# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (fourteenth session, same evening as P2.5) — **Phase 3
soak started; the forced w1 reboot (event 1/3) surfaced two P2.5
defects. Remote-media (event 3/3) attempted, not covered.**

- **w1's declared address never applied.** P2.5's `deviceSelector`
  used the machine-dir MAC `98:e7:43:11:97:b8` — Dell's *pass-through*
  address, which only a Dell dock inherits; the LAN NIC today is an
  r8152 dongle `0c:37:96:5d:26:c4`. Pre-reboot `.71` was a surviving
  DHCP lease; the reboot came back on `.72`. Fixed: patch.yaml now
  pins the dongle's MAC (user chose that over `physical: true`), and
  the same one-line change went to w1 live via `talosctl patch mc`
  (**hub not redeployed** — the served config still has the old
  selector until the next `fly/deploy.sh`). Flannel needed its pod
  deleted after the address change (stale `public-ip`), same as after
  a rename. Beads `c4vd` (reinstall path), `hyjv` (`-n w1` via cp1
  picks the dead nebula leg; use the LAN IP until Phase 4).
- **P2.5 rotated the service-account issuer.** Talos derives
  `--service-account-issuer`/`--api-audiences` from the cluster
  endpoint; cp1's kubelet re-fetched every pod's token 2 s before the
  apiserver flipped, minting 1-year tokens with `iss` = the nebula
  endpoint. 14 control-loop pods on cp1 (kube-proxy, flannel,
  longhorn-manager/csi-plugin, all kubevirt, ingress-nginx) were
  `Unauthorized` and were deleted by hand → recreated clean. Data-
  plane pods never touched the API and were left alone. Bead `etzl`.
- Longhorn salvaged `win2k25`'s two volumes after the dead
  virt-launcher was deleted; the VM is back on w1, replicas rebuilding.
  34 dead pod objects (incl. the two 18 h-old kubevirt ones) cleaned.
- **Remote media, phone on cellular:** the tunnel re-underlaid
  (`advertising 10.3.91.10:…`) but took ~3.5 min to `beat ok` — the
  agent's hub-fetch `http.Client` reuses the Wi-Fi-era h2 connection,
  30 s timeout per attempt (bead `rnfk`). Then the cellular bearer
  itself churned (netId 152→154→155/156, plain `curl` dead), so no
  client could have held a session — **event not covered**. Reverse
  handover 28 s (the stale conn got a RST). Wi-Fi restored; Jellyfin
  reconnects to the gateway in <1 s on Wi-Fi.

## Loose threads

- **Hub redeploy pending** so the served w1 config carries the dongle
  MAC; until then `nix run .#apply` would re-serve the wrong selector
  (harmless live — Talos falls back to DHCP — but it undoes the fix
  on the next reboot). Do it with the next deploy; needs the unseal.
- `talosctl -n w1` (and `nix run .#apply` for w1) hangs via cp1
  (`hyjv`); `-n 10.0.0.71` works.
- Two Longhorn volumes `degraded` (w1 replicas rebuilding) — check
  they return to `healthy`.
- **TV still holds a Jellyfin admin session**; non-admin user not
  created. **Mac daemon still on the pre-`d4960c1` binary**; `~/git/
  nixos` lock bump uncommitted; `darwin-rebuild switch` not run.

## Suggested next steps

- Soak: 1/3 events (node reboot). Still open: a natural hub re-seal,
  a remote-media session from somewhere with stable cellular (or the
  laptop tethered elsewhere). Fix `rnfk` first or the session will
  spend its first minutes waiting on the hub fetch (the h2
  `ReadIdleTimeout` landed at end of session caps a black-holed
  attempt at ~30 s; `CloseIdleConnections` on `NetworkChanged` is
  still the real fix).
- Redeploy the hub (also picks up nothing else — image `585524e`).
- Phase 4 (`359.11`) once the two remaining events have passed.
