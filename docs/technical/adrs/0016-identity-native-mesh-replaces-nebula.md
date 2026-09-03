# ADR-0016: Identity-native mesh (iroh) replaces the nebula IP overlay

- Status: Accepted _(2026-09-03: direction committed; **implementation
  is gated** on the Phase 0 spike in `docs/mesh-v3-iroh.md` — a failed
  gate re-defers this ADR to Superseded-by-nothing/Deferred and reopens
  the nebula backlog. Nebula remains the deployed mesh until Phase 4.)_
- Date: 2026-09-03
- Supersedes (on Phase 4 completion): ADR-0002, ADR-0005
- Revises (on Phase 4 completion): ADR-0006 (relay-by-default carries
  over verbatim), ADR-0007 (per-request identity header replaces
  cert-group + source-IP inference), ADR-0009 (in-cluster gateway is
  the mesh side of ingress), ADR-0013 (app internals: iroh + fake-IP
  instead of the nebula AAR), ADR-0014 (policy renders to ALPN accept
  rules, not nebula firewall stanzas)
- Related: ADR-0012, ADR-0015 (mint-on-member pattern is the identity
  model here), `docs/sovereign-actor-protocol.md`, decision
  `talos-config-dlk`, epic `talos-config-359`

## Context and Problem Statement

Mesh v2 (nebula) is complete and healthy. Analysing what the mesh's
consumers actually need showed that IP is *fundamental* only where
third-party software lives (browsers, Jellyfin apps, kubelet) and
*incidental* everywhere we own the code (hub→node dials, admin CLIs).
Everything else the overlay carries — CIDR allocation, cert-baked
addresses, the mesh DNS server, the hub's gvisor netstack, certSAN
IPs — is IP bookkeeping, not networking. An identity-addressed
transport deletes the bookkeeping and gives per-request cryptographic
device identity, but pushes an IP adapter to every edge where
third-party software runs.

The design was written 2026-08-17 and deferred: coherent, no dead
ends, no operational driver. It was explicitly ruled out to migrate
for elegance alone. The question now: has a driver appeared?

## Decision Drivers

- Invariants 1, 2, 4, 5, 7: stateless membership, git as truth, mesh
  is post-bootstrap, single public entrypoint, no ephemeral facts in
  identity. Any replacement must hold all of them; ideally strengthen
  invariant 4's accepted wart (lighthouse as a hard dependency of
  cluster membership).
- The sovereign-actor sketch is moving from design to build. Its
  components — identity-addressed transport, membership as
  wallet-authorized delegation certs, a gateway that knows *which key*
  is asking, device apps that speak identity streams — are exactly
  v3's four bespoke components. Building them once, for the mesh, is
  the trigger the deferral named as dominant.
- Mesh-v2 discipline: gate on a spike with kill criteria, dual-plane
  migration, per-consumer cutover, event-based (not calendar) soak.
- Ownership cost: four components with no upstream (Talos extension,
  in-cluster gateway, desktop daemon, Android/TV VPN app) against a
  pre-1.0 dependency.

## Considered Options

### Option A: Stay on nebula; build the sovereign-actor stack beside it

Nebula as v0 transport for actors (the sketch's own v0 plan), mesh
untouched.

- Pros: zero migration risk; nebula proven; actor work starts today.
- Cons: two identity systems (nebula CA certs + actor delegation
  certs) over one wire; per-request device identity at the gateway
  stays inferred from source IP (ADR-0007); the IP bookkeeping stays
  forever; nebula ecosystem risk (Slack/Defined, Mobile Nebula on new
  Android) remains unhedged.

### Option B: Identity-native mesh (iroh), IP as device-local fiction

Members dialed by Ed25519 NodeId over QUIC; self-hosted relay in the
hub (443 + existing UDP port); membership = wallet-derived-issuer cert
binding NodeId → name + groups + expiry; name→NodeId map is a pure
function of git; k8s leaves the mesh onto declared static LAN IPs;
browsers/apps served by a fake-IP TUN (`198.18/15`, per device, never
coordinated). Policy becomes ALPN class × forward target. Full record:
`docs/mesh-v3-iroh.md`.

- Pros: deletes overlay CIDR, DNS server, netstack, cert-baked IPs,
  renumbering; per-request device identity; k8s membership no longer
  depends on the lighthouse (invariant 4 strengthened); components
  double as the actor stack's; short-expiry certs give the revocation
  story nebula lacks.
- Cons: four bespoke components owned forever; iroh pre-1.0 churn;
  reverses mesh-v2's cluster-endpoint-on-mesh step; not reversible in
  an afternoon; sealed hub now takes *all* remote connectivity down
  (relay identity derives from the master), not just the lighthouse.

### Option C: Localhost-proxy clients, no TUN

Ruled out in the design record: cookie/origin collapse on
`localhost:PORT`, every third-party app needs manual pointing; saves
only the VPN slot.

### Option D: n0-hosted relays/discovery

Ruled out: third-party infrastructure in the connectivity path
violates invariants 3/5 in spirit.

## Decision Outcome

Chosen: **Option B**, because the dominant pickup trigger fired
(sovereign-actor build-out) — the four components are now work we are
doing anyway, so v3 stops being migration-for-elegance and becomes the
cheapest way to build them once. Options C/D remain ruled out inside
B. Option A is the fallback if the gate fails.

Binding conditions carried from the design record:

- **Phase 0 spike gate** (`talos-config-359.1`), any failure ⇒ shelve:
  relay fully self-hosted with n0 infra verifiably absent; iroh inside
  Android `VpnService` sustains ≥80 Mbps 4K remux with acceptable
  battery; a Talos-extension node agent survives reboot and
  reconnects unaided; API-churn probe recorded.
- **Kill criteria** during migration: churn burn (two consecutive
  upgrades each >½ day, or a load-bearing API deprecated
  mid-migration); any consumer needing fleet-coordinated addressing
  again — that refutes the design, it is not a bug to fix.
- **ALPN routes, never authorizes** — coarse protocol/version/trust
  classes only; authority is checked post-accept against the
  membership cert.
- Nebula is untouched until Phase 4; every phase leaves the system
  fully working.

### Consequences

- Easier: revocation (expiry + renewal beat), per-request identity at
  the gateway, remote-path handling (relay + hole-punch are iroh's
  job), zero renumbering, `mesh.internal` becomes a client-side name
  map.
- Harder: we own a Talos extension, a gateway, a desktop daemon and an
  Android VPN app with no upstream; a sealed hub blocks all non-LAN
  traffic (raises `talos-config-fbb`); k8s needs router DHCP-pool
  exclusion for static LAN IPs.
- Follow-up: phase tree under `talos-config-359`; nebula-era backlog
  (`cjo en6 4ns 41b 6gq ap2 90a`) deferred on the gate; SideroLink
  (`2y7`) vs node agent must be decided before Phase 1.3; domain model
  §1 (Runner, Binding), §4 (Rendezvous) and glossary (Mesh zone)
  update when Phase 1 lands; Quint `enroll.qnt` and Nickel contracts
  follow the new membership/policy shapes.

### Confirmation

Right if: the Phase 0 gate passes on all four checks; Phase 1 exit
checks hold (node reboot reconnects unaided, hub re-seal reconverges
after unseal, laptop roaming holds relay path and re-punches LAN);
Phase 3 soak passes one natural re-seal, one node reboot and one full
remote-media session; and at Phase 4 the deleted code (`neb*`,
`nebderive`, `nebstack`, `cmd/nebup`, vendored nebula pkg) is not
missed by any consumer.

Wrong if: any kill criterion fires, or the sovereign-actor build stalls
so that the components are built for the mesh alone — at which point
the "migration for elegance" objection returns and the honest move is
to stop at the last fully-working phase.
