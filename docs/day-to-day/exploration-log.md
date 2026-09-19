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
  LAN-direct measured at home the same evening on the phone (97 Mbps
  avg, PASS) — the Shield was not needed for the bar and gets its turn
  in the parents'-TV deployment (`4te`).
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
  `nal-hrd=cbr`. Step 7 (the same run through cp1) was then **dropped
  by owner ruling at the gate**: it re-proves P0.3's forwarding only,
  and cluster Jellyfin has no media while w1 is down.
- 2026-09-16 — Gate ruling on cp1's undeclared install image (`5cz`):
  considered upgrading back to the factory `6a9acc…` for a clean
  baseline. Ruled out: the Image Factory carries official extensions
  only (P0.3), so Phase 1.3 would immediately rebuild the imager chain
  — a round trip. Landed on: declare the imager image in
  `minipc.yaml`, digest-pinned; the supply-chain cost (a public ghcr
  image we build is now cp1's install image) is why the digest, not
  the tag, is the pin.

<!-- 2026-09-16: §P0.1 (relay) and §P0.3 (Talos extension) pruned —
     resolved by ADR-0022 and ADR-0023. Rulings live there and in
     mesh-v3-iroh.md §P0.1/§P0.3; recover from git history if needed. -->

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

## Mesh v3 P2.1 — admin CLI paths on the tun (2026-09-19)

- 2026-09-19 — Considered `nodes: [cp1]` / `-n cp1.mesh.internal` /
  `-n 127.0.0.1` for talosconfig. Ruled out from Talos source: apid on
  a control plane dials every `-n` target as `<target>:50000` with SNI
  (never local), so the target must be self-resolvable *and* a SAN —
  only the hostname is. Considered pinning `hostname: cp1` live.
  Ruled out: renames the k8s Node on the only control plane and
  orphans Longhorn's single-replica volumes bound to `talos-wu6-eib`
  (`t7b2` at reinstall). Landed on: the generated hostname, honestly.
- 2026-09-19 — Considered leaving `nodes:` empty so the endpoint is
  the node. Ruled out: talosctl refuses nearly every command without
  nodes.
- 2026-09-19 — Considered `apply` dialing each node by
  `<name>.mesh.internal`. Ruled out: needs every node on the identity
  plane (w1 is not). Landed on: the control plane's mesh name as the
  endpoint, `-n <hostname>` proxied by apid via `resolveMemberNames`
  — the talosconfig shape.
- 2026-09-19 (later, `qb5q`) — **The constraint behind the `apply`
  ruling above has lifted**: w1 now runs the agent, so every node is
  on the identity plane and per-node `<name>.mesh.internal` dialing is
  viable. Not re-opened — the apid-proxy shape still has the smaller
  blast radius (one endpoint, works for any future agentless node, and
  matches talosconfig) — but the "ruled out" reason is no longer the
  reason. Re-read this before assuming the option is closed.
- 2026-09-19 — Considered putting the hub in the name map so
  `hub.mesh.internal` resolves like a member. Ruled out: the map is
  witnessed member certs and the hub holds none; a synthetic entry
  would make the Issuer a member of its own network. Landed on: the
  daemon resolves `nodeagent.HubName` from the hub record it already
  keeps for the beat.
