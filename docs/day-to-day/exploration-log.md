# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Mesh v3 P0.2 — Android (2026-09-16)

- 2026-09-16 — Tried measuring the ≥ 80 Mbps throughput check with
  owner, phone and Mac away from the home LAN. Ruled out: with QAD off
  (ADR-0022) neither side learns a public `ip:port`, so no WAN punch is
  attempted and every session is the fly relay (~50–75 Mbps to a
  phone). Landed on: relay figure recorded as a relay measurement;
  LAN-direct on the Shield done at home.
- 2026-09-16 — Suspected iroh cannot enumerate interfaces inside an
  Android app (netlink restrictions ≥ API 30) and plumbed
  `LinkProperties → AddExternalAddr`. Ruled out as the cause: iroh
  listed Wi-Fi, cellular and tun addresses itself on Android 13. The
  plumbing stays as belt-and-braces; the empty `peer-direct` on the
  node was "nothing validated", not "nothing advertised".
- 2026-09-16 — Considered enabling QAD on the scratch relay to get a
  WAN-direct data point from outside. Ruled out for the spike: needs
  the relay to own a TLS cert (DNS-01 as a fly secret), and remote-
  direct is out of scope (ADR-0006/0022). Stays under `0pq`.
- 2026-09-16 — Considered powering w1 on (cluster Jellyfin's media
  volumes have their only replica there) vs. a stand-in. Landed on: the
  owner's existing Jellyfin on the NixOS box with a synthetic 95 Mbps
  CBR file — synthetic content otherwise compresses to nothing, hence
  `nal-hrd=cbr`. The final measurement still goes through cp1 (step 7).

## Mesh v3 P0.3 — Talos extension (2026-09-15)

- 2026-09-15 — Tried shipping the node agent through the Image
  Factory (the bead said "factory schematic"). Ruled out: the factory
  accepts official `siderolabs/*` extensions only. Landed on: `imager`
  + our own installer image on ghcr (`talos/extensions/p0agent/
  build.sh`); content-addressed schematic ids give way to a tag we own.
- 2026-09-15 — Tried `imager --base-installer-image <factory
  installer>` to inherit its three extensions and add ours. Ruled out:
  the initramfs is rebuilt from the listed `--system-extension-image`s
  only — cp1 came up with `p0agent` alone. Landed on: list all four.
- 2026-09-15 — Tried an extension spec with `network` + `time`
  dependencies only (start as early as possible). Ruled out: the
  upgrade/reboot sequence stops `cri`/`trustd` and their reverse deps,
  then closes LUKS; an unrelated extension holding a `/var` bind mount
  hangs it. Landed on: `depends: - service: cri` (starts ~2 s after
  cri; not a real cost).

## Mesh v3 P0.1 — self-hosted iroh relay (2026-09-13)

- 2026-09-13 — Considered running the relay with its own TLS (LetsEncrypt
  or manual cert) so QUIC address discovery (UDP 7842) works. Ruled
  out for the spike: fly's TLS handler cannot proxy QUIC, the relay
  would need a DNS-01 cert shipped as a secret, and QAD only serves
  remote hole-punching, which ADR-0006 already gives up. Landed on:
  plain-HTTP relay behind fly's terminator, QAD off (ADR-0022). Revisit
  only if remote-direct becomes a goal (`0pq`).
- 2026-09-13 — Tried the owner laptop as the LAN-direct peer. Ruled
  out: corporate socket-filter extension EPIPEs LAN UDP from unsigned
  binaries (`nc`/python/C succeed, iroh fails). Landed on: two Linux
  hosts (NixOS box + Docker-on-mac musl binary); fly machine for the
  far-NAT peer.
- 2026-09-13 — Considered a fly process-group "sidecar" for the relay
  in Phase 1. Ruled out: a process group is a separate machine and
  cannot share `[http_service]` 443 with config-server — it would be a
  second entrypoint. Landed on (not built): config-server spawns
  `iroh-relay` as a child and reverse-proxies `/relay`, `/ping`,
  `/generate_204` (`5gz`).

## Unattended Windows guest on KubeVirt (2026-08-11→14)

- 2026-08-12 — Tried scripting the "Press any key to boot from CD" EFI
  prompt with a VNC keypress (vncdotool, reinstall script v1). Ruled
  out: timing window, requires VNC reachability from the operator's
  machine, not derivable in-cluster. Landed on: repack the ISO with
  Microsoft's own `efisys_noprompt.bin` as the El Torito EFI entry —
  the prompt never exists, reinstall becomes a pure API act.
- 2026-08-13 — Repack v1 extracted the ISO with xorriso. Ruled out: the
  stock ISO is UDF-bridge and the ISO9660/Joliet view xorriso reads
  cannot represent the >4GB `install.wim` — extraction died partway.
  Landed on: 7z extracts the UDF layer; xorriso stays as the rebuilder.
- 2026-08-12 — containerDisk delivery of the ISO via the ImageVolume
  path ruled out: needs k8s ≥1.35 (kubevirt#17460); feature gate
  disabled, ISO delivered by CDI DataVolume import instead.
- 2026-08-11 — Block-mode system disk ruled out: CDI's importer runs
  non-root and cannot open the raw device on a Block-mode Longhorn
  volume. Filesystem mode; revisit only alongside the virtio switch.

## Pod resolution of mesh names (2026-07-31)

- Resolved by ADR-0010 (`hostAliases` pin the issuer name to the
  siwe-oidc Service ClusterIP). Kept as a pointer only: the 70-min SSO
  outage that motivated it was a pod dialing a *mesh* address from a
  10.244.x source — nebula routes 10.42.0.0/16, so it only worked
  while the pod ran on cp1. Rule: pods talk to Services; the mesh is
  for hosts and browsers.
