# ADR-0002: Nebula mesh replaces the WireGuard hub-and-spoke control channel

- Status: Accepted _(2026-07-29: gate spike passed — see Confirmation)_
- Date: 2026-07-29

## Context and Problem Statement

The wg0 control channel is hub-and-spoke through fly.io: all admin/media
traffic hairpins through the hub even on the home LAN; there is no story
for non-admin devices (phones, TV); no direct peer-to-peer paths exist.
NAT traversal for kernel WireGuard on Talos nodes requires a privileged
node agent that punches holes and rewrites endpoints — verified against
NetBird's source: thousands of lines of connection-state glue, a forked
pion/ice, and a stateful management plane.

How do we get direct peer paths and general device membership without
violating the repo's trust model (stateless, wallet-rooted, single
public entrypoint, git as source of truth)?

## Decision Drivers

- Direct paths: LAN traffic stays on LAN; remote streaming at real bitrates.
- Phones/Android TV join the network.
- Invariant 1 (amended this session): identity **and membership** must be
  re-derivable from git + owner-held keys, zero runtime re-enrollment.
- Mesh must be strictly post-bootstrap (invariant 4).
- Single public entrypoint (invariant 5).

## Considered Options

- **Nebula** (chosen) — membership = cert signed by wallet-derived CA; no
  controller, no registry, no database. Sidero system extension exists.
  Official mobile apps with standalone-site support. NAT traversal one
  tier below Tailscale (no DERP-class network; relay = designated mesh
  member). Userspace on nodes (vs kernel wg0).
- **NetBird (+ Zitadel/Ory IdP)** — best-supported traversal, kernel WG,
  Talos extension. Rejected: management DB is load-bearing,
  non-derivable state (server-generated setup keys); DB loss ⇒ fleet
  reprovision. Requires an OIDC IdP (its own database).
- **Headscale/Tailscale** — best traversal + client polish. Rejected:
  same statelessness objection (preauth keys/peers are runtime DB records).
- **Self-hosted SIWE OIDC issuer (hub facet or spruceid/siwe-oidc)** —
  obsoleted: nebula consumes certs, not tokens. Permitted-but-unused;
  may return for app SSO as a separate decision.
- **ZeroTier / Netmaker / innernet / wesher / KubeSpan** — L2 semantics
  buy nothing / stateful + security history / no NAT traversal /
  cluster-internal only.
- **Keep wg0 + LAN-endpoint shortcut in wgup** — the fallback if any
  kill criterion fires (~20 lines, covers the LAN-hairpin pain only).

## Decision Outcome

Chosen: **nebula replaces wg0 wholesale.** Nodes run the nebula system
extension (certs+config injected at config-compose, HKDF-derived from
the wallet master); admin/appliance devices enroll via the wgup pattern
(`nebup`); the hub embeds `slackhq/nebula/service` (userspace, gvisor)
as the **sole lighthouse + relay** — SPOF accepted, multi-lighthouse
escape hatch retained. Public surface: hub HTTPS + UDP 4242.

Migration: (0) prerequisite — the `apply` serve-time-stripping bug
(landed 2026-07-29); (1) gate spike — `nebula/service` on fly:
lighthouse+relay+dial without TUN (passed 2026-07-29); (2) phase 1 —
dual overlay, dogfood ≥1 week measuring direct-vs-relay rate and
throughput (task uuid 1afafb50); (3) phase 2 — certSANs (name+IP),
endpoint moves to nebula IP, wg0 stripped, wg* hub code removed
(task uuid dc04e3e8).

Kill criteria (any ⇒ revert to wg0 + LAN shortcut): spike fails; punch
rate mostly-relay on real NAT pairs; direct LAN path can't sustain
~80 Mbps; mobile/TV UX bad enough those devices stay off the mesh.

Full design record: [`../../mesh-v2-nebula.md`](../../mesh-v2-nebula.md).

### Consequences

- Invariant 1 amended (OIDC-as-protocol permitted when self-hosted +
  wallet-derived; membership statelessness now explicit) — `vision.md`
  updated 2026-07-29.
- Cluster endpoint migrates off the wg0-derived IP (careful sequence;
  prior renumbering incident applies).
- Nebula extension ⇒ new factory schematic ⇒ `talosctl upgrade` (no wipe).
- Nodes trade kernel WG for userspace nebula (throughput bar in kill
  criterion 3 guards this).
- Open: cert revocation/expiry policy before shared-space devices
  enroll (task uuid 888aac0f); nebula CIDR + DNS zone naming.
- Spike finding: nebula's built-in lighthouse DNS (`serve_dns`) binds a
  kernel socket and cannot work on the TUN-less embedded hub (verified:
  overlay UDP/53 is dropped by the netstack). Phase 1 ports the wgdns
  pattern (git-derived zone, gonet UDP on the netstack) instead — a
  small variant of the TCP-only `nebula/service` package is required.
- Spike finding: `nebula-cert` ≥1.10 emits V2 certs; nebula ≤1.9 cannot
  parse them. Pin node extension and client versions ≥1.10 (hub embeds
  1.11.0).

### Confirmation

Gate spike (2026-07-29, scratch fly app, all three checks passed):
`listen.host: fly-global-services` resolves natively (no custom bind
code); laptop→hub handshake 22ms and banner served from netstack :80;
hub→peer netstack TCP dial 39ms; relay verified with a UDP-locked peer
(direct paths blocked by iptables ⇒ handshake `from="…:4242 (relayed)"`,
ping 0% loss). Kill criteria 2–4 (punch rate, throughput floor,
mobile/TV UX) are measured during the phase-1 dogfood window and can
still revert this decision.
