# Mesh v3 — identity-native networking (iroh) replaces nebula

_Design record + migration plan, 2026-08-17. Status: **deferred by
decision — do not build**. Product of a design conversation exploring
whether iroh-style identity addressing fits this project better than
an IP overlay. Conclusion: the architecture is coherent and has no
known dead ends, but there is no operational driver today; the plan
exists so it can be picked up when a trigger fires (see §Pickup
triggers). Nothing here is scheduled. Written against the repo as of
mesh-v2 completion (ADR-0007, ADR-0009, ADR-0013, ADR-0015)._

## Premise

Mesh v2 (nebula, `mesh-v2-nebula.md`) is complete and healthy. This
is not a repair; it is a re-founding of the transport on
**identity-addressed networking**: peers are dialed by public key
(iroh NodeId) over QUIC, not by overlay IP. The question it answers:
how much of the current mesh's complexity is *networking* and how
much is *IP bookkeeping* — and what the system looks like if the
bookkeeping is deleted.

## The analysis this plan rests on

### Fundamental vs incidental IP

| Mesh consumer today | IP is… | Consequence for v3 |
|---|---|---|
| kubelet → apiserver (worker join) | fundamental to k8s, **incidental to the mesh** (nodes share a LAN; mesh endpoint was chosen to escape DHCP drift, invariant 7) | k8s leaves the mesh: static LAN addressing declared in git |
| talosctl / kubectl | incidental-ish (third-party binaries wanting a socket) | local TCP bridge over an iroh stream (dumbpipe pattern) |
| Hub → node dials (bootstrap probes, /status) | incidental (own code; hub already fakes IP via gvisor netstack because fly has no TUN) | goes identity-native; netstack machinery deleted |
| Browser UIs (ArgoCD, media, SIWE flows) | fundamental (browsers are IP+DNS+TLS clients) | served via fake-IP TUN presentation on devices (below) |
| Jellyfin apps on phones/TV | fundamental (third-party apps dial URLs) | same fake-IP TUN presentation |
| `mesh.internal` DNS server | pure IP overhead | deleted; name→NodeId map is a pure function of git, shipped to clients |
| Overlay CIDR, cert-baked addresses, certSAN IPs | pure IP overhead | unrepresentable in v3 — identity is the address |

Key insight #1: **an IP overlay is a single universal
legacy-adapter** (the TUN) that makes all third-party software work
unmodified. Identity networking deletes the bookkeeping but pushes an
adapter to every edge where third-party software lives. v3 is worth
it only because those adapters double as components the
sovereign-actor design needs anyway.

Key insight #2: **IP survives only as device-local fiction.** The
client TUN answers DNS for `*.mesh.internal` with synthetic IPs
(fake-IP mode, `198.18.0.0/15`) minted per device, per session —
never coordinated, never in a cert, never in git. The fleet-wide
shared artifact shrinks to *name → NodeId*.

### What v3 buys / what it costs

Buys:
- Deletes: overlay CIDR + address allocation, mesh DNS server,
  cert-baked IPs, hub gvisor-netstack embedding (`nebstack`), the
  vendored nebula service package (ADR-0005), renumbering as a
  concept.
- Per-request cryptographic device identity at the gateway
  (successor to ADR-0007's cert-group + source-IP inference).
- Native alignment with the sovereign-actor sketch
  (`sovereign-actor-protocol.md`): NodeId ≈ actor identity, relays
  and lighthouses become ordinary self-hosted services, ALPN ≈ facet
  classes.
- Kills the accepted wart in invariant 4 (lighthouse as hard
  dependency of cluster membership) — k8s leaves the mesh.
- On mobile: iroh core needs no TUN; the VPN slot is spent only on
  the *presentation* layer we choose to ship.

Costs:
- Four bespoke components owned forever: node agent (Talos
  extension), in-cluster gateway, desktop daemon, Android/TV fake-IP
  VPN app. No upstream Sidero extension, no official mobile app.
- iroh is pre-1.0: API churn, a startup's roadmap, must run with n0's
  hosted discovery/relay infrastructure fully disabled.
- Reverses one piece of mesh-v2 phase 2 (cluster endpoint back to
  LAN, now static).
- Not reversible in an afternoon.

## Architecture (end-state)

```
wallet sig (EIP-191 over frozen message)
  → HKDF master (masterderive, unchanged)
    → membership issuer key            (talos-config/iroh/v1/issuer)
    → hub relay+gateway identity       (…/hub)
member NodeIds: Ed25519 keypairs minted ON the member (ADR-0015
pattern), never derived, never in transit. Membership = a
wallet-authorized cert binding NodeId → name + groups + expiry,
signed by the issuer. Same trust shape as the nebula CA; different
serialization.
```

- **Hub (fly.io)**: config-server + KMS + enrollment (unchanged) +
  **self-hosted iroh relay** (single public entrypoint preserved:
  HTTPS 443 carries the relay protocol; QUIC direct on the existing
  UDP port). No n0 infra anywhere: default discovery disabled, hub
  relay pinned as home relay in every member config. Hub also serves
  the signed name→NodeId map (successor of the DNS zone; pure
  function of git).
- **Nodes**: a Talos system extension we build — a small agent
  holding the node's NodeId, accepting ALPN-gated streams and
  forwarding to loopback targets per policy (apid :50001,
  kube-apiserver :6443, ingress :80, Jellyfin NodePort). Outbound
  dials to the hub relay only. This replaces the nebula extension.
- **In-cluster gateway**: iroh listener pod terminating identity
  streams and forwarding to Services — the mesh side of ingress
  (revises ADR-0009). Injects a verified per-request device-identity
  header (revises ADR-0007). Its NodeId enrolls like any member.
- **Desktop** (`irohup`, successor to `cmd/nebup`): daemon exposing
  (a) TCP bridges for talosctl/kubectl, (b) either SOCKS/PAC or the
  same fake-IP TUN as mobile for browser traffic. Enrollment flow
  unchanged: wallet signs, hub authorizes the locally-minted NodeId.
- **Android/TV app** (evolves the existing `android/` app,
  ADR-0013): keeps `VpnService`, drops the nebula AAR. TUN → gvisor
  netstack → fake-IP DNS for `*.mesh.internal` → per-flow iroh
  streams. Split routing: only synthetic range enters the tunnel.
  Jellyfin app and browser work unmodified with normal hostnames —
  distinct origins, working cookies, OIDC redirects intact.
- **k8s / Talos control plane**: off the mesh. Static LAN IPs
  declared in `talos/machines/<mac>/` (git-derived, satisfying
  invariants 2 and 7 — declared, not leased; requires DHCP pool
  exclusion on the router). certSANs carry LAN name/IP. Remote
  kubectl/talosctl ride the desktop daemon's bridges.
- **Policy**: `talos/mesh-policy.yaml` survives as the who×what
  table; "what" changes from ports to **ALPN classes + forward
  targets**. Compiled into gateway/agent accept rules instead of
  nebula firewall stanzas. Blocklist (`mesh-blocklist.txt`) becomes
  NodeId-based; expiry on membership certs finally gives the
  revocation story thread dc04e3e8 wanted (nebula has no CRL; certs
  with short expiry + renewal beat do).

### ALPN ↔ facet mapping (rules, from the actor-design review)

1. ALPN discriminates **protocol + version + trust class** only
   (e.g. `mesh/apid/v1`, `mesh/http/v1`, `mesh/frontdoor/v1`) —
   coarse classes, not fine-grained verbs. Fine dispatch stays in
   the (signed) application layer.
2. **ALPN is unsigned — it routes, it never authorizes.** Membership
   and per-class policy are checked after accept, against the
   membership cert. Mismatch between ALPN and the authenticated
   request = drop.
3. ALPN is visible in the QUIC ClientHello (relays and on-path
   observers see it). Coarse classes bound the metadata leak.

## Invariant compliance (desired-state/invariants.md)

| # | Verdict |
|---|---|
| 1 stateless identity/membership | Holds, same shape as today: keys minted on members, membership = wallet-derived-issuer cert, set bounded by wallet-signed acts, re-derivable minus member private keys |
| 2 git single source of truth | Holds: name→NodeId map, policy, static LAN IPs all git-derived; hub remembers nothing |
| 3 owner-held roots | Unchanged |
| 4 mesh is post-bootstrap | **Strengthened**: k8s membership no longer depends on the lighthouse/relay; provisioning stays HTTPS + device flow |
| 5 single public entrypoint | Holds: hub HTTPS (now also relay protocol) + existing UDP port for QUIC |
| 6 hardware-selected, human-ratified config | Unchanged |
| 7 no ephemeral facts in durable identity | Holds; static LAN IPs are declared config, and overlay addresses cease to exist |
| 8 secrets in memory only | Unchanged |

## Migration plan

Modeled on mesh-v2: gate on a spike, run dual planes, cut over
per-consumer, delete. Every phase leaves the system fully working;
nebula is not touched until phase 4.

### Phase 0 — spike gate (~2–4 days). Fail ⇒ whole plan shelved again.

All on scratch infra; no repo changes beyond a spike branch.
1. **Self-hosted relay on fly**: iroh relay in the hub process (or
   sidecar), 443 + UDP; two peers behind different NATs connect via
   relay and hole-punch LAN-direct when co-located. **All n0
   endpoints disabled and verified absent** (no DNS discovery, no
   default relays — packet-capture check).
2. **Android feasibility**: iroh (FFI/gomobile) inside a
   `VpnService` with gvisor netstack fake-IP; Jellyfin app streams a
   4K remux ≥ 80 Mbps sustained through it (kill-criterion parity
   with mesh-v2 #3), acceptable battery over a 2h stream.
3. **Talos extension proof**: minimal agent as a system extension —
   boots, dials the hub relay outbound, forwards one inbound
   ALPN-gated stream to apid; survives `talosctl reboot`.
4. **API-churn probe**: pin iroh version; note breaking-change rate
   over the spike window and the upgrade cost of one version bump.

### Phase 1 — identity plane beside nebula (dual plane)

- `irohderive`-equivalent: issuer key from `masterderive`; membership
  cert mint/verify (reuse the enrollment flow in `nebenroll.go` /
  `deviceflow` — the wallet-signature UX is unchanged).
- Hub: embed relay + membership issuance + name→NodeId map endpoint.
- Node extension on one node (cp1) via factory schematic +
  `talosctl upgrade` (no wipe).
- Desktop `irohup`: enrollment + talosctl/kubectl bridges.
- **Exit checks** (event-based, not calendar): node reboot →
  agent reconnects unaided; hub re-seal → identity plane reconverges
  after unseal; laptop roams LAN→cellular → relay path holds, LAN
  path re-punches direct.

### Phase 2 — consumers migrate, one at a time (each step reversible)

1. Admin CLI paths (talosctl/kubectl) onto bridges. Nebula still
   carries everything else.
2. Hub→node dials (/status, bootstrap probes) onto identity streams;
   delete the hub's netstack dial path (keep code until phase 4).
3. In-cluster gateway deployed; one low-stakes UI (e.g. Jackett)
   exposed through it end-to-end with the per-request identity
   header; then the rest of ingress (ADR-0009 revision).
4. Android app swaps nebula AAR for iroh+fake-IP internals (same
   APK, ADR-0013 pipeline); phones/TV re-enroll NodeIds via the
   existing device flow. Media verified: LAN-direct and remote-relay.
5. k8s endpoint off the mesh: static LAN IPs into machine configs +
   router DHCP exclusion; certSANs → LAN names; talosconfig/
   kubeconfig re-pointed (reverses mesh-v2 phase-2 step 2; sequenced
   late because it is the only step touching cluster availability).

### Phase 3 — soak

All traffic on the identity plane; nebula idle but installed. Wait
for one natural hub re-seal and one node reboot to pass, plus one
full remote-media session. No calendar minimum — event coverage, the
mesh-v2 lesson.

### Phase 4 — deletion

- Factory schematic without the nebula extension; upgrade nodes.
- Delete: `config-server/mesh/neb*.go`, `nebderive`, `nebstack`,
  `nebenroll.go`, `cmd/nebup`, vendored nebula service pkg, nebula
  AAR build (`android/build-aar.sh` nebula parts), DNS shim in
  `mobile/`.
- ADRs: new ADR "identity-native mesh replaces nebula" superseding
  0002/0005; revisions noted against 0006 (relay-by-default carries
  over verbatim), 0007 (identity header), 0009 (gateway), 0013 (app
  internals), 0014 (policy render targets).
- Update `desired-state/{goals,invariants}.md`, `domain-model.md`,
  `deployed-state.md`; fold this file's outcome into an exploration-
  log entry.

## Kill criteria (any one fires ⇒ stop, keep nebula, close the spike)

1. Spike check 1 fails: relay cannot be fully self-hosted / n0 infra
   cannot be cleanly disabled.
2. Spike check 2 fails: mobile fake-IP path can't hold the 80 Mbps
   floor or wrecks battery.
3. Spike check 3 fails: no viable Talos extension path.
4. Churn burn: two consecutive iroh upgrades each cost >½ day of
   breakage, or a load-bearing API is deprecated mid-migration.
5. During phase 2: any consumer needs a workaround that reintroduces
   fleet-coordinated addressing — that's the design refuted, not a
   bug to fix.

## Pickup triggers (what makes this worth starting)

- Sovereign-actor work moves from sketch to build (dominant trigger —
  the gateway, device apps, and membership certs are its components).
- Nebula ecosystem risk materializes: Slack/Defined stagnation, or
  Mobile Nebula breaking on an Android release (matters even with the
  custom app: the AAR is upstream code).
- A concrete need for per-request device identity that ADR-0007's
  network-layer inference can't serve.
- iroh reaches 1.0 / API stability, removing kill-criterion-4 risk.

## Ruled out (with reasons)

| Option | Why not |
|---|---|
| Localhost-proxy-only clients (no TUN) | Cookie/origin collapse on `localhost:PORT`; every third-party app needs manual pointing; the one thing it saved (VPN slot) is worth less than universal compatibility |
| ALPN as authorization | ALPN is unsigned ClientHello data; routing hint only |
| Fine-grained facet ALPNs | Metadata leak to relays/on-path observers; coarse trust classes only |
| n0-hosted relays/discovery | Third-party infra in the connectivity path; violates invariants 3/5 in spirit |
| Public hub HTTPS reverse-proxy for remote media (no device client) | Loses the network-layer auth factor entirely; puts app auth on the public edge |
| Keeping k8s on the identity mesh | Kubernetes is IP-native; bridging it means rebuilding an IP overlay — the design refuted |
| Migrating for its own sake, absent triggers | Recorded decision: no operational driver; elegance is not a driver (mesh-v2 discipline) |

## Open questions (resolve during spike/phase 1)

- Membership cert format: reuse the sovereign-actor delegation-cert
  JSON shape (aligning the two designs) vs a minimal bespoke blob.
  Leaning: the delegation shape, `can: member`, caveats = groups.
- Renewal beat for membership certs (revocation story): 90 days to
  match device re-enrollment cadence, or shorter now that renewal is
  a background dial instead of a human act?
- Desktop presentation: SOCKS/PAC (less code) vs same fake-IP TUN as
  mobile (UX symmetry). Leaning: start SOCKS/PAC, upgrade if friction.
- Gateway placement: one per cluster vs per-node agents also serving
  ingress. Leaning: single gateway pod + node agents only for
  apid/system targets.
- Does the hub's mesh HTTP surface (`/config`, `/hosts`, `/policy`)
  move to an ALPN class or stay HTTPS-only? Leaning: ALPN class
  `mesh/hub/v1`, same handlers.
