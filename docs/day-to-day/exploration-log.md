# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Mesh v3 — outcome (2026-08-17 → 2026-09-21)

The plan in `docs/mesh-v3-iroh.md` was written 2026-08-17 and
deferred as migration-for-elegance; picked up 2026-09-03 when the
sovereign-actor build made its four components (transport, member
cert, gateway, device apps) work we were doing anyway (ADR-0016).
Gate 2026-09-16, Phases 1–4 2026-09-17 → 09-21, no kill criterion
fired. Strategy-level lessons, so the next migration does not re-learn
them:

- **Gate on a spike with kill criteria, then dual-plane, then
  per-consumer cutover, then event-based soak, then delete** — the
  mesh-v2 recipe held a second time. Every phase left the system
  working; nothing was rolled back. The soak exit was event coverage
  (one re-seal, one reboot, one remote-media session), not a calendar.
- **Tried: factory schematic for the node agent.** Ruled out at P0.3 —
  the Image Factory carries official extensions only. Landed on an
  imager-built installer we publish and digest-pin (ADR-0023). Cost:
  a public ghcr image we build is now the install image.
- **Considered: an iroh sidecar process with bespoke IPC.** Never
  built — the in-house uniffi bindgen produced a working Go package
  inside the P0.4 time-box, so the sidecar proof was skipped
  (ADR-0021).
- **Tried: TCP bridges (`irohup -bridge`) as the desktop
  presentation.** Proved the plane in Phase 1, did not scale to names;
  the fake-IP utun (ADR-0025) replaced it in Phase 2.0 and the same
  `fakeip`/`meshtun` code became the Android internals. SOCKS/PAC was
  never built.
- **Tried: receiver-side policy tables (nebula's model) on the new
  plane.** Ruled out at spike `359.2` — a second authority mechanism
  with its own sync and unseal-reconciliation; landed on caller-
  carried grants compiled from the git recipe (ADR-0017, amendment
  2026-09-18 for the four compiler pins).
- **Tried: the ephemeral policy overlay on the identity plane.** Ruled
  out 2026-09-20 (`ri3b`) — no live consumer once the app moved; the
  overlay half of ADR-0014 was deleted rather than ported.
- **Tried: the cluster endpoint on the mesh (mesh-v2's step).**
  Reversed at P2.5 — k8s is IP-native and leaves the mesh onto
  declared static LAN addresses; invariant 4's accepted wart closed
  instead of being ported. Cost: an SA-issuer rotation that bounced 14
  control-loop pods (`etzl`), and a router DHCP pool we deliberately
  do not edit (`ebis`).
- **Tried: relaying through n0's infrastructure / QAD on our relay.**
  Ruled out (invariants 3/5; ADR-0022) — remote is relay-by-default,
  as ADR-0006 already accepted for nebula; no remote-direct data point
  exists and none is sought (`0pq`).
- **Not proven on the new plane:** 4K playback / throughput through
  the relay (only P0.2 spike figures), and the parents' TV in the
  field (`4te`). The nebula-era measurements live in ADR-0006.

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

## Mesh v3 P2.2 — hub re-learning members after its own restart (2026-09-20)

- 2026-09-20 — Considered the node watching its **home-relay
  connection** (`iroh-ffi` `WatchHomeRelay`) as the "hub died" signal:
  the relay is the hub process's child, so a redeploy restarts it.
  Ruled out by spike: the 1.1.0 binding's `watch_*` methods are sync
  fns that `spawn` outside the tokio runtime (panic: "no reactor
  running", `watch.rs:85`) *and* map `RelayStatus` to `url()` only,
  dropping `is_connected()`; `Addr().RelayUrl()` never blinks across a
  relay restart either. Fixing needs a nix patch + Go regen. Landed
  on: the pooled QUIC connection the beat leaves — iroh keep-alives
  it (5 s), `Closed()` fires 32 s after the hub is SIGKILLed (child-
  process probe, relay up or down). Decision `z2go`.
- 2026-09-20 — Considered persisting the hub's witnessed-member cache
  on a fly volume, a shorter `DefaultBeat`, and the relay access hook
  (`5gz`) reporting connected NodeIds. Ruled out: fungibility (`gar`)
  / brute force / ids-not-names respectively. `pu9q`'s rejections
  (short `/.well-known` polling, hub push channel) carry over; the one
  poll admitted is against a *sealed* hub, bounded to the sealed
  window.

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

## Mesh v3 P2.4 — the app's build host (2026-09-20)

- 2026-09-20 — Considered keeping the Android APK in CI now that the
  app links iroh. Ruled out for now: the AAR needs
  `libiroh_ffi.a` for `aarch64-linux-android`, and that build is impure
  (unfree NDK) and ~1 h cold — a stock runner would pay it on every
  run. Landed on: build on the NixOS box (`android/shell.nix`),
  `android/publish.sh` to the same rolling release, workflow
  dispatch-only. Revisit if the cross build can be pushed to a binary
  cache CI can read — the rest of the workflow (gradle, release upload)
  still works as written.
- 2026-09-20 — Considered adopting the P0.2 spike's `iroh-go/mobile`
  package as the app's core (it already did netstack + fake IP + iroh).
  Ruled out: it is a one-peer spike with no certs, no name map and its
  own copy of the fake-IP dialect. Landed on: the app binds
  `config-server/mobile` over the same `nodeagent` + `meshtun` the
  desktop daemon runs, so there is one zone rule and one pool; the
  spike package is superseded and can be deleted.
