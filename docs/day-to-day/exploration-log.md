# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Policy compiler `359.8.5` — grill-design (2026-09-18)

- 2026-09-18 — Target of a kind-wide grant (`{facet: apid, group:
  admins}` under `node:`) when git cannot enumerate NodeIds
  (ADR-0015) and `Attenuate` intersects `cav.target` with the node's
  `{target: [self]}` consent. Ruled out: **per-receiver targets from
  the location cache** — a cold cache after every deploy renders empty
  targets, so callers lose node access for a poll interval per
  redeploy, and a safe-to-lose cache becomes an authorization input
  through the compiler. Ruled out: **`target: group:<kind>` resolved
  against the receiver's own member cert** — a new rule in
  `authorize.qnt` + `Receiver.Member` input, buying per-node-group
  scoping nobody needs yet (keep as the upgrade path). Ruled out:
  **generalizing ADR-0024 F to nodes** (`target: wallet`, consent
  declares `[self, wallet]`) — weakens protocol rule 4 / ADR-0003 for
  no gain over the wildcard. Landed on: **`target: "*"`** — a wildcard
  sentinel in `cav.target` (precedent `aud "*"`), `intersect(X, *) =
  X`, rule 4 untouched; kind is carried by the facet name (closed,
  disjoint per kind), the recipe's `node:/gateway:/hub:` keys are
  Nickel validation structure, not compiled data. No postage: `aud *`
  is "anyone may present" (spam ⇒ postage), `target *` is "honored at
  every receiver that consented to this sovereign for this facet" —
  bounded by consent.
- 2026-09-18 — Hub relay as a grantable facet (`{facet: relay, group:
  …}` rows in `mesh-policy-v3.yaml`). Ruled out: the iroh relay is a
  keyless child whose access hook (`5gz`) sees only
  `X-Iroh-Endpoint-Id` — no bundle crosses the relay handshake, so a
  relay grant is a cert nothing can verify (the "documentation
  pretending to be enforcement" shape ADR-0017 forbids). Landed on:
  relay access is membership-implied; hub facets shrink to `hub-http`
  + the Issuer's actor facets; `relay` verb stays reserved for the M3
  envelope relay. Caveat carried to `5gz`: gating on the beat cache has
  a cold-cache trap after every deploy unless members can reach the
  hub's own iroh endpoint without the relay (`e8d`) or the hook fails
  open while cold.
- 2026-09-18 — Where the v3 recipe lives during the dual plane. Ruled
  out: **one file with both vocabularies** (`{port, proto, facet,
  host|group}`) — ICMP rows have no facet, `host: any` has no `aud`,
  `device:` has no v3 kind; a merged schema lies on one side. Ruled
  out: **derive the nebula render from the v3 recipe** — rewrites the
  plane `b2t` promised not to touch. Landed on: **two files** —
  `talos/mesh-policy.yaml` (v2, frozen, nebula) beside
  `talos/mesh-policy-v3.yaml` (the fixture promoted; the compiler's
  input; `mesh-policy-v3.ncl` validates it). Phase 4 deletes v2 and
  drops the suffix.
- 2026-09-18 — "(b) hub renders per-receiver accept tables" (bead
  title). Ruled out as stated: the recipe carries no forward address
  (ports live only in the producer's facet definition), so the hub
  cannot render `apid → 127.0.0.1:50000` for anyone. Landed on: the
  compiler package owns the **shared vocabulary** — kinds, closed facet
  set per kind, `ALPN(facet) = "talos-mesh/<facet>/v1"`,
  `AcceptTable(kind)` as the ALPN→facet map `authorize()` takes — and
  the receiver keeps `facet → forward` as its own constant. `Compile`
  is per caller identity (name + groups), unsigned, deterministic;
  the Issuer signs at `#bundle`. Compiling all groups and filtering in
  `#bundle` was not taken: same security (groupSatisfied rejects a
  grant for a group the member is not in), worse hygiene.

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
