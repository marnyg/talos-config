# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Mesh v3 P1.2a — hub actor cut (2026-09-16, `359.8.2.1` grill-design)

- 2026-09-16 — Considered all hub actors (Issuer, Enroll, Relay,
  Provisioner) sharing the one `hubkey` the unseal `speak-as` names.
  Ruled out: in the protocol an actor *is* a keypair, so shared
  `hubkey` = one actor with facets, and "promote to a process is a
  transport change" (decision `vl4`) would mean copying a private key
  across processes; `delegable: false` on the `speak-as` also forbids
  re-delegating it to sibling keys. Landed on: `hubkey` is the
  **Issuer's** key alone; Enroll and Provisioner hold their own
  per-process keys with no wallet delegation, consented to by the
  Issuer at boot; Relay is a transport component, not an actor.
- 2026-09-16 — Grants to hub facets (`#renew`, `hub-http`, `#publish`)
  naming the current `hubkey` as `target`. Ruled out: `VerifyChain`
  rule 4 is literal (`Target ∋ receiver`) and ADR-0018 rotates `hubkey`
  every deploy, so the first beat after a redeploy deadlocks — the
  `#renew` grant names a dead key and fetching a fresh one is itself a
  hub facet. Also ruled out: a bootstrap facet admitting a member cert
  alone (the "any cert I signed authorizes asking" special case the
  glossary forbids for `#renew`) and a seed-derived stable `hubkey`
  (ADR-0018). Landed on: the receiver answers for principals it holds
  a live `speak-as` from — hub facets are the **sovereign's** facets
  (`target: wallet`), served by whichever hot key holds the unseal
  (`kau`, model first).
- 2026-09-16 — Considered dialing the *wallet* (`Dial(eth:…, hints)` with
  a new `iroh:id=` hint, `to.target: wallet`, `reach-me-at` for the
  wallet signed under the `speak-as`). Ruled out: an `ed:` actor id
  *is* the iroh `EndpointId` (`iroh-transport/nodeid.go`), the TLS pin
  and the Reply `from == to.target` check both hang on that, and an
  `eth:` id has no endpoint by design. Landed on: the hub process's
  transport identity is the **Issuer's `hubkey`**; members dial and
  address `hubkey` (learned from the `speak-as` they hold anyway), only
  the *grant* names the wallet; Enroll/Provisioner reach the Issuer over
  the in-memory transport; the cold-cache fallback after a deploy is a
  WAN HTTPS document serving the current `speak-as` alone.

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
