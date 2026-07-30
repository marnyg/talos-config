# Deployed state

Where the running system stood when last verified. Rehomed from the
legacy `docs/handover.md` so it survives that file's deletion. Facts here
decay — each block carries the date it was last confirmed. If you verify
or change something, update the date.

## Cluster — _last verified 2026-07-24_

- Single control plane `b0:41:6f:15:3b:8f`, Talos v1.12.6, k8s v1.32.3.
- LUKS2 on STATE + EPHEMERAL.
- Cluster endpoint `https://10.99.0.54:6443` — the node's **derived
  tunnel IP**, deliberately not its DHCP LAN address (invariant 7; DHCP
  handed out four leases in one day before this moved).
- Media stack 6/6 Running. SealedSecrets (`newshosting`, `nzbgeek`)
  unseal via the inlineManifest-provisioned key pair.
- Admin access is tunnel-only: `talos/talosconfig` (local, gitignored)
  points at 10.99.0.54; NodePorts serve on wg0 (Jellyfin
  `10.99.0.54:30096`).
- _2026-07-29_: upgraded in place to the nebula schematic (see Mesh
  below). EPHEMERAL survived, as expected since Talos 1.5 — etcd and
  `/var/media` intact, media stack unaffected.

## Mesh (nebula, phase 1) — _last verified 2026-07-29_

Both overlays run at once; **wg0 still carries production traffic**
(talosconfig, KMS, auto-bootstrap, `nix run .#apply`). The mesh is on
trial — see the kill criteria in `../../mesh-v2-nebula.md`.

- Hub is lighthouse + relay on `10.42.0.1`, fly udp/4242, same dedicated
  IPv4 as wg0 (`213.188.219.215`).
- cp1 runs `siderolabs/nebula` 1.10.3 from factory schematic
  `011ccccdcfa98314d2550cb33b56426be8f45553fce129a1e6124de63e9f1598`,
  service `ext-nebula`, interface `nebula0`, overlay `10.42.218.125/16`.
- Verified handshake in both directions, node WAN endpoint seen by the
  hub as `80.212.67.203:4242` — so NAT mapping is visible and direct
  punching is possible.
- Node inbound firewall: icmp from any member, everything from cert name
  `hub`, everything from group `admins`, and — since the 2026-07-29
  re-apply — Jellyfin's NodePort (tcp/30096) from group `media`.
  Machines are not in that list.
- **Mesh certSANs live** _(phase 2 step 1, verified 2026-07-31)_: apid
  and kube-apiserver certs carry `cp1.mesh.internal` + `10.42.218.125`.
  `talosctl -e 10.42.218.125` and the kube API verify over the mesh.
  `-e cp1.mesh.internal` still needs the laptop resolver to split-DNS
  `.mesh.internal` → `10.42.0.1` (the hub zone answers it; local
  resolution is the missing piece).
- The CA fingerprint is re-derived on every unseal and is the value
  members pin; it was `b881d6ff…` on the 2026-07-29 unseal. A *different*
  fingerprint after an unseal means a different wallet signed — not a
  rotation.

### Phase-1 measurements — _2026-07-29, laptop ↔ cp1 on the same LAN_

- **Kill criterion 2 (LAN case): passes.** Steady state 0% loss, min
  1.785ms / avg 3.3ms. The hub is ~20ms away, so sub-20ms RTT is proof
  the path is direct and not relayed. First tunnel takes ~6s to converge
  (drops, then ~27ms relayed, then direct).
- Mesh DNS answers clients: `cp1.mesh.internal` → `10.42.218.125`, the
  same address as the cert. Out-of-zone → REFUSED, in-zone unknown →
  NXDOMAIN.
- Jellyfin reachable over the overlay: 302 in 5.4ms on
  `10.42.218.125:30096`.
- **Kill criterion 2 (remote case): RESOLVED 2026-07-30 — relayed, and
  the criterion is AMENDED not fired (ADR-0006).** NAT behaviour was
  classified at both ends by STUN binding requests from one socket to
  several distinct destination IPs (same external port ⇒
  endpoint-independent "cone"; differing ⇒ symmetric):

  | Endpoint | NAT behaviour | Punch |
  |---|---|---|
  | Home (cp1) | endpoint-independent + port-preserving (cone) | not the blocker |
  | Cellular hotspot | symmetric CGNAT | relayed |
  | Office Wi-Fi | symmetric, **random** ports (3 dests → 19586/51810/64036) | relayed |

  Punching needs one predictable side. Home is predictable; neither
  remote network is, and the office NAT's random allocation rules out
  port prediction too. So remote relay is a property of the networks,
  not of our config or our router — wg0 would not have punched either.
  Hence parity, not regression. The office run's validity was confirmed
  before drawing conclusions: Tailscale was up but split-tunnel with no
  exit node, and `route get` for both cp1's WAN and fly showed egress on
  the physical `en0`.

  **Pre-flight for any future punch test** (portable — the old `ip link`
  check silently no-ops on macOS, where these tests actually run):

  ```bash
  netstat -rn | head -5              # default route on a physical NIC?
  route get <peer-wan-ip>            # macOS: "interface:" must not be utun/tun
  ip route get <peer-wan-ip>         # Linux equivalent
  ```

  Any overlay (wg0, Tailscale exit node, corporate VPN) that carries the
  route to the peer poisons the measurement — nebula will use it as
  underlay and hairpin.

  <details><summary>Original 2026-07-29 hotspot measurement</summary>
  Laptop on a phone hotspot (carrier CGNAT + tether NAT) against the
  home router: handshake completed `from="213.188.219.215:4242
  (relayed)"`, no roam to direct over several minutes, and a packet
  capture on the Wi-Fi interface showed **all** overlay traffic
  laptop↔fly, zero packets to the home WAN. Steady-state RTT ~59ms min
  (= 40ms hotspot→fly baseline + fly→home leg), i.e. exactly today's
  wg0 hairpin. Not a config gap: `punchy.punch`/`respond` are on for
  both laptop and node, and the hub's punch rendezvous is unconditional.
  **Caveats bounding the result:** (a) the first two attempts were
  invalidated by wg0 being up — nebula used the wireguard tunnel as
  underlay (`from="10.99.0.54:4242"`) and hairpinned through fly, so
  any dual-overlay measurement with wg0 up is poisoned; (b) this pair
  stacks tether NAT on carrier CGNAT — blame (home NAT vs cellular
  CGNAT) is unresolved, and a punch test from ordinary foreign Wi-Fi
  (café/office) would discriminate. Cellular CGNATs are typically
  symmetric, which no amount of punching defeats.
  </details>
- **Kill criterion 3 (throughput): passes.** iperf3 against a temporary
  NodePort pod, laptop ↔ cp1:

  | Direction | Mesh | Bare LAN | Ratio |
  |---|---|---|---|
  | laptop → node | 229 Mbit/s | 326 Mbit/s | 70% |
  | node → laptop | 168 Mbit/s | 182 Mbit/s | 92% |

  2.1–2.9× the ~80 Mbit/s 4K-remux floor. Userspace nebula on the node
  costs 8–30% versus the raw LAN path. Caveat: the 326 Mbit/s baseline is
  low for wired gigabit, so the underlay was probably Wi-Fi — the ratio
  is the meaningful number, not the absolutes. The LAN reverse run had 93
  retransmits against the mesh's 4 and came out *slower*, which is
  underlay noise rather than the overlay winning.

## Hub on fly — _last verified 2026-07-29_

- `fly secrets list` is **empty**. Everything derives from the wallet
  signature at unseal: wg keys (server/machine/admin), tunnel IPs, KMS
  seal keys, recovery passphrases, the age identity that decrypts
  `clusters/**/*.age` into tmpfs, and — since 2026-07-29 — the nebula
  mesh CA and all mesh identities.
- Public age recipient is committed at `talos/age-recipient.txt`
  (re-derive with `recover -age-recipient -sig <unseal-sig>`). The SSH key
  remains a break-glass recipient.
- An unseal that cannot decrypt the secrets fails loudly rather than
  serving broken configs.

## Disk encryption posture — _decision, closed 2026-07-24_

Slot 0 is the network KMS, slot 1 a derived static passphrase stored in
plaintext META.

- **Slot 0 is dormant at boot in practice**: early-boot DNS loses the
  race to the KMS dial every time so far. Accepted rather than fixed.
- **Accepted consequence**: encryption protects against disk
  disposal/RMA only, not against an attacker with the running machine.
- A sealed hub therefore does **not** block reboots — slot 1 boots the
  node unattended. Only provisioning and config refetch need an unseal.
- Going KMS-only would first require break-glass tooling for slot-0
  blobs.

> Now recorded properly in **ADR-0004**, including the consequence that
> matters most: wipe META before a *machine* (not just a disk) leaves the
> owner's hands, because the slot-1 passphrase travels with it.
