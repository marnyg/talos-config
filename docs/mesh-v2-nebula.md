# Mesh v2 — Nebula replaces wg0

_Design record, 2026-07-29. Product of a grill-design session; decisions
below are confirmed unless marked **open**. Tasks (by uuid — short ids
renumber): spike done/passed 69138146, phase 1 fca5be68, phase 2
1afafb50, revocation thread dc04e3e8, ENS commitment idea 888aac0f.
This doc will be decomposed into
the docs scaffold (ADR, invariants, exploration log) when that lands;
until then it is authoritative for the mesh design._

## Problem

The current WireGuard control channel (`wg0`) is hub-and-spoke through
fly.io: every admin byte — including Jellyfin streamed from a laptop
ten meters from the node — hairpins through the hub, metered and
latency-taxed. There is no story for non-admin devices (phones,
Android TV), and no direct peer-to-peer path anywhere. NAT traversal
for kernel WireGuard on Talos nodes would require a node-side agent
that punches and rewrites endpoints (the NetBird model), which fights
Talos's declarative reconciliation and imports a stateful management
plane.

## Drivers

1. **Direct paths for all admin/media traffic** — LAN traffic stays on
   LAN; remote streaming viable at real bitrates instead of fly-relayed.
2. **Non-admin devices join the network** — phones, Android TV.
3. **Net simplification** — one overlay, one derivation tree; the hub
   keeps all its roles, the design keeps all its properties
   (statelessness, single entrypoint, wallet root).

## Decision

**Nebula replaces wg0 wholesale.** Nodes run the Sidero nebula system
extension; admin devices and appliances run nebula clients (official
iOS/Android apps, bare binary elsewhere); the hub embeds nebula
in-process (`slackhq/nebula/service`, gvisor netstack, no TUN, no
root) and serves as the sole **lighthouse + relay**, alongside its
existing config-server / KMS / enroller duties.

Chosen over the alternatives because nebula is the only mesh whose
trust model matches ours: membership is *holding a cert signed by a CA
we derive from the wallet* — no controller, no peer registry, no
database anywhere. NAT traversal is a tier below Tailscale's (no
DERP-class fallback network; relay is a designated mesh member — ours
is the hub). That trade is accepted.

## Amended trust invariant (supersedes vision.md wording)

> No identity or membership state outside git + owner-held keys: all
> authentication reduces to offline verification against the wallet,
> and the entire network (peers, certs, addresses) must be
> re-derivable with zero runtime re-enrollment. Self-hosted token/cert
> issuance is permitted only when issuer keys are wallet-derived.

Notes:
- "Stateless" covers **identity and membership**. This is what killed
  NetBird and Headscale (see Ruled out).
- OIDC *as a protocol* is no longer banned — only OIDC-shaped trust
  dependencies on third parties. With nebula, no OIDC issuer is needed
  at all; a self-hosted, wallet-derived issuer remains a permitted
  future option for app SSO (separate decision, not part of this plan).
- The north star is intact: a machine joins with exactly one human
  act — a wallet signature at provision time.

## Architecture (end-state)

```
wallet sig (EIP-191 over frozen message)
  → HKDF master (as today, wgderive pattern)
    → nebula CA key                        (new derivation domain, e.g. talos-config/nebula/v1/ca)
    → per-machine cert+key                 (…/machine/<mac>, minted at config-compose)
    → per-device cert+key                  (…/device/<name>, minted at enrollment)
    → hub identity (lighthouse/relay)      (…/hub)
```

- **Hub (fly.io)**: config-server + KMS + enrollment (unchanged) +
  embedded nebula lighthouse/relay via `nebula/service`, UDP 4242 on
  `fly-global-services`. Hub dials nodes through the embedded netstack
  (auto-bootstrap, /status probes) — the wgstack role, ported.
- **Nodes**: nebula system extension (factory schematic), config +
  cert injected at compose time — same trust chain as today's wg0 key
  injection. Cluster endpoint moves to the node's nebula IP.
- **Admin devices**: `nebup` (the `wgup` pattern) — wallet signs a
  challenge, hub derives cert + site config, returns it. Re-running
  after a device wipe re-derives the same identity.
- **Headless/appliance devices** (TV): admin-mediated enrollment
  (`nebup -name androidtv -print`, transfer config into the app).
  RFC 8628 device flow (existing `deviceflow.go`) reserved for devices
  that can run an enrollment client. The wallet never touches the
  device; SIWE authorizes issuance — same pattern as PXE machines.
- **DNS**: the hub serves the on-tunnel zone over the nebula netstack
  — the wgdns pattern ported (git-derived zone, gonet UDP listener).
  **Not** nebula's built-in `serve_dns`: that binds a kernel socket and
  requires a TUN, so it cannot receive overlay queries on the embedded
  hub (verified during the spike — overlay UDP/53 is dropped by the
  netstack; the stock `service` package is TCP-only and needs a small
  variant). Zone stays a pure function of git — stronger than
  lighthouse DNS, which only knows reported hosts. CertSANs carry
  **both** nebula name and IP. Mobile-app DNS push is unverified — fold
  into the phase-1 mobile UX check (kill criterion 4).
- **Public surface**: hub HTTPS (config/enroll/status) + UDP 4242.
  Single hostname, achieved by construction — nothing to reverse-proxy.
  No external STUN needed; lighthouses do reflexive discovery.

## Failure matrix

| Failure | Effect | Surviving access |
|---|---|---|
| Cluster down | Mesh unaffected (Talos extension, not a k8s workload) | talosctl over mesh to repair |
| Fly down | Config/enroll/KMS down (as today; slot-1 keeps boots working). No new discovery/relay; **established tunnels persist** (p2p post-handshake) | On-LAN: full, via `static_host_map` pinning node LAN addrs (lighthouse-less). Remote: cached endpoints until NAT rebind |
| Hub redeploy → sealed | Hub mesh roles down until wallet unseal (thread 27 precedent). Device/node certs unaffected | Everything p2p keeps working |
| Node reprovision | Provisioning is HTTPS + device flow — zero mesh dependency | Unchanged |
| Fly down + admin remote | The one lost case: no fresh punch until fly returns | None (accepted, see below) |

**Structural rule (invariant-grade): the mesh is post-bootstrap.
Nothing on the provisioning or recovery path may depend on it.**

**SPOF accepted**: fly is the sole lighthouse+relay. A second
lighthouse means a second public entrypoint (router port-forward pins
the home IP into every device config) or a second VPS (new infra, new
unseal story). Nebula supports multiple lighthouses natively, so this
is reversible in an afternoon if reality disagrees.

## Migration plan

**Prerequisite: task #30** — the `apply` serve-time-stripping bug.
Every migration step is a config change; that bug wedges nodes exactly
during config changes (cp1 incident). It lands first.

**Gate — spike**: embed `nebula/service` in a scratch fly app;
verify lighthouse + relay work without TUN, `listen.host` binds
`fly-global-services`, and the netstack can dial a laptop peer.
~1 day. **Fail → whole plan reverts to "keep wg0 + 20-line LAN
shortcut in wgup"; nothing else is built.**

**PASSED 2026-07-29** (all three checks; evidence in ADR-0002
§Confirmation): native `listen.host` DNS resolution covers the
fly-global-services bind (no custom bind code); handshake 22ms;
hub→peer netstack dial 39ms; relay verified against a UDP-locked peer.
Findings: `nebula-cert` ≥1.10 emits V2 certs (nebula ≤1.9 can't parse
— pin versions); stock lighthouse `serve_dns` unusable on the embedded
hub (see DNS bullet above).

**Phase 1 (uuid fca5be68)** — dual overlay:
- New factory schematic with nebula extension; `talosctl upgrade`
  (no wipe, `/var/media` survives).
- CA + cert derivation in the hub; compose-time injection of extension
  config; `nebup` enrollment.
- Run nebula beside wg0. ~~Android TV sideload check~~ _(dropped
  2026-07-30 — TV case decided out of criterion 4, see below)_.
- **Phase-1 exit checks** _(replaced the ≥1-week dogfood window,
  2026-07-30; **all three passed later the same day**)_. Calendar time
  was a proxy for resilience events that ordinary use would eventually
  trigger; naming them directly was both faster and stricter — and in
  fact all three ran within hours of being named:
  1. **Node reboot** — ✓ 2026-07-30: `talosctl reboot`; `ext-nebula`
     auto-restarted, nebula0 back at 10.42.218.125, hub handshake
     seconds after start. Bonus evidence: cp1's LAN lease drifted
     10.0.0.20→.30→.31 the same day while the mesh address never moved
     — the phase-2 endpoint argument demonstrating itself.
  2. **Hub re-seal** — ✓ 2026-07-30, exercised **twice** (TV build
     deploy, then the /status UI deploy): sealed window drops the
     mesh, cp1's handshake times out, and it reconverges unaided one
     retry cycle (~60s) after the wallet unseal. Note: the hub re-mints
     its own leaf per unseal (fingerprint rotates, issuer stable).
  3. **Roaming reconvergence** — ✓ 2026-07-30: laptop hopped
     fixed-line → cellular with nebup untouched; hub hostmap flipped
     endpoint, tunnel stayed up, laptop→cp1 answered relayed at
     ~71–82ms steady (ADR-0006 parity; 589ms first packet is relay
     setup).

  Criteria 1–3 are settled (1 spike, 2 amended per ADR-0006, 3
  measured). **Criterion 4 is half-decided (2026-07-30): the TV case is
  dropped and no longer blocks phase 2.** The official Mobile Nebula app
  is unusable on Android TV (Flutter buttons ignore d-pad clicks,
  DefinedNet/mobile_nebula#148; no camera for QR import), so the home
  TV is served LAN-direct — consistent with ADR-0006's scoping — and a
  bespoke mesh client (thin Kotlin/leanback APK bundling the nebula
  gomobile AAR, task 2e1bef85) is deferred until a remote-TV need
  actually exists. The `/mesh/tv` device flow stays: it serves media
  *phones*, whose official app imports externally-derived private keys.
  **The phone half was measured later the same day (2026-07-30) and
  passes** (decision c4f07507): a phone was declared as a media device
  and self-served through the `/mesh/tv` device flow end to end. The
  Mobile Nebula import UX is bad — but not bad enough to keep the phone
  off the mesh, which is the bar criterion 4 sets. Driver 2 stands for
  phones; a UX improvement is filed, not blocking. With that, **all
  four criteria are settled and phase 2 is unblocked.**
- The exit checks are the evaluation window — the go/no-go for phase 2
  lands before anything irreversible.

**~~Open decision~~ (2026-07-30, resolved same day)** — with driver 1
narrowed to LAN-direct (ADR-0006), if criterion 4 also goes unmeasured
then phase 2 is justified by LAN-direct plus consolidation alone. That
was the risk of deciding by attrition. It didn't happen: the TV half was
dropped out loud (decision 3dfef644), and the phone half was **measured
and passed** (decision c4f07507) — enrollment via the device flow
worked, the import UX is poor but workable at a 90-day cadence. Phase 2
proceeds on drivers 1 (LAN), 2 (phones), and 3 (consolidation).

**Phase 2 (uuid 1afafb50)** — cutover, in order. **COMPLETE
2026-07-30**: steps 1–2 landed 2026-07-31 (sic — tracker dates),
step 3 landed 2026-07-30 in two commits (`2fd66df` control channel
onto the mesh, `c9c7360` wg0 deleted; ADR-0007 supersedes ADR-0003,
invariant 5's dual-overlay exception closed).
1. Add nebula name + IP to certSANs (additive, safe). ✓
2. Move cluster endpoint to the node's nebula IP; re-point
   talosconfig/kubeconfig. ✓
3. Strip wg0 from compose; remove wg* code from the hub ✓
   (wgbind/wgstack/wgdns/wgenroll/wgup deleted — the derivation
   pattern survives as `masterderive` + `nebderive`; offline
   break-glass moved to `cmd/recover`).

## Kill criteria (any one fires → stop, keep wg0, do the LAN shortcut)

1. **Spike fails** — `nebula/service` can't lighthouse+relay+dial on fly.
2. **~~Punch rate~~ → Parity + LAN directness** _(amended 2026-07-30,
   ADR-0006)_. Original form: real NAT pairs mostly fall back to relay →
   we've rebuilt today's topology with more moving parts. **Amended
   form:** remote paths must be *no worse than wg0* (relayed via the hub
   is parity, not failure), **and** same-LAN paths must be direct; if LAN
   traffic hairpins, the criterion fires.

   Why amended: measured relay on both remote networks tested, but NAT
   classification showed home is endpoint-independent + port-preserving
   (cone) while both remote networks are symmetric — the office one with
   random port allocation, which forecloses port prediction. Punching
   needs one predictable side; neither remote network provides it, and
   wg0 would not have punched either. The original criterion's implicit
   causal claim (that relay indicts our side) was disproven, so firing it
   on the symptom would revert a strictly-better system.
3. **Throughput floor** — direct LAN path must trivially sustain
   ~80 Mbps (4K remux); remote direct must measurably beat today's
   fly-relay. Nebula is userspace on nodes (kernel wg0 today) — this
   should clear by an order of magnitude; if not, something is wrong
   enough to stop.
4. **Mobile/TV UX** — if enrollment/operation is miserable enough that
   those devices would stay off the mesh, driver 2 evaporates; rerun
   the calculus on drivers 1+3 alone. _TV case dropped 2026-07-30
   (decided out, not fired): no usable client exists for Android TV,
   the home TV needs only LAN, and a bespoke client is deferred — the
   criterion now covers phones only._ _Phone case measured 2026-07-30:
   PASSES (decision c4f07507). Device-flow enrollment worked end to
   end; Mobile Nebula import UX is bad but the phone is on the mesh —
   below the "would stay off" bar. Criterion settled; a UX improvement
   task exists (+later)._

## Ruled out (with reasons)

| Option | Why not |
|---|---|
| Node-side hole punching for kernel WG (NetBird-style agent) | Needs privileged agent; fights Talos declarative reconciliation; NetBird's own source shows the glue is thousands of lines of scar tissue |
| NetBird (+ extension) | Membership DB is load-bearing, non-derivable state (server-generated setup keys); DB loss ⇒ fleet reprovision. Violates statelessness |
| Headscale/Tailscale | Same objection: preauth keys/peers are runtime DB records |
| Zitadel / Ory | Whole premise dissolved with NetBird; user-database IdPs are off-model regardless |
| OIDC facet on the hub (self-hosted SIWE issuer) | Not needed — nebula consumes certs. Permitted-but-unused; may return for app SSO |
| siwe-oidc (spruceid) | Same — obsoleted by dropping the OIDC requirement |
| ZeroTier / Netmaker / innernet / wesher | L2 semantics buy nothing / stateful server + security history / no NAT traversal (sidegrades) |
| KubeSpan | Node↔node within cluster only; nodes share a LAN; no admin devices |
| Second lighthouse | Second public entrypoint or new infra; SPOF accepted instead (reversible) |
| Greenfield reprovision as migration | EPHEMERAL wipe loses `/var/media`; dual overlay costs nothing |

## Open items

- **Revocation/expiry policy** (thread uuid dc04e3e8): nebula has no CRL —
  blocklist-by-fingerprint distributed via git-managed configs, or
  short-lived device certs. Decide before enrolling shared-space
  devices (TV). `nebderive` keeps this option open: leaf validity is
  caller-supplied, so short-lived certs need no derivation change.
- **Extension to explore — ENS commitment layer** (idea uuid 888aac0f):
  text records on the
  owned .eth name carrying the nebula CA fingerprint + hub endpoint, as
  wallet-anchored out-of-band discovery/verification. Strictly additive:
  never in an auth path (invariant 1), never publish mesh IPs on-chain.
  On-chain resolution as a *primary* naming path is ruled out (chain
  RPC / gateway dependency).
- One-VPN-slot on mobile: nebula competes with any other VPN app on
  the device (platform limitation, not nebula's).
- Speculative: implementing Defined's managed-enrollment API subset on
  the hub for polished in-app enrollment — only if the copy-paste UX
  actually hurts.

## Settled 2026-07-29 (was open)

- **Overlay CIDR: `10.42.0.0/16`**, hub/lighthouse `10.42.0.1`. Clear of
  the LAN (`10.0.0.0/24`), the pod subnet (`10.244.0.0/16`) and the
  service subnet (`10.96.0.0/12` — which spans 10.96–10.111 and so
  already contains wg0's `10.99.0.0/24`). A /16 rather than a /24
  because certs bake the address: over a /24's 253 slots the birthday
  bound hits ~50% at only ~19 peers, and phase 1 adds phones + TV.
  Not `100.64.0.0/10` — a CGNAT hotspot underlay (kill criterion 2)
  lives there.
- **DNS zone: `mesh.internal`** (ICANN-reserved private-use TLD).
  The ENS-name-as-zone idea is **rejected**: Brave ships `.eth`
  interception (Settings → Web3 → Ethereum) and MetaMask resolves
  `.eth` in its in-app browser, so the zone would be hijacked in the
  URL bar on exactly the admin devices meant to use it — a real hazard
  for a cosmetic gain. The ENS name keeps its role in the commitment
  layer above. `mesh.internal` also drops the misleading `.wg` suffix,
  which names the transport phase 2 deletes.
- **Cert format: V2.** Extension ships nebula 1.10.3, Mobile Nebula and
  the hub embed 1.11.0, and `nebula-cert` 1.10.3 already defaults to
  `-version 2`; nothing in the toolchain predates 1.10, so V1 would be
  the deviation. V2 also unlocks IPv6 / multiple overlay addresses.
- **CA is network-unconstrained.** Nebula can restrict a CA's
  subordinate networks; we omit it so a future renumber re-mints only
  leaves instead of re-rooting the mesh. The constraint would only
  bound a leaked CA key, which forces re-rooting anyway.

## Verified claims (session evidence)

- NetBird's architecture (pion/ice fork, ice_bind STUN demux, BPF
  sharedsock, E2E-encrypted signal, 1k+-line conn state machine,
  WS+QUIC relay) — verified against source at HEAD, 2026-07-29.
- Sidero extensions exist for nebula, netbird, tailscale, zerotier
  (`siderolabs/extensions/network`).
- Mobile Nebula: official Play/App Store apps, standalone sites
  supported, actively maintained (pushed 2026-07-27).
- `slackhq/nebula/service` userspace embedding: **verified 2026-07-29**
  — spike passed on fly (lighthouse + relay + netstack dial, no TUN,
  v1.11.0).
